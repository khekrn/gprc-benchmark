//! Applies the idempotent schema on startup. For a real system prefer a
//! dedicated migration tool (refinery, sqlx-migrate); this mirrors the Kotlin
//! DbMigrator / Go migrate package, which run the same SQL.

use deadpool_postgres::Pool;

const SCHEMA: &str = include_str!("../migrations/schema.sql");

pub async fn run(pool: &Pool) -> anyhow::Result<()> {
    let client = pool.get().await?;
    client.batch_execute(SCHEMA).await?;
    Ok(())
}
