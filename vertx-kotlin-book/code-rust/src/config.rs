//! Strongly-typed configuration loaded from a YAML file with environment
//! overrides, mirroring the layered Kotlin AppConfig / Go config package.
//!
//! Layers (later overrides earlier):
//!  1. config.yaml (path from CONFIG_FILE, default ./config.yaml)
//!  2. environment variables (HTTP_PORT, GRPC_PORT, DB_HOST, ...)

use std::env;

use serde::Deserialize;

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Config {
    #[serde(default)]
    pub http: Http,
    #[serde(default)]
    pub grpc: Grpc,
    #[serde(default)]
    pub db: Db,
    #[serde(default = "default_grace")]
    pub shutdown_grace_period_ms: u64,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Http {
    pub host: String,
    pub port: u16,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Grpc {
    pub port: u16,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct Db {
    pub host: String,
    pub port: u16,
    pub database: String,
    pub user: String,
    pub password: String,
    pub pool_max_size: u32,
    pub schema_on_startup: bool,
}

fn default_grace() -> u64 {
    15_000
}

// Defaults match the Kotlin application.yaml / Go defaults().
impl Default for Http {
    fn default() -> Self {
        Self { host: "0.0.0.0".into(), port: 8080 }
    }
}
impl Default for Grpc {
    fn default() -> Self {
        Self { port: 9090 }
    }
}
impl Default for Db {
    fn default() -> Self {
        Self {
            host: "localhost".into(),
            port: 5432,
            database: "appdb".into(),
            user: "app".into(),
            password: "app".into(),
            pool_max_size: 16,
            schema_on_startup: true,
        }
    }
}
impl Default for Config {
    fn default() -> Self {
        Self {
            http: Http::default(),
            grpc: Grpc::default(),
            db: Db::default(),
            shutdown_grace_period_ms: default_grace(),
        }
    }
}

impl Config {
    /// Reads the YAML file (if present) on top of the defaults, then applies
    /// environment-variable overrides.
    pub fn load() -> anyhow::Result<Self> {
        let path = env::var("CONFIG_FILE").unwrap_or_else(|_| "config.yaml".into());
        let mut cfg: Config = match std::fs::read_to_string(&path) {
            Ok(raw) => serde_yaml::from_str(&raw)?,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => Config::default(),
            Err(e) => return Err(e.into()),
        };

        // Env overrides.
        env_str("HTTP_HOST", &mut cfg.http.host);
        env_num("HTTP_PORT", &mut cfg.http.port);
        env_num("GRPC_PORT", &mut cfg.grpc.port);
        env_str("DB_HOST", &mut cfg.db.host);
        env_num("DB_PORT", &mut cfg.db.port);
        env_str("DB_DATABASE", &mut cfg.db.database);
        env_str("DB_USER", &mut cfg.db.user);
        env_str("DB_PASSWORD", &mut cfg.db.password);
        env_num("DB_POOL_MAX_SIZE", &mut cfg.db.pool_max_size);

        Ok(cfg)
    }
}

fn env_str(key: &str, dst: &mut String) {
    if let Ok(v) = env::var(key) {
        if !v.is_empty() {
            *dst = v;
        }
    }
}

fn env_num<T: std::str::FromStr>(key: &str, dst: &mut T) {
    if let Ok(v) = env::var(key) {
        if let Ok(parsed) = v.parse::<T>() {
            *dst = parsed;
        }
    }
}
