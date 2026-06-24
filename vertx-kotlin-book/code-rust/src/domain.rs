//! Core entities and the single error type that flows through every layer.
//! Mirrors the Kotlin `domain` package and the Go `internal/domain`.

use chrono::{DateTime, Utc};

/// The domain entity. Repository row mappers translate to/from this.
#[derive(Debug, Clone)]
pub struct User {
    pub id: i64,
    pub email: String,
    pub full_name: String,
    pub created_at: DateTime<Utc>,
}

/// Validated input used by the HTTP and gRPC create paths.
#[derive(Debug, Clone)]
pub struct NewUser {
    pub email: String,
    pub full_name: String,
}

impl NewUser {
    /// Validates and constructs a `NewUser`, enforcing the same rules as the
    /// Kotlin `NewUser` init block and the Go `MakeNewUser`.
    pub fn validate(email: String, full_name: String) -> Result<Self, UserError> {
        if !email.contains('@') {
            return Err(UserError::Validation("email must contain '@'".into()));
        }
        if full_name.trim().is_empty() {
            return Err(UserError::Validation("fullName must not be blank".into()));
        }
        if email.len() > 320 {
            return Err(UserError::Validation("email too long".into()));
        }
        if full_name.len() > 200 {
            return Err(UserError::Validation("fullName too long".into()));
        }
        Ok(Self { email, full_name })
    }
}

/// One error type spanning validation, not-found, conflict, and the underlying
/// database/pool failures. The transport layers map this to gRPC status codes
/// (`From<UserError> for tonic::Status`) and HTTP problem responses.
#[derive(Debug, thiserror::Error)]
pub enum UserError {
    #[error("user {0} not found")]
    NotFound(i64),

    #[error("email already exists: {0}")]
    DuplicateEmail(String),

    #[error("{0}")]
    Validation(String),

    #[error(transparent)]
    Db(#[from] tokio_postgres::Error),

    #[error(transparent)]
    Pool(#[from] deadpool_postgres::PoolError),
}

impl From<UserError> for tonic::Status {
    fn from(e: UserError) -> Self {
        use tonic::Status;
        let msg = e.to_string();
        match e {
            UserError::NotFound(_) => Status::not_found(msg),
            UserError::DuplicateEmail(_) => Status::already_exists(msg),
            UserError::Validation(_) => Status::invalid_argument(msg),
            UserError::Db(_) | UserError::Pool(_) => Status::internal(msg),
        }
    }
}
