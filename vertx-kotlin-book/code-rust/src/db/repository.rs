//! Repository: every method returns a domain type and never leaks driver
//! types to the caller.

use async_stream::try_stream;
use chrono::{DateTime, Utc};
use deadpool_postgres::Pool;
use futures::{stream::poll_fn, Stream, StreamExt};
use tokio::sync::mpsc;
use tokio_postgres::{error::SqlState, AsyncMessage, NoTls, Row};

use crate::domain::{NewUser, User, UserError};

const SQL_FIND_BY_ID: &str = "SELECT id, email, full_name, created_at FROM users WHERE id = $1";
const SQL_FIND_BY_EMAIL: &str =
    "SELECT id, email, full_name, created_at FROM users WHERE email = $1";
const SQL_INSERT: &str =
    "INSERT INTO users (email, full_name) VALUES ($1, $2) RETURNING id, email, full_name, created_at";
const SQL_STREAM_ALL: &str = "SELECT id, email, full_name, created_at FROM users ORDER BY id";
const SQL_STREAM_PREFIX: &str =
    "SELECT id, email, full_name, created_at FROM users WHERE email LIKE $1 ORDER BY id";

/// Default rows fetched per portal round-trip while streaming.
const FETCH_SIZE: i32 = 100;

/// Stateless data access apart from the pool reference. `conn_str` is only used
/// by the LISTEN/NOTIFY hook, which owns a dedicated (non-pooled) connection.
#[derive(Clone)]
pub struct Repository {
    pool: Pool,
    conn_str: String,
}

impl Repository {
    pub fn new(pool: Pool, conn_str: String) -> Self {
        Self { pool, conn_str }
    }

    // ---- single-row reads ------------------------------------------------

    pub async fn find_by_id(&self, id: i64) -> Result<Option<User>, UserError> {
        let client = self.pool.get().await?;
        let row = client.query_opt(SQL_FIND_BY_ID, &[&id]).await?;
        Ok(row.as_ref().map(row_to_user))
    }

    pub async fn find_by_email(&self, email: &str) -> Result<Option<User>, UserError> {
        let client = self.pool.get().await?;
        let row = client.query_opt(SQL_FIND_BY_EMAIL, &[&email]).await?;
        Ok(row.as_ref().map(row_to_user))
    }

    // ---- write -----------------------------------------------------------

    pub async fn create(&self, input: NewUser) -> Result<User, UserError> {
        let client = self.pool.get().await?;
        match client
            .query_one(SQL_INSERT, &[&input.email, &input.full_name])
            .await
        {
            Ok(row) => Ok(row_to_user(&row)),
            Err(e) if e.code() == Some(&SqlState::UNIQUE_VIOLATION) => {
                Err(UserError::DuplicateEmail(input.email))
            }
            Err(e) => Err(e.into()),
        }
    }

    // ---- batch -----------------------------------------------------------

    /// Inserts many users in one pipelined round-trip. tokio-postgres pipelines
    /// concurrent queries over a single connection, the analogue of pgx's
    /// SendBatch and vertx-pg-client's executeBatch.
    pub async fn create_many(&self, inputs: &[NewUser]) -> Result<Vec<User>, UserError> {
        if inputs.is_empty() {
            return Ok(Vec::new());
        }
        let client = self.pool.get().await?;
        let stmt = client.prepare(SQL_INSERT).await?;
        let rows = futures::future::try_join_all(inputs.iter().map(|u| {
            let (client, stmt) = (&client, &stmt);
            async move { client.query_one(stmt, &[&u.email, &u.full_name]).await }
        }))
        .await?;
        Ok(rows.iter().map(row_to_user).collect())
    }

    // ---- streaming reads -------------------------------------------------

    /// Streams every user, holding a transaction open for the whole stream and
    /// reading a server-side portal in `FETCH_SIZE` batches. Because the
    /// consumer (gRPC Send / HTTP flush) pulls each item, a slow client
    /// naturally back-pressures the portal: the next FETCH only runs once the
    /// previous batch has been handed off. If the stream is dropped early, the
    /// transaction's Drop rolls back and the connection returns clean.
    pub fn stream_all(
        &self,
        email_prefix: Option<String>,
    ) -> impl Stream<Item = Result<User, UserError>> {
        let pool = self.pool.clone();
        try_stream! {
            let mut conn = pool.get().await?;
            let tx = conn.transaction().await?;

            let portal = match email_prefix {
                Some(p) if !p.is_empty() => {
                    let pattern = format!("{p}%");
                    tx.bind(SQL_STREAM_PREFIX, &[&pattern]).await?
                }
                _ => tx.bind(SQL_STREAM_ALL, &[]).await?,
            };

            loop {
                let rows = tx.query_portal(&portal, FETCH_SIZE).await?;
                let n = rows.len();
                for row in &rows {
                    yield row_to_user(row);
                }
                if n < FETCH_SIZE as usize {
                    break; // portal exhausted
                }
            }
            tx.commit().await?;
        }
    }

    // ---- LISTEN/NOTIFY hook ----------------------------------------------

    /// Subscribes to Postgres NOTIFY events on `users_created` over a dedicated
    /// connection (LISTEN cannot share a pooled connection). Each id sent on the
    /// channel was just inserted by any client of the database. The driving task
    /// and connection are torn down when the receiver is dropped.
    pub async fn listen_for_new_users(&self) -> Result<mpsc::Receiver<i64>, UserError> {
        let (client, mut connection) = tokio_postgres::connect(&self.conn_str, NoTls).await?;
        client.batch_execute("LISTEN users_created").await?;

        let (tx, rx) = mpsc::channel(64);
        tokio::spawn(async move {
            // Hold `client` for the task's life so the connection stays open.
            let _client = client;
            let mut messages = poll_fn(move |cx| connection.poll_message(cx));
            while let Some(msg) = messages.next().await {
                match msg {
                    Ok(AsyncMessage::Notification(n)) => {
                        if let Ok(id) = n.payload().parse::<i64>() {
                            if tx.send(id).await.is_err() {
                                break; // receiver dropped
                            }
                        }
                    }
                    Ok(_) => {}
                    Err(_) => break, // connection lost
                }
            }
        });
        Ok(rx)
    }
}

fn row_to_user(row: &Row) -> User {
    User {
        id: row.get(0),
        email: row.get(1),
        full_name: row.get(2),
        created_at: row.get::<_, DateTime<Utc>>(3),
    }
}
