# Chapter 9 — PostgreSQL with vertx-pg-client: pool, queries, mapping

> By the end of this chapter you will have an event-loop-aware Pool, you
> will understand prepared-statement caching and pipelining, you will know
> the difference between `query` and `preparedQuery`, and you will be
> writing repository methods as plain suspending functions.

`vertx-pg-client` is a **non-blocking, Netty-based** PostgreSQL driver.
It implements the PG wire protocol directly. There is no JDBC, no
intermediate thread pool, no `Statement.executeQuery` blocking-call.

```
┌────────────────────────────────────────────────────────────┐
│  Coroutine code                                            │
│      pool.preparedQuery(SQL).execute(Tuple).coAwait()      │
│                                                            │
│  vertx-pg-client                                           │
│      Parse / Bind / Execute frames over a connection       │
│                                                            │
│  Netty                                                     │
│      ByteBuf + Channel pipeline → epoll/kqueue/io_uring    │
│                                                            │
│  PostgreSQL                                                │
└────────────────────────────────────────────────────────────┘
```

Same path as your HTTP request: bytes in, bytes out, no thread parks.

## 9.1 Building the Pool

```kotlin
// code/full-app/src/main/kotlin/com/example/app/db/DbModule.kt
val connect = PgConnectOptions()
    .setHost(db.host).setPort(db.port).setDatabase(db.database)
    .setUser(db.user).setPassword(db.password)
    .setPipeliningLimit(db.pipeliningLimit)
    .setCachePreparedStatements(true)
    .setPreparedStatementCacheMaxSize(256)
    .setReconnectAttempts(10).setReconnectInterval(500)

val poolOpts = PoolOptions()
    .setMaxSize(db.poolMaxSize)
    .setShared(true)
    .setName("app-pg-pool")

val pool: Pool = PgBuilder.pool()
    .with(poolOpts).connectingTo(connect).using(vertx).build()
```

Three knobs worth understanding:

- **`setMaxSize(N)`** — total physical connections from this process.
  For non-blocking + pipelining drivers, the right value is
  *much smaller* than you'd pick for JDBC. Rule of thumb:
  `2-4 × event-loop count`. We use 16 for ~8 cores.
- **`setPipeliningLimit(N)`** — how many parse/bind/execute requests can be
  in flight on the **same connection**. PG processes them in order and
  responds in order; the driver multiplexes.
- **`setCachePreparedStatements(true)`** — per-connection LRU cache of
  prepared statements keyed by SQL. Hot queries become O(bind+execute).

`PoolOptions.setShared(true)` lets multiple verticles in the same
process share the same pool. Otherwise you'd open one pool per verticle.

In the real code this lives in `DbModule`, and the `PgConnectOptions`
are built by a separate `connectOptions(db)` function rather than inline.
That's deliberate: in Vert.x 5 `io.vertx.pgclient.PgPool` is gone and a
`Pool` won't hand its connect options back, so anything that needs a
dedicated (non-pooled) connection — notably the `PgSubscriber` for
LISTEN/NOTIFY in Chapter 10 — reuses `connectOptions(db)` directly.

## 9.2 Event-loop affinity of the Pool

The Pool is event-loop-aware. When you call `pool.preparedQuery(…).execute(…)`
from event loop `L_3`, the Pool gives you a connection bound to `L_3`,
sends the request, and the response handler runs on `L_3`. No
cross-thread handoff. **This is the key property** that makes
`pool` + coroutines correct: the resume of your `coAwait` happens on
the loop you started on.

If your traffic spans multiple loops, the pool eventually opens one
"sub-pool" per loop up to `maxSize`. The driver minimises cross-loop
operations. You rarely have to think about this.

## 9.3 query vs preparedQuery

```kotlin
pool.query("SELECT 1").execute()         // ad-hoc, no params, no cache
pool.preparedQuery(SQL).execute(Tuple)   // parameterised, cached
```

Use `preparedQuery` for anything with parameters. Always. It protects
against SQL injection (PG parses the SQL once, binds parameters
separately) and gives you the cache speed-up.

`Tuple.of(a, b, c)` is the argument list. `$1`, `$2`, `$3` are the
placeholders (Postgres style — *not* `?`).

## 9.4 Row mapping by hand

Vert.x rows are columnar. You read by name or index:

```kotlin
private fun rowToUser(row: Row): User =
    User(
        id        = row.getLong("id"),
        email     = row.getString("email"),
        fullName  = row.getString("full_name"),
        createdAt = row.getOffsetDateTime("created_at") ?: OffsetDateTime.now(),
    )
```

That is the entire ORM layer. We deliberately write it out: ten lines,
zero magic, easy to debug, easy to performance-tune.

If you have 50 columns and want generated mappers, look at
`vertx-sql-client-templates`. We don't use it here.

## 9.5 INSERT … RETURNING is your friend

`UserRepository.create` uses one round-trip:

```kotlin
const val SQL_INSERT =
    "INSERT INTO users (email, full_name) VALUES ($1, $2) " +
    "RETURNING id, email, full_name, created_at"
```

`RETURNING` is PG-specific and gives you the inserted row in one shot.
No second `SELECT`. No `lastInsertId()`. Faster + no race.

## 9.6 Reading single rows safely

```kotlin
suspend fun findById(id: Long): User? {
    val rows = pool.preparedQuery(SQL_FIND_BY_ID)
        .execute(Tuple.of(id))
        .coAwait()
    return rows.firstOrNull()?.let(::rowToUser)
}
```

The `RowSet<Row>` is iterable. We treat 0 rows as `null`. The service
layer turns `null` into `UserError.NotFound`.

## 9.7 Catching `PgException`

Some constraint violations bubble up as `PgException`. We translate the
PG SQLSTATE code into our domain error:

```kotlin
try {
    pool.preparedQuery(SQL_INSERT).execute(Tuple.of(...)).coAwait()
} catch (e: PgException) {
    if (e.sqlState == "23505") throw UserError.DuplicateEmail(input.email)
    throw e
}
```

Code `23505` is `unique_violation`. There is a [full
list](https://www.postgresql.org/docs/current/errcodes-appendix.html)
in the PG docs.

## 9.8 Migrations

`DbMigrator.migrate(pool)` runs `V1__schema.sql`. It is intentionally
naïve — for production use Flyway, Liquibase, or a CI pipeline that
runs SQL out-of-process. Schema management is too important to be a
side effect of "did anyone restart the service".

The migration runs **once on startup** inside the same `AppVerticle`
that the HTTP server later starts. So your service is "ready" only
after `migrate` returns.

## 9.9 Configuration: pool sizing

| Symptom                                        | Likely cause                       |
|------------------------------------------------|------------------------------------|
| latency rises with concurrency                 | pool too small                     |
| memory rises, GC churns                        | pool too big (per-conn buffers)    |
| CPU on PG is at 100 %, no queries in pool      | pool right; SQL is the bottleneck  |
| connections opening/closing constantly         | `reconnectAttempts` 0, transient   |
| `connection idle in transaction`               | leaked `withTransaction` block     |

For non-blocking + pipelining, start with `maxSize = 2 × cores`. Add
metrics from `BackendRegistries`/Micrometer: `pool.queue.size`,
`pool.queue.time`, `pool.acquire.time`. Tune from data.

## 9.10 Exercises

1. Add `findByName(prefix: String): List<User>` with a `LIKE` query.
   Add a Prometheus timer around it.
2. Drop the prepared-statement cache (set max size = 0). Re-run a load
   test against `GET /api/users/:id`. Compare p99 latency.
3. Add a column `last_login_at TIMESTAMPTZ` via a new
   `V2__last_login.sql` migration. Update `findById` and `rowToUser`.

---

[← Chapter 8](08-rest-api.md) · [Next → Chapter 10: Advanced PostgreSQL](10-postgresql-advanced.md)
