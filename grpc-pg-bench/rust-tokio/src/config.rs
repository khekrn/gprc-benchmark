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
    /// Max tokio blocking-pool threads. We do no `spawn_blocking`, so keep this
    /// small (the pool is lazy, but this caps it explicitly).
    pub max_blocking_threads: usize,
    /// Pin each tokio worker thread to a CPU core (cache locality). RUST_PIN_CORES.
    pub pin_cores: bool,
    /// HTTP port for the co-hosted axum REST /health server.
    pub http_port: u16,
    /// Redis read-through cache for GetState — off unless REDIS_ENABLED=true.
    pub redis_enabled: bool,
    pub redis_host: String,
    pub redis_port: u16,
    pub redis_ttl_secs: u64,
}

impl Config {
    pub fn from_env() -> Self {
        Self {
            dsn: env_or("DATABASE_URL", "postgres://postgres:sam@127.0.0.1:5432/bench"),
            listen_addr: env_or("LISTEN_ADDR", "127.0.0.1:50053"),
            pool_max: env_int_or("PG_POOL_MAX", 16),
            pool_min: env_int_or("PG_POOL_MIN", 4),
            worker_threads: env_int_or("RUST_WORKER_THREADS", 2),
            max_blocking_threads: env_int_or("RUST_MAX_BLOCKING_THREADS", 4),
            pin_cores: env::var("RUST_PIN_CORES").map(|v| v != "false" && v != "0").unwrap_or(true),
            http_port: env_int_or("HTTP_PORT", 8082) as u16,
            redis_enabled: env::var("REDIS_ENABLED").map(|v| v == "true").unwrap_or(false),
            redis_host: env_or("REDIS_HOST", "127.0.0.1"),
            redis_port: env_int_or("REDIS_PORT", 6379) as u16,
            redis_ttl_secs: env_int_or("REDIS_TTL_SECONDS", 300) as u64,
        }
    }
}

fn env_or(key: &str, default: &str) -> String {
    env::var(key).unwrap_or_else(|_| default.to_string())
}

fn env_int_or(key: &str, default: usize) -> usize {
    env::var(key).ok().and_then(|v| v.parse().ok()).unwrap_or(default)
}
