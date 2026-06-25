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

mod config;
mod db;
mod fnv;
mod proto;
mod server;
mod service;

use config::Config;
use db::Db;

// `#[hotpath::main]` (feature-gated) installs the profiler and prints the
// per-function timing report when main returns (i.e. after graceful shutdown).
// No-op in the normal build.
#[cfg_attr(feature = "hotpath", hotpath::main)]
fn main() -> Result<(), Box<dyn std::error::Error>> {
    let cfg = Config::from_env();

    // Multi-threaded Tokio runtime. Worker count is pinned to the same value
    // as GOMAXPROCS / VERTX_EVENT_LOOPS (default 2) so the Rust server gets the
    // same CPU budget as the others. Built by hand rather than via
    // `#[tokio::main]` so the worker count is a runtime value, not a constant.
    let rt = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(cfg.worker_threads)
        .enable_all()
        .thread_name("rust-bench")
        .build()?;

    rt.block_on(run(cfg))
}

async fn run(cfg: Config) -> Result<(), Box<dyn std::error::Error>> {
    init_tracing();

    let db = Db::connect(&cfg.dsn, cfg.pool_max)?;
    db.warmup(cfg.pool_min).await?;

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
