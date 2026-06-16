# Chapter 11 — Repository patterns, migrations, domain modelling

> You will have a working mental model for the layers (handler →
> service → repository), know why we expose `Flow<T>` instead of
> `List<T>` for unbounded reads, and have a recipe for evolving the
> schema without downtime.

## 11.1 The three-layer split

```
   handler        — protocol concerns (HTTP / gRPC frames, validation)
       │
       ▼
   service        — domain rules, orchestration
       │
       ▼
   repository     — persistence: SQL, mapping, retries
       │
       ▼
   driver (vertx-pg-client) — network
```

Why bother? Two reasons:

1. **Replaceability.** If you swap PG for a different store (we
   wouldn't — it's the best choice), only the repository changes.
2. **Testability.** Service tests do not need a database. We use a
   small in-memory fake of `UserRepository` to test `UserService`.

We do *not* introduce an "interface" for `UserRepository` until we have
a second implementation. YAGNI. Kotlin lets us add the interface later
without changing call sites.

## 11.2 What the repository should and should not do

| Repository                                       | Service                              |
|--------------------------------------------------|--------------------------------------|
| SQL strings, rows → domain objects, retry policy | Business rules ("can A become B?")   |
| Translate DB errors to domain errors             | Composition of multiple repositories |
| Knows about Pool, Tuple, Row                     | Knows only about domain types        |
| `Flow<User>` for unbounded reads                 | `Flow<User>` or `List<User>` based on use |

Common smell: a service that calls `pool.preparedQuery(...)` directly.
Move it down.

## 11.3 Returning `Flow<T>` vs `List<T>`

For bounded results (`findById`, "give me page 1 of 10"), return the
type the caller expects: `User?`, `List<User>`.

For *unbounded* results, return `Flow<T>`. The caller decides how much
to consume. Memory stays bounded.

```kotlin
// bounded: caller wants a list
suspend fun page(offset: Int, limit: Int): List<User> { … }

// unbounded: caller streams
fun streamAll(prefix: String?): Flow<User> { … }
```

Mixing these is a common bug. A `findAll(): List<User>` works at 100
rows; explodes at 10 M.

## 11.4 Generated keys, immutable IDs

We use `BIGSERIAL` and `INSERT … RETURNING`. The created `User` is
immutable: id, created_at are server-generated and the client only
sees them after creation. Don't let callers pass an `id` on creation.

## 11.5 Idempotent migrations

`V1__schema.sql` uses `CREATE TABLE IF NOT EXISTS` and
`CREATE INDEX IF NOT EXISTS`. The trigger uses `CREATE OR REPLACE`.
Why? So running the migration twice (a restart-loop, a re-deploy) is a
no-op instead of a crash.

For real migrations (column drops, type changes), use
Flyway/Liquibase. Don't write your own state tracker.

## 11.6 Online schema change without downtime

Adding a column: `ALTER TABLE users ADD COLUMN last_login TIMESTAMPTZ`.
With Postgres ≥ 11, this is metadata-only (no rewrite) **as long as
the column is nullable and has no DEFAULT** (or the default is constant).

Dropping a column: drop usage from code first, ship; *then* drop the
column. Reverse order is harder to undo.

Renaming a column: don't. Add new, dual-write, backfill, switch reads,
drop old. Same for table renames.

Indexes: build with `CONCURRENTLY`. It takes longer but doesn't block
writes:

```sql
CREATE INDEX CONCURRENTLY IF NOT EXISTS users_created_at_idx ON users (created_at);
```

## 11.7 The domain model is just data classes

```kotlin
data class User(
    val id: Long,
    val email: String,
    val fullName: String,
    val createdAt: OffsetDateTime,
)
```

No annotations, no DSL. The repository translates rows to this; the
service uses it; the handler encodes it to JSON / protobuf.

Validation lives in `NewUser.init`. Domain errors are sealed:

```kotlin
sealed class UserError(msg: String) : RuntimeException(msg) {
    class NotFound(id: Long) : UserError("user $id not found")
    class DuplicateEmail(email: String) : UserError("email already exists: $email")
    class Validation(msg: String) : UserError(msg)
}
```

A handler `catch`-es the specific subtype and chooses an HTTP / gRPC
status. The compiler tells you if you missed one.

## 11.8 Retry policy: keep it explicit

If the DB is briefly unavailable, you might want a one-time retry.
Don't bury this in the repository. Make it explicit:

```kotlin
suspend fun <T> withRetry(times: Int = 1, block: suspend () -> T): T {
    var attempt = 0
    while (true) {
        try { return block() } catch (e: PgException) {
            if (++attempt > times || !e.isTransient()) throw e
            delay(50 * attempt.toLong())
        }
    }
}
```

Wrap *non-idempotent* calls only after careful thought. An idempotent
read is fine to retry. An insert that already happened the first time
will create duplicate rows on retry — unless you use upserts with
`ON CONFLICT DO NOTHING` or pass a client-side correlation id.

## 11.9 Exercises

1. Add `UserRepository.update(id, email)` and consider what happens if
   the email collides. Translate `PgException` → `UserError`.
2. Add a fake `UserRepository` for tests (`InMemoryUserRepository`),
   make `UserRepository` an interface, and write a `UserServiceTest`
   that runs without Testcontainers.
3. Migrate to add a `username` column. Plan it as a four-step rollout
   (dual-write, backfill, switch reads, drop legacy). Write the SQL.

---

[← Chapter 10](10-postgresql-advanced.md) · [Next → Chapter 12: gRPC fundamentals](12-grpc-unary.md)
