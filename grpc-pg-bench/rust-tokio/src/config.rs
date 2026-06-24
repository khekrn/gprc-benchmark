//! Runtime configuration, sourced entirely from environment variables with
//! defaults that match the other stacks (`scripts/config.sh`). No config file,
//! no clap — the benchmark harness only ever sets these through the env.

use std::env;

pub struct Config {
    /// libpq-style connection string for tokio-postgres.
    pub dsn: String,
    /// `host:port` the gRPC server binds to.
    pub listen_addr: String,
    /// Upper bound on pooled Postgres connections (matches `PG_POOL_MAX`).
    pub pool_max: usize,
    /// Connections held open at startup (matches `PG_POOL_MIN`).
    pub pool_min: usize,
    /// Tokio worker threads — pinned to GOMAXPROCS / VERTX_EVENT_LOOPS for a
    /// fair comparison on the 2-core box.
    pub worker_threads: usize,
}

impl Config {
    pub fn from_env() -> Self {
        Self {
            dsn: env_or("DATABASE_URL", "postgres://postgres:sam@127.0.0.1:5432/bench"),
            listen_addr: env_or("LISTEN_ADDR", "127.0.0.1:50053"),
            pool_max: env_int_or("PG_POOL_MAX", 16),
            pool_min: env_int_or("PG_POOL_MIN", 4),
            worker_threads: env_int_or("RUST_WORKER_THREADS", 2),
        }
    }
}

fn env_or(key: &str, default: &str) -> String {
    env::var(key).unwrap_or_else(|_| default.to_string())
}

fn env_int_or(key: &str, default: usize) -> usize {
    env::var(key).ok().and_then(|v| v.parse().ok()).unwrap_or(default)
}
