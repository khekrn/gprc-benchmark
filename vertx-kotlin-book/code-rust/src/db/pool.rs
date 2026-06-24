//! Connection-string construction and deadpool-postgres pool setup.

use deadpool_postgres::{Config as PoolConfig, ManagerConfig, Pool, RecyclingMethod, Runtime};
use tokio_postgres::NoTls;

use crate::config;

/// Builds a libpq-style URL from the DB config. `sslmode=disable` is fine for
/// local/dev; set it via the URL for production.
pub fn conn_string(c: &config::Db) -> String {
    format!(
        "postgres://{}:{}@{}:{}/{}?sslmode=disable",
        c.user, c.password, c.host, c.port, c.database
    )
}

/// Builds the deadpool-postgres pool. tokio-postgres caches prepared statements
/// per connection and pipelines concurrent queries over one connection.
pub fn new_pool(c: &config::Db) -> anyhow::Result<Pool> {
    let mut cfg = PoolConfig::new();
    cfg.host = Some(c.host.clone());
    cfg.port = Some(c.port);
    cfg.dbname = Some(c.database.clone());
    cfg.user = Some(c.user.clone());
    cfg.password = Some(c.password.clone());
    cfg.manager = Some(ManagerConfig { recycling_method: RecyclingMethod::Fast });
    if c.pool_max_size > 0 {
        cfg.pool = Some(deadpool_postgres::PoolConfig::new(c.pool_max_size as usize));
    }
    Ok(cfg.create_pool(Some(Runtime::Tokio1), NoTls)?)
}
