//! Database layer: the deadpool-postgres pool and the repository over
//! tokio-postgres. The Rust analogue of the Go `internal/db` package.

mod pool;
mod repository;

pub use pool::{conn_string, new_pool};
pub use repository::Repository;
