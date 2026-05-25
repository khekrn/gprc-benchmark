// Rust gRPC server for the bench.v1.CommandService — Rust side of the
// benchmark. Mirrors the Go and Kotlin/Vert.x implementations:
//   1. Receive Execute(CommandRequest).
//   2. Tiny CPU touch: FNV-1a checksum of the payload.
//   3. INSERT one row into Postgres via tokio-postgres + deadpool.
//   4. Return id + checksum + receive timestamp.
//
// The HTTP/2 listener is owned by **axum** — we mount the tonic-generated
// CommandService as a `tower::Service` inside an axum::Router. tonic still
// does the protobuf framing (so the wire protocol matches Go/Kotlin), but
// the listener, request routing, and graceful-shutdown plumbing all go
// through axum.

use std::env;
use std::time::{Duration, SystemTime, UNIX_EPOCH};

use axum::Router;
use deadpool_postgres::{Config as PoolCfg, ManagerConfig, Pool, RecyclingMethod, Runtime};
use fnv::FnvHasher;
use hyper_util::rt::{TokioExecutor, TokioIo};
use hyper_util::server::conn::auto;
use hyper_util::service::TowerToHyperService;
use std::hash::Hasher;
use std::sync::Arc;
use tokio::net::TcpListener;
use tokio::sync::Notify;
use tokio_postgres::NoTls;
use tonic::service::Routes;
use tonic::{Request, Response, Status};
use tower::Service;
use tracing::{info, warn};

// Generated tonic stubs from proto/command.proto. Compiled by build.rs.
pub mod bench_v1 {
    tonic::include_proto!("bench.v1");
}
use bench_v1::command_service_server::{CommandService, CommandServiceServer};
use bench_v1::{CommandRequest, CommandResponse};

const INSERT_SQL: &str = "
INSERT INTO commands (workflow_id, command_type, payload, seq, checksum)
VALUES ($1, $2, $3, $4, $5)
RETURNING id";

struct Svc {
    pool: Pool,
}

#[tonic::async_trait]
impl CommandService for Svc {
    async fn execute(
        &self,
        req: Request<CommandRequest>,
    ) -> Result<Response<CommandResponse>, Status> {
        let r = req.into_inner();

        // "Small processing": FNV-1a 32-bit hash over the payload bytes.
        // Matches Go's hash/fnv and Kotlin's Fnv.kt.
        let mut h = FnvHasher::default();
        h.write(r.payload.as_bytes());
        let checksum = h.finish() as u32;

        let recv_micros = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map(|d| d.as_micros() as i64)
            .unwrap_or(0);

        // Get a connection from the pool, run the prepared INSERT.
        // tokio-postgres caches the prepared statement per connection, so
        // subsequent inserts on the same connection skip parse/plan.
        let client = self.pool.get().await.map_err(internal)?;
        let stmt = client.prepare_cached(INSERT_SQL).await.map_err(internal)?;
        let row = client
            .query_one(
                &stmt,
                &[
                    &r.workflow_id,
                    &r.command_type,
                    &r.payload,
                    &r.seq,
                    &(checksum as i64),
                ],
            )
            .await
            .map_err(internal)?;
        let id: i64 = row.get(0);

        Ok(Response::new(CommandResponse {
            id,
            checksum,
            received_at_micros: recv_micros,
        }))
    }
}

fn internal<E: std::fmt::Display>(e: E) -> Status {
    Status::internal(format!("{e}"))
}

// --- env helpers ---
fn env_or(key: &str, default: &str) -> String {
    env::var(key).unwrap_or_else(|_| default.to_string())
}
fn env_int_or(key: &str, default: usize) -> usize {
    env::var(key)
        .ok()
        .and_then(|v| v.parse().ok())
        .unwrap_or(default)
}

fn build_pool(dsn: &str, pool_max: usize) -> Result<Pool, Box<dyn std::error::Error>> {
    // deadpool-postgres takes a config struct; we point it at the DSN, cap
    // the pool, and ask for the Fast recycling method (skip a per-checkout
    // ping — jackc/pgx and vertx-pg-client default to no health-check-per-
    // checkout either, so this matches them).
    let mut cfg = PoolCfg::new();
    cfg.url = Some(dsn.to_string());
    cfg.manager = Some(ManagerConfig {
        recycling_method: RecyclingMethod::Fast,
    });
    let mut pool_cfg = deadpool_postgres::PoolConfig::new(pool_max);
    pool_cfg.timeouts = deadpool_postgres::Timeouts {
        wait: Some(Duration::from_secs(5)),
        create: Some(Duration::from_secs(5)),
        recycle: Some(Duration::from_secs(5)),
    };
    cfg.pool = Some(pool_cfg);
    Ok(cfg.create_pool(Some(Runtime::Tokio1), NoTls)?)
}

async fn warm_pool(pool: &Pool, n: usize) -> Result<(), Box<dyn std::error::Error>> {
    // Mirror Db.warmup(min) on the Kotlin side and pgxpool's MinConns: hold
    // N clients at startup so the first measured phase isn't paying for
    // lazy connection creation.
    let mut held = Vec::with_capacity(n);
    for _ in 0..n {
        held.push(pool.get().await?);
    }
    // Drop, returning all to the pool.
    drop(held);
    Ok(())
}

fn main() -> Result<(), Box<dyn std::error::Error>> {
    // 2 worker threads to match GOMAXPROCS=2 and VERTX_EVENT_LOOPS=2.
    // Override with RUST_WORKER_THREADS.
    let workers = env_int_or("RUST_WORKER_THREADS", 2);
    let rt = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(workers)
        .enable_all()
        .thread_name("rust-bench")
        .build()?;
    rt.block_on(run(workers))
}

async fn run(workers: usize) -> Result<(), Box<dyn std::error::Error>> {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .init();

    let dsn = env_or(
        "DATABASE_URL",
        "postgres://postgres:sam@127.0.0.1:5432/bench",
    );
    let addr_str = env_or("LISTEN_ADDR", "127.0.0.1:50053");
    let pool_max = env_int_or("PG_POOL_MAX", 16);
    let pool_min = env_int_or("PG_POOL_MIN", 4);

    let pool = build_pool(&dsn, pool_max)?;
    warm_pool(&pool, pool_min).await?;

    // Standard gRPC health service — wired up for parity with what we'd ship.
    let (mut health_reporter, health_service) = tonic_health::server::health_reporter();
    health_reporter
        .set_serving::<CommandServiceServer<Svc>>()
        .await;

    // Build the gRPC routing table (tonic), then hand it to axum as a Router.
    // Same wire protocol as Go/Kotlin (HTTP/2 + protobuf + gRPC framing),
    // but the listener + request dispatch are axum's responsibility.
    //
    // Why not `axum::serve`: its default hyper-util builder auto-negotiates
    // HTTP/1.1 vs HTTP/2 by sniffing the PRI preface, which adds ~40 ms of
    // fixed latency per connection for h2c gRPC traffic. gRPC clients always
    // speak HTTP/2 with prior knowledge, so we run an explicit `http2_only`
    // accept loop. The Router itself is still pure axum.
    let svc = CommandServiceServer::new(Svc { pool });
    let app: Router = Routes::new(svc).add_service(health_service).into_axum_router();

    let listener = TcpListener::bind(&addr_str).await?;
    info!(
        addr = %addr_str,
        pool_min,
        pool_max,
        workers,
        "rust-tokio (axum) server listening"
    );

    // SIGTERM / SIGINT: stop accepting new connections and let in-flight
    // requests finish. Notify is a cheap many-listeners wakeup.
    let shutdown = Arc::new(Notify::new());
    {
        let shutdown = shutdown.clone();
        tokio::spawn(async move {
            use tokio::signal::unix::{SignalKind, signal};
            let mut term = signal(SignalKind::terminate()).expect("install SIGTERM");
            let mut int = signal(SignalKind::interrupt()).expect("install SIGINT");
            tokio::select! {
                _ = term.recv() => {}
                _ = int.recv()  => {}
            }
            info!("shutdown signal received, draining in-flight RPCs");
            shutdown.notify_waiters();
        });
    }

    // `http2_only()` consumes self in hyper-util 0.1, so build once and
    // clone the configured builder per connection.
    let http2 = auto::Builder::new(TokioExecutor::new()).http2_only();
    let in_flight = Arc::new(tokio::sync::Semaphore::new(0));
    loop {
        tokio::select! {
            _ = shutdown.notified() => break,
            accept = listener.accept() => {
                let (stream, _peer) = match accept {
                    Ok(v) => v,
                    Err(e) => { warn!(?e, "accept error"); continue; }
                };
                // Disable Nagle: gRPC writes are tiny, latency matters.
                let _ = stream.set_nodelay(true);

                let http2 = http2.clone();
                let mut app = app.clone();
                let in_flight = in_flight.clone();
                let shutdown = shutdown.clone();
                tokio::spawn(async move {
                    // Adapt our `tower::Service<Request<Incoming>>` (the axum
                    // Router) into a `hyper::service::Service` so hyper-util
                    // can drive it on this connection.
                    let hyper_svc = TowerToHyperService::new(
                        tower::service_fn(move |req| app.call(req))
                    );
                    let conn = http2.serve_connection(TokioIo::new(stream), hyper_svc);
                    tokio::pin!(conn);
                    let permit = in_flight.clone();
                    let _g = permit;
                    tokio::select! {
                        res = &mut conn => {
                            if let Err(e) = res {
                                // Quiet down expected client disconnects.
                                let s = format!("{e}");
                                if !s.contains("CANCELED") && !s.contains("GOAWAY") {
                                    warn!(err = %s, "h2 connection error");
                                }
                            }
                        }
                        _ = shutdown.notified() => {
                            conn.as_mut().graceful_shutdown();
                            let _ = (&mut conn).await;
                        }
                    }
                });
            }
        }
    }

    info!("graceful stop complete");
    Ok(())
}
