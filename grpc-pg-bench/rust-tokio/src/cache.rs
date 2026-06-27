//! Redis read-through cache for `workflow_state` — the Rust twin of spring-vt's
//! `RedisStateCache` and go-pgx's go-redis cache, for the cross-runtime A/B.
//!
//! Uses a redis-rs `MultiplexedConnection`: one connection that pipelines all
//! commands concurrently, the closest analog to Lettuce's shared connection
//! (and unlike go-redis's pool). It's cheap to `clone()` — a clone shares the
//! same underlying connection — so each call clones to get a `&mut`.
//!
//! Same wire format as the other stacks: key `wf:state:{id}`, value
//! `version|micros|state` (state last, so it may contain the delimiter).
//! Resilient: any Redis error degrades to Postgres (a miss), never fails the RPC.

use redis::AsyncCommands;

use crate::db::StateRow;

#[derive(Clone)]
pub struct StateCache {
    conn: redis::aio::MultiplexedConnection,
    ttl_secs: u64,
}

impl StateCache {
    pub async fn connect(
        host: &str,
        port: u16,
        ttl_secs: u64,
    ) -> Result<Self, Box<dyn std::error::Error>> {
        let client = redis::Client::open(format!("redis://{host}:{port}/"))?;
        let conn = client.get_multiplexed_async_connection().await?;
        Ok(Self { conn, ttl_secs })
    }

    /// Cache lookup. `None` on a miss OR any Redis error (degrade to Postgres).
    pub async fn get(&self, workflow_id: &str) -> Option<StateRow> {
        let mut conn = self.conn.clone();
        let v: Option<String> = conn.get(state_key(workflow_id)).await.ok().flatten();
        decode_state(&v?)
    }

    /// Populate the cache for an existing row (best-effort; errors ignored).
    pub async fn put(&self, workflow_id: &str, row: &StateRow) {
        let mut conn = self.conn.clone();
        let _: redis::RedisResult<()> = conn
            .set_ex(state_key(workflow_id), encode_state(row), self.ttl_secs)
            .await;
    }

    /// Drop the cached entry after a write (best-effort; TTL is the backstop).
    pub async fn invalidate(&self, workflow_id: &str) {
        let mut conn = self.conn.clone();
        let _: redis::RedisResult<i64> = conn.del(state_key(workflow_id)).await;
    }
}

fn state_key(workflow_id: &str) -> String {
    format!("wf:state:{workflow_id}")
}

fn encode_state(row: &StateRow) -> String {
    format!("{}|{}|{}", row.version, row.updated_at_micros, row.state)
}

/// Parse a `version|micros|state` value; `None` if malformed (treated as miss).
fn decode_state(v: &str) -> Option<StateRow> {
    let p1 = v.find('|')?;
    let rest = &v[p1 + 1..];
    let p2 = rest.find('|')?;
    let version = v[..p1].parse().ok()?;
    let updated_at_micros = rest[..p2].parse().ok()?;
    Some(StateRow {
        state: rest[p2 + 1..].to_string(),
        version,
        updated_at_micros,
    })
}
