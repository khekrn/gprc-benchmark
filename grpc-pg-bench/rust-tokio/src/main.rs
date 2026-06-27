//! Rust gRPC server for `bench.v1.CommandService` — the Rust side of the
//! benchmark. Mirrors the Go and Kotlin/Vert.x stacks: receive a command, do a
//! tiny CPU touch (FNV-1a checksum of the payload), then write/read Postgres.
//!
//! Stack: tonic (gRPC) + tokio-postgres + deadpool. The logic is split into
//! small modules so each piece reads on its own:
//!   config  — env-sourced settings           proto   — generated gRPC stubs
//!   fnv     — the checksum                    db      — pool + SQL operations
//!   service — the three RPC methods           server  — HTTP/2 transport
//!
//! `main` owns only the runtime bootstrap and the startup sequence.

mod cache;
mod config;
mod db;
mod fnv;
mod proto;
mod server;
mod service;

use cache::StateCache;
use config::Config;
use db::Db;

// `#[hotpath::main]` (feature-gated) installs the profiler and prints the
// per-function timing report when main returns (i.e. after graceful shutdown).
// No-op in the normal build.
#[cfg_attr(feature = "hotpath", hotpath::main)]
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = Config::from_env();

    // Multi-threaded Tokio runtime. Worker count = GOMAXPROCS / VERTX_EVENT_LOOPS
    // (default 2) so the Rust server gets the same CPU budget as the others.
    // Built by hand (not `#[tokio::main]`) so the worker count is a runtime value.
    let mut builder = tokio::runtime::Builder::new_multi_thread();
    builder
        .worker_threads(cfg.worker_threads)
        .max_blocking_threads(cfg.max_blocking_threads)
        .enable_all()
        .thread_name("rust-bench");

    // Optional core pinning (RUST_PIN_CORES, default on): pin each worker to one
    // CPU so it stops migrating between the 2 cores — the same cache-locality win
    // the JVM stacks got from 1 Netty I/O thread. Under `taskset -c 2,3` the
    // visible core set is just {2,3}, so worker 0 -> core 2, worker 1 -> core 3.
    if cfg.pin_cores {
        if let Some(core_ids) = core_affinity::get_core_ids() {
            if !core_ids.is_empty() {
                let next = std::sync::Arc::new(std::sync::atomic::AtomicUsize::new(0));
                builder.on_thread_start(move || {
                    let i = next.fetch_add(1, std::sync::atomic::Ordering::Relaxed) % core_ids.len();
                    core_affinity::set_for_current(core_ids[i]);
                });
            }
        }
    }

    let rt = builder.build()?;
    rt.block_on(run(cfg))
}

async fn run(cfg: Config) -> Result<(), Box<dyn std::error::Error>> {
    init_tracing();

    let db = Db::connect(&cfg.dsn, cfg.pool_max)?;
    db.warmup(cfg.pool_min).await?;

    // Optional Redis read-through cache (REDIS_ENABLED=true).
    let db = if cfg.redis_enabled {
        let cache = StateCache::connect(&cfg.redis_host, cfg.redis_port, cfg.redis_ttl_secs).await?;
        tracing::info!(
            host = %cfg.redis_host,
            port = cfg.redis_port,
            ttl_secs = cfg.redis_ttl_secs,
            "redis read-through cache enabled"
        );
        db.with_cache(Some(cache))
    } else {
        db
    };

    server::serve(&cfg, db).await
}

/// One structured logger; no per-RPC logging (it would skew the benchmark).
/// Honours `RUST_LOG`, defaults to `info`.
fn init_tracing() {
    tracing_subscriber::fmt()
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("info")),
        )
        .init();
}
