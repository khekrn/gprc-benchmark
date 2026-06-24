//! Thin application-service layer: orchestration that doesn't belong in the
//! repository (validation flow, uniqueness pre-check) lives here, keeping it
//! independent of any transport. Mirrors the Go `internal/service`.

use futures::Stream;

use crate::db::Repository;
use crate::domain::{NewUser, User, UserError};

#[derive(Clone)]
pub struct UserService {
    repo: Repository,
}

impl UserService {
    pub fn new(repo: Repository) -> Self {
        Self { repo }
    }

    pub async fn get_by_id(&self, id: i64) -> Result<User, UserError> {
        self.repo
            .find_by_id(id)
            .await?
            .ok_or(UserError::NotFound(id))
    }

    pub async fn create(&self, input: NewUser) -> Result<User, UserError> {
        // Friendly uniqueness pre-check so we can return a clean conflict
        // without relying on the DB error; still catch DuplicateEmail from the
        // repo because races exist.
        if self.repo.find_by_email(&input.email).await?.is_some() {
            return Err(UserError::DuplicateEmail(input.email));
        }
        self.repo.create(input).await
    }

    pub async fn bulk_create(&self, inputs: &[NewUser]) -> Result<Vec<User>, UserError> {
        self.repo.create_many(inputs).await
    }

    /// Streams every user; a slow consumer back-pressures all the way to the
    /// Postgres portal (see `Repository::stream_all`).
    pub fn stream_all(
        &self,
        email_prefix: Option<String>,
    ) -> impl Stream<Item = Result<User, UserError>> {
        self.repo.stream_all(email_prefix)
    }
}
