//! Postgres access layer: a `deadpool` connection pool over `tokio-postgres`,
//! plus the three operations the service exposes. Keeping the SQL and the
//! pool here (and out of `service.rs`) mirrors the Go `Db`/Kotlin `Db.kt`
//! split and keeps the gRPC layer free of database concerns.

use std::time::Duration;

use deadpool_postgres::{
    Config as PoolCfg, ManagerConfig, Object, Pool, PoolConfig, RecyclingMethod, Runtime, Timeouts,
};
use tokio_postgres::NoTls;
use tonic::Status;

use crate::cache::StateCache;

// SQL kept byte-identical to go-pgx/main.go and the JVM stacks so every stack
// hits the planner with the same prepared-statement text and the same plan.
const INSERT_COMMAND_SQL: &str = "INSERT INTO commands (workflow_id, command_type, payload, seq, checksum) VALUES ($1, $2, $3, $4, $5) RETURNING id";
const UPSERT_STATE_SQL: &str = "INSERT INTO workflow_state (workflow_id, state, version, updated_at) VALUES ($1, $2, 1, now()) ON CONFLICT (workflow_id) DO UPDATE SET state = EXCLUDED.state, version = workflow_state.version + 1, updated_at = now()";
const INSERT_OUTBOX_SQL: &str = "INSERT INTO outbox (workflow_id, event_type, payload) VALUES ($1, $2, $3)";
const SELECT_STATE_SQL: &str = "SELECT state, version, (EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT AS updated_at_micros FROM workflow_state WHERE workflow_id = $1";

/// One row of `workflow_state`, already projected to the shape `GetState`
/// returns over the wire (timestamp pre-converted to micros server-side, like
/// the Go stack, to skip a TIMESTAMPTZ marshal).
pub struct StateRow {
    pub state: String,
    pub version: i64,
    pub updated_at_micros: i64,
}

/// Cheap to clone — `Pool` is `Arc`-backed internally, so cloning just bumps a
/// refcount. Cloned once per gRPC service handle.
#[derive(Clone)]
pub struct Db {
    pool: Pool,
    /// Read-through cache for workflow_state: `Some` when REDIS_ENABLED=true,
    /// else `None` (every read hits Postgres — the original behaviour).
    cache: Option<StateCache>,
}

impl Db {
    /// Build the pool from a DSN. `RecyclingMethod::Fast` skips a per-checkout
    /// `SELECT 1` health ping — jackc/pgx and vertx-pg-client don't ping on
    /// checkout either, so this keeps the pools comparable.
    pub fn connect(dsn: &str, pool_max: usize) -> Result<Self, Box<dyn std::error::Error>> {
        let mut cfg = PoolCfg::new();
        cfg.url = Some(dsn.to_string());
        cfg.manager = Some(ManagerConfig {
            recycling_method: RecyclingMethod::Fast,
        });

        let mut pool_cfg = PoolConfig::new(pool_max);
        pool_cfg.timeouts = Timeouts {
            wait: Some(Duration::from_secs(5)),
            create: Some(Duration::from_secs(5)),
            recycle: Some(Duration::from_secs(5)),
        };
        cfg.pool = Some(pool_cfg);

        let pool = cfg.create_pool(Some(Runtime::Tokio1), NoTls)?;
        Ok(Self { pool, cache: None })
    }

    /// Attach (or clear) the Redis read-through cache. Builder-style so `main`
    /// can add it after `connect`/`warmup` only when REDIS_ENABLED=true.
    pub fn with_cache(mut self, cache: Option<StateCache>) -> Self {
        self.cache = cache;
        self
    }

    /// Hold `n` connections open at startup, then release them, so the first
    /// measured phase isn't paying for lazy connection creation. Mirrors
    /// pgxpool's `MinConns` and Kotlin's `Db.warmup`.
    pub async fn warmup(&self, n: usize) -> Result<(), Box<dyn std::error::Error>> {
        let mut held = Vec::with_capacity(n);
        for _ in 0..n {
            held.push(self.pool.get().await?);
        }
        drop(held); // returns every connection to the pool
        Ok(())
    }

    #[cfg_attr(feature = "hotpath", hotpath::measure)]
    async fn get(&self) -> Result<Object, Status> {
        self.pool.get().await.map_err(internal)
    }

    /// `Execute` hot path: one autocommit INSERT, returns the generated id.
    /// `prepare_cached` reuses the per-connection prepared statement, so after
    /// the first call on a connection there's no parse/plan round trip.
    #[cfg_attr(feature = "hotpath", hotpath::measure)]
    pub async fn insert_command(
        &self,
        workflow_id: &str,
        command_type: &str,
        payload: &str,
        seq: i64,
        checksum: i64,
    ) -> Result<i64, Status> {
        let client = self.get().await?;
        let stmt = client.prepare_cached(INSERT_COMMAND_SQL).await.map_err(internal)?;
        let row = client
            .query_one(&stmt, &[&workflow_id, &command_type, &payload, &seq, &checksum])
            .await
            .map_err(internal)?;
        Ok(row.get(0))
    }

    /// `ExecuteTx`: INSERT command + UPSERT state + INSERT outbox in one
    /// transaction. Statements are awaited *sequentially* — BEGIN, INSERT,
    /// UPSERT, INSERT, COMMIT (five round trips), deliberately NOT pipelined,
    /// so this matches the JDBC/virtual-thread stacks that physically cannot
    /// pipeline a transaction. See go-pgx's ExecuteTx for the full rationale.
    pub async fn execute_tx(
        &self,
        workflow_id: &str,
        command_type: &str,
        payload: &str,
        seq: i64,
        checksum: i64,
    ) -> Result<i64, Status> {
        let mut client = self.get().await?;

        // Prepare (cached) before opening the transaction: `prepare_cached`
        // borrows the client immutably and `transaction()` needs it mutably,
        // so we resolve the statements first and hold the owned handles.
        let insert = client.prepare_cached(INSERT_COMMAND_SQL).await.map_err(internal)?;
        let upsert = client.prepare_cached(UPSERT_STATE_SQL).await.map_err(internal)?;
        let outbox = client.prepare_cached(INSERT_OUTBOX_SQL).await.map_err(internal)?;

        let tx = client.transaction().await.map_err(internal)?;
        let row = tx
            .query_one(&insert, &[&workflow_id, &command_type, &payload, &seq, &checksum])
            .await
            .map_err(internal)?;
        let id: i64 = row.get(0);
        tx.execute(&upsert, &[&workflow_id, &command_type]).await.map_err(internal)?;
        tx.execute(&outbox, &[&workflow_id, &command_type, &payload]).await.map_err(internal)?;
        tx.commit().await.map_err(internal)?;
        // workflow_state changed → evict the cached entry (cache-aside on write);
        // post-commit so a rolled-back TX never evicts. TTL is the backstop.
        if let Some(cache) = &self.cache {
            cache.invalidate(workflow_id).await;
        }
        Ok(id)
    }

    /// `GetState`: single lookup by workflow_id, read THROUGH the Redis cache
    /// when enabled. Cache hit returns immediately; on a miss we read Postgres
    /// and populate the cache for existing rows (misses are not cached).
    /// `query_opt` returns `None` for a missing row → `found = false`.
    pub async fn get_state(&self, workflow_id: &str) -> Result<Option<StateRow>, Status> {
        if let Some(cache) = &self.cache {
            if let Some(row) = cache.get(workflow_id).await {
                return Ok(Some(row));
            }
        }
        let client = self.get().await?;
        let stmt = client.prepare_cached(SELECT_STATE_SQL).await.map_err(internal)?;
        let row = client.query_opt(&stmt, &[&workflow_id]).await.map_err(internal)?;
        let result = row.map(|r| StateRow {
            state: r.get(0),
            version: r.get(1),
            updated_at_micros: r.get(2),
        });
        if let (Some(cache), Some(row)) = (&self.cache, &result) {
            cache.put(workflow_id, row).await;
        }
        Ok(result)
    }
}

/// Collapse any error into a gRPC `INTERNAL` status. The benchmark never logs
/// per-RPC (it would skew the numbers), so the message is all the client sees.
fn internal<E: std::fmt::Display>(e: E) -> Status {
    Status::internal(e.to_string())
}
