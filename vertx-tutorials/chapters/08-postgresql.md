# Chapter 8 — PostgreSQL with `vertx-pg-client`

> **You should finish this chapter able to:** size a `PgPool` correctly, use
> prepared statements with parameter binding, manage transactions, leverage
> protocol-level pipelining for massive throughput gains, stream large result
> sets, map rows to data classes, and avoid the JDBC traps that don't apply
> to a non-blocking driver.

`vertx-pg-client` is a **native non-blocking** PostgreSQL driver. It is not a
JDBC wrapper. It speaks the wire protocol directly (`COPY`, prepared
statements, pipelining, the works), uses Netty for I/O, and exposes Vert.x
`Future<T>` everywhere. You can drive thousands of concurrent queries
through a handful of TCP connections on a single event-loop thread.

This chapter assumes you've read chapter 1 (event loop) and chapter 3
(coroutines).

## 8.1  Why a reactive driver?

JDBC drivers are *synchronous*. Every call blocks the calling thread until
the database responds. Combined with a typical connection pool (HikariCP),
each in-flight query holds one Java thread for the duration of the DB
round-trip. A 5 ms query on 1000 RPS = ~5 threads busy. A 50 ms query at
the same RPS = ~50 threads. With JDBC you cannot escape this; "async JDBC"
libraries built on top of HikariCP are just thread pools wearing a callback.

`vertx-pg-client` is different:

```
JDBC               vertx-pg-client
====               ===============
[Thread]           [Event loop thread]
  │                  │
  │ exec(query)      │ pool.preparedQuery(sql).execute(tuple)
  │ ...blocks...     │   ↓ (returns Future immediately)
  │ ...blocks...     │ event loop serves OTHER requests
  │ ...blocks...     │
  │ result           │ ← when DB responds, Future completes,
  │                  │   coroutine resumes
```

One event-loop thread can handle the in-flight state of *thousands* of
concurrent queries. The connection pool exists to multiplex queries onto a
small number of TCP sockets, *not* to assign threads.

## 8.2  Connection pool, properly sized

Our pool is created in
[`UserRepository.kt`](../code/full-app/src/main/kotlin/com/example/app/db/UserRepository.kt#L53):

```kotlin
val connect = PgConnectOptions()
    .setHost(cfg.host)
    .setPort(cfg.port)
    .setDatabase(cfg.database)
    .setUser(cfg.user)
    .setPassword(cfg.password)
    .setReconnectAttempts(2)
    .setReconnectInterval(1_000)
    .setPipeliningLimit(cfg.pipeliningLimit)        // ← KEY OPTION

val poolOptions = PoolOptions()
    .setMaxSize(cfg.maxSize)
    .setShared(true)
    .setName("pg-pool")

val pool = PgBuilder.pool()
    .with(poolOptions)
    .connectingTo(connect)
    .using(vertx)
    .build()
```

### Pool size — counter-intuitive math

With JDBC, the rule of thumb is `cores × 2 + 1` to keep the DB busy without
context-thrashing. **With `vertx-pg-client`, the formula changes.**

Each connection can have **`pipeliningLimit`** queries in-flight
simultaneously (the driver writes the next query before the previous response
arrives, and demuxes responses by order). With pipelining = 256, one
connection can carry 256 concurrent queries. A pool of `maxSize=16` carries
`16 × 256 = 4096` concurrent queries before blocking.

For most workloads:

| Workload | `maxSize` | `pipeliningLimit` |
|---|---|---|
| Read-heavy CRUD | 8–16 | 256 |
| Write-heavy with `BEGIN ... COMMIT` | 32 | 1 (pipelining off — see below) |
| Mixed | 16 | 256 |

The PostgreSQL server has its own `max_connections` (often 100). If you run
*N* application instances each with a pool of 16, you've used *16N*
connections — make sure that fits.

### Why `setShared(true)`?

`setShared(true)` means **all verticle instances in this JVM share one
underlying pool object**. Without it, deploying 8 instances of `AppVerticle`
gives you 8 separate pools, each maxing at 16 — 128 total connections.
Usually undesirable. With `shared = true`, all 8 instances share a single
16-connection pool. The `setName("pg-pool")` is what binds them; same name
on the same `Vertx` instance returns the same pool.

### Pipelining caveats

Pipelining is a win only for **simple, autocommit, idempotent** queries. If
you run a transaction (`BEGIN; UPDATE x; UPDATE y; COMMIT;`) on a pipelined
connection, the `UPDATE`s may interleave with other clients' queries. The
driver actually serializes transactions on a connection automatically, so
this is more about *what you give up* — within a transaction the connection
is exclusive, no pipelining benefits.

For pure-read traffic, pipelining can push **10×** more queries through the
same number of connections.

## 8.3  The SQL and the schema

Our migration in
[`V1__schema.sql`](../code/full-app/src/main/resources/db/migration/V1__schema.sql):

```sql
CREATE TABLE IF NOT EXISTS users (
    id                      TEXT PRIMARY KEY,
    email                   TEXT NOT NULL UNIQUE,
    name                    TEXT NOT NULL,
    created_at_epoch_millis BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS users_email_idx ON users (email);
```

We use `TEXT` for the ID (UUID string) and `BIGINT` for the timestamp
(epoch millis) — two intentional decisions:

- **UUID-as-text** is portable across systems and easy to log. Postgres has
  a native `uuid` type that is more compact (16 bytes vs 36 chars), but the
  Vert.x driver's binary codec is excellent either way.
- **Epoch millis** as `BIGINT` avoids timezone confusion. Convert at the
  application boundary. Postgres has `timestamptz` which is also great if you
  need date arithmetic in SQL.

## 8.4  Prepared statements and parameter binding

```kotlin
private const val SELECT_BY_ID = """
    SELECT id, email, name, created_at_epoch_millis
    FROM users WHERE id = $1
"""

suspend fun findById(id: String): User? {
    val rows = pool.preparedQuery(SELECT_BY_ID).execute(Tuple.of(id)).coAwait()
    return rows.firstOrNull()?.toUser()
}
```

Key points:

1. **`$1, $2` placeholders.** PostgreSQL uses numbered placeholders, not `?`.
   This is the wire protocol; the driver doesn't translate.
2. **`Tuple.of(...)` for parameters.** Always pass values via `Tuple`. *Never*
   build SQL via string concatenation — that's how SQL injection happens.
3. **`preparedQuery(sql)` caches the plan.** The driver maintains a
   per-connection prepared-statement cache. First execution per connection
   does a `PARSE` + `BIND` + `EXECUTE`; subsequent ones are just `BIND` +
   `EXECUTE`. Substantial saving for hot queries.
4. **`.coAwait()` suspends the coroutine** until the response arrives, then
   resumes on the same event-loop Context. The event loop is free to serve
   other requests in the meantime.

## 8.5  Inserts and reading the inserted row

```kotlin
suspend fun insert(email: String, name: String): User {
    val id = UUID.randomUUID().toString()
    val now = System.currentTimeMillis()
    pool.preparedQuery(INSERT)
        .execute(Tuple.of(id, email, name, now))
        .coAwait()
    return User(id, email, name, now)
}
```

That works because we generate the ID client-side. If the DB generates the
ID (`SERIAL` / `BIGSERIAL` / DEFAULT `gen_random_uuid()`), use `RETURNING`:

```sql
INSERT INTO users(email, name, created_at_epoch_millis)
VALUES ($1, $2, $3)
RETURNING id
```

```kotlin
val row = pool.preparedQuery(INSERT).execute(Tuple.of(email, name, now)).coAwait().first()
val id = row.getString("id")
```

The driver supports `RETURNING` natively — the rows you get back are the
inserted (or updated) rows.

## 8.6  Transactions — the right pattern

Multi-statement transactions need to run on **one dedicated connection**.
Don't try to manage that by hand — use `pool.withTransaction { }`:

```kotlin
suspend fun transfer(from: String, to: String, amount: Long) {
    pool.withTransaction { conn ->
        // Returns Future<T>; coroutine bridge below
        Future.future<Void> { p ->
            conn.preparedQuery("UPDATE accounts SET balance = balance - $1 WHERE id = $2")
                .execute(Tuple.of(amount, from))
                .compose {
                    conn.preparedQuery("UPDATE accounts SET balance = balance + $1 WHERE id = $2")
                        .execute(Tuple.of(amount, to))
                }
                .onComplete { ar -> if (ar.succeeded()) p.complete() else p.fail(ar.cause()) }
        }
    }.coAwait()
}
```

Or the coroutine-friendly version (we add this helper once and use it
everywhere):

```kotlin
suspend fun <T> Pool.txCoroutine(block: suspend (SqlConnection) -> T): T =
    withTransaction { conn ->
        io.vertx.kotlin.coroutines.vertxFuture { block(conn) }
    }.coAwait()

// Usage
suspend fun transfer(from: String, to: String, amount: Long) {
    pool.txCoroutine { conn ->
        conn.preparedQuery("UPDATE accounts SET balance = balance - $1 WHERE id = $2")
            .execute(Tuple.of(amount, from)).coAwait()
        conn.preparedQuery("UPDATE accounts SET balance = balance + $1 WHERE id = $2")
            .execute(Tuple.of(amount, to)).coAwait()
    }
}
```

`withTransaction` semantics:

- Acquires a connection from the pool.
- Sends `BEGIN`.
- Runs your block.
- If the block returns a successful Future: sends `COMMIT`.
- If it returns a failed Future or throws: sends `ROLLBACK`.
- Releases the connection back to the pool.

The whole block runs on **one** connection, so pipelining doesn't help here;
the queries serialize. That's the cost of correctness.

### Read-only and isolation

For analytic reads you can request a different isolation level:

```kotlin
pool.withTransaction(TransactionPropagation.NESTED) { conn ->
    conn.query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
        .execute()
        .compose { /* your queries */ }
}
```

`READ ONLY` lets Postgres skip taking row locks on indexes — measurable
benefit for big read-only reports.

## 8.7  Row mapping — typed and explicit

We extract values by column name:

```kotlin
private fun Row.toUser() = User(
    id                      = getString("id"),
    email                   = getString("email"),
    name                    = getString("name"),
    createdAtEpochMillis    = getLong("created_at_epoch_millis"),
)
```

`Row` has methods for every supported Postgres type: `getString`, `getInteger`,
`getLong`, `getDouble`, `getInstant`, `getLocalDate`, `getJsonObject`,
`getUUID`, `getJsonArray`, `getBuffer`, `get<T>(klass, "col")`, etc.

For more complex mappers (a join with nested objects), use a builder:

```kotlin
data class UserWithProfile(val user: User, val profile: Profile?)

private fun Row.toUserWithProfile(): UserWithProfile {
    val u = User(getString("u_id"), getString("u_email"), getString("u_name"), getLong("u_created"))
    val p = if (getString("p_id") != null)
        Profile(getString("p_id"), getString("p_bio"))
    else null
    return UserWithProfile(u, p)
}
```

The driver also has a `RowMapper` type that pre-binds column indices, faster
for very large result sets — but for most queries the named accessor cost
is negligible.

## 8.8  Streaming large result sets

`SELECT * FROM events` on a billion-row table will OOM you. Use a cursor:

```kotlin
suspend fun streamAllEvents(consume: suspend (Row) -> Unit) {
    pool.withConnection { conn ->
        Future.future<Void> { p ->
            conn.prepare("SELECT * FROM events")
                .compose { ps ->
                    val stream = ps.createStream(50)        // 50-row batches
                    stream.handler { row ->
                        // CAREFUL: this runs on the event loop. consume must not block.
                        vertxFuture { consume(row) }
                    }
                    stream.endHandler { p.complete() }
                    stream.exceptionHandler(p::fail)
                    Future.succeededFuture<Void>(null)
                }
                .onFailure(p::fail)
        }
    }.coAwait()
}
```

`createStream(N)` fetches `N` rows at a time via Postgres's `FETCH` cursor.
The driver pulls `N`, fires `handler` for each, then pulls the next `N` when
your handler is ready (back-pressure!).

For most cases, batch queries explicitly with `LIMIT/OFFSET` (or cursors —
see 8.9) instead of streaming.

## 8.9  Keyset (cursor) pagination — the right way

`OFFSET 1000000` makes Postgres scan and discard a million rows. Use a
keyset (cursor) instead:

```kotlin
private const val LIST_PAGE = """
    SELECT id, email, name, created_at_epoch_millis
    FROM users
    WHERE ($1::bigint IS NULL OR created_at_epoch_millis < $1)
    ORDER BY created_at_epoch_millis DESC, id DESC
    LIMIT $2
"""

suspend fun list(cursor: Long?, limit: Int): Page<User> {
    val rows = pool.preparedQuery(LIST_PAGE)
        .execute(Tuple.of(cursor, limit + 1))
        .coAwait()
    val users = rows.take(limit).map(Row::toUser)
    val nextCursor = if (rows.size() > limit) users.last().createdAtEpochMillis else null
    return Page(users, nextCursor)
}
```

We over-fetch by one row to know if there is a next page. The cursor encodes
the *last seen sort value*, not an offset — each page is `O(limit)` from
the index, regardless of where in the dataset you are.

## 8.10  Batch operations

Bulk insert without a transaction wrapping every row:

```kotlin
suspend fun insertMany(users: List<User>) {
    val tuples = users.map { Tuple.of(it.id, it.email, it.name, it.createdAtEpochMillis) }
    pool.preparedQuery(INSERT).executeBatch(tuples).coAwait()
}
```

The driver pipelines the inserts onto a single connection — far faster than
N round-trips. For really large bulk loads (millions of rows), use
`COPY ... FROM STDIN`:

```kotlin
val copy = conn.preparedQuery("COPY users FROM STDIN WITH (FORMAT CSV)")
    .copyIn(pgConn)
copy.write(Buffer.buffer("id1,a@b.c,Alice,1234567890\n"))
copy.write(Buffer.buffer("id2,b@b.c,Bob,1234567891\n"))
copy.end().coAwait()
```

`COPY` is the fastest way to load data into Postgres. It bypasses planning,
triggers, and most of the per-row overhead.

## 8.11  Health checks for the pool

Postgres connections can drift (network blips, server restarts). Add a
lightweight health query:

```kotlin
suspend fun ping(): Boolean = runCatching {
    pool.query("SELECT 1").execute().coAwait()
}.isSuccess
```

Wire into `HealthCheckHandler`:

```kotlin
val health = HealthChecks.create(vertx)
health.register("postgres") { promise ->
    vertxFuture {
        if (userRepo.ping()) promise.complete(Status.OK())
        else promise.complete(Status.KO())
    }
}
router.get("/healthz/ready").handler(HealthCheckHandler.createWithHealthChecks(health))
```

## 8.12  Error handling: catch the right thing

Driver-specific exceptions are in `io.vertx.pgclient.PgException`. The
constraint violation case shows up as `code "23505"` (unique_violation), etc.

```kotlin
suspend fun create(email: String, name: String): User = try {
    repo.insert(email, name)
} catch (e: PgException) {
    when (e.code) {
        "23505" -> throw DuplicateEmailException(email)
        "23503" -> throw ForeignKeyViolation(e.message ?: "")
        else    -> throw e
    }
}
```

Map driver exceptions to *domain* exceptions at the repository boundary;
the HTTP layer maps the domain exceptions to status codes (chapter 7).

## 8.13  Connection lifecycle internals

When you call `pool.preparedQuery(sql).execute(...)`:

1. The pool looks for an idle `SqlConnection`. If one exists, it returns it.
2. If none idle and `currentSize < maxSize`, it creates a new connection
   (TCP connect + Postgres handshake + auth). That takes ~10 ms.
3. If `currentSize == maxSize`, the request goes to a **wait queue**.
4. The connection issues `PARSE` (if not cached), `BIND`, `EXECUTE`,
   `SYNC` over the same TCP socket.
5. The Future completes when `ReadyForQuery` arrives.
6. The connection is returned to the pool.

`PoolOptions().setMaxWaitQueueSize(N)` bounds the queue. Default unlimited
(bad for production). Set it to ~ `1024` and the pool will fail fast under
pressure instead of growing memory forever.

## 8.14  SCRAM authentication

Modern Postgres 14+ requires SCRAM-SHA-256 by default. The driver supports
it transparently — but if your `pg_hba.conf` has `scram-sha-256` and your
user was created in an older era with MD5, auth fails. Fix:

```sql
ALTER USER your_user PASSWORD 'your_password';   -- forces re-hash with scram
```

For TLS:

```kotlin
PgConnectOptions()
    .setSslMode(SslMode.REQUIRE)
    .setTrustOptions(TrustAllOptions())          // or your real CA bundle
```

`REQUIRE` only verifies that TLS is negotiated — it does *not* verify the
server cert. Use `VERIFY_FULL` plus a real `setTrustStoreOptions(...)` for
production.

## 8.15  Common mistakes

**Reusing a `SqlConnection` from `withConnection` across coroutines without
synchronizing.** A SqlConnection is *not* thread-safe. Inside a single
coroutine on a single event loop it's fine. Across event loops, treat it as
single-owner.

**Holding a connection across user input.** Don't `acquire connection →
send response with form → wait for next click → use connection`. The
connection is locked for the duration.

**Catching `Throwable` and not the specific cause.** PgException has rich
metadata; throwing it away to `printStackTrace()` is malpractice. At minimum,
log `e.code`, `e.sqlState`, `e.errorMessage`.

**Forgetting to close the pool on shutdown.** Pool close = graceful drain.
Add it to `AppVerticle.stop()`:

```kotlin
override suspend fun stop() {
    userRepo.close()
    redisCache.close()
}
```

**Using `setMaxLifetime`-like settings**. Vert.x pools don't have a "kill
connection after X" knob like HikariCP. If you need long-running connections
to recycle, restart the pool periodically (or, better, set
`tcp_keepalives_idle` on Postgres and let the OS clean up).

## 8.16  Try it

1. Run `wrk -t 8 -c 256 -d 30s http://localhost:8080/api/v1/users/<id>` with
   `pipeliningLimit=1` vs `pipeliningLimit=256`. Compare RPS.
2. Add a `GET /api/v1/users` route that returns a cursor-paginated list as
   in §8.9. Hit page 1, then page 100, then page 10000. Latency should not
   degrade.
3. Use `pg_stat_statements` to find your hottest queries. Add an `EXPLAIN
   ANALYZE` for each. Are indexes used?

[← Ch 7](07-rest-api.md) · [Next: Chapter 9 — Redis integration →](09-redis.md)
