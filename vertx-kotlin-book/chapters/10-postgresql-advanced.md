# Chapter 10 — PostgreSQL advanced: transactions, streaming, LISTEN/NOTIFY, pipelining

> By the end of this chapter you will use transactions correctly under
> coroutines, stream millions of rows with server-side cursors and
> end-to-end backpressure, react to `LISTEN/NOTIFY`, and have a feel for
> when pipelining gives you a real win.

## 10.1 Transactions with `withTransaction`

`Pool.withTransaction { conn -> … }` checks out a connection, opens a
transaction, runs your block, and commits on success / rolls back on
failure. The block returns a `Future<T>`; the result of `withTransaction`
is `Future<T>`.

```kotlin
pool.withTransaction { conn ->
    conn.preparedQuery(SQL_INSERT).execute(...)
        .compose { conn.preparedQuery(SQL_OTHER).execute(...) }
}.coAwait()
```

Coroutine version using `vertxFuture` for the block:

```kotlin
suspend fun createWithAudit(input: NewUser): User =
    pool.withTransaction { conn ->
        vertxFuture {
            val u = insertUser(conn, input)
            insertAudit(conn, u.id, "created")
            u
        }
    }.coAwait()
```

Two pitfalls:

1. **Don't escape the connection.** If you store `conn` in a property
   and use it later, you'll be using a connection that has been
   returned to the pool — undefined behaviour.
2. **Don't use the pool inside the block.** Use the provided `conn`.
   Otherwise you'll use a different connection that can't see your
   uncommitted writes.

## 10.2 Row streaming with server-side cursors

Reading 5 M rows with `SELECT *` and materialising into a List is a
memory bomb. Use a cursor:

```kotlin
pool.withTransaction { conn ->
    val stream = conn.preparedQuery(SQL).createStream(fetchSize, params)
    stream.handler { row -> channel.trySend(rowToUser(row)) }
    stream.exceptionHandler { t -> channel.close(t) }
    stream.endHandler { channel.close() }
    Future.succeededFuture<Void>()
}
```

`createStream(N, params)` opens a PG cursor and fetches `N` rows per
round-trip. `stream.handler { }` is called once per row, on the event
loop. We feed each row into a coroutine `Channel`; the consumer pulls
through a `Flow`.

When the consumer falls behind:

```kotlin
val ok = channel.trySend(rowToUser(row)).isSuccess
if (!ok) stream.pause()
```

We `pause()` the stream. Vert.x stops asking the driver for the next
batch. The driver stops reading from the socket. PG stops pushing.

When the consumer catches up: the channel drains, the next `trySend`
succeeds, and we **don't** explicitly resume. Why? Because Vert.x's
`RowStream` is a *pull-based* `ReadStream`; resuming has to be wired
via `fetch(N)`. In our repo, we use a `Channel(fetchSize)` so the
stream keeps producing until the channel is full again; then it pauses.
The simpler model is: bounded channel = bounded buffer, and the
"resume" is implicit because the upstream emits one batch at a time.

For very large streams, the explicit `resume()` after consumption is
worth wiring:

```kotlin
channel.invokeOnClose { stream.close() }
stream.endHandler { channel.close() }
```

We make sure the stream is closed if the consumer goes away.

## 10.3 The complete `streamAll`

```kotlin
fun streamAll(emailPrefix: String?, fetchSize: Int = 100): Flow<User> {
    val channel = Channel<User>(capacity = fetchSize)
    pool.withTransaction { conn ->
        val sql = if (emailPrefix.isNullOrBlank()) SQL_STREAM_ALL else SQL_STREAM_PREFIX
        val params = if (emailPrefix.isNullOrBlank()) Tuple.tuple() else Tuple.of("$emailPrefix%")
        val stream: RowStream<Row> = conn.preparedQuery(sql).createStream(fetchSize, params)
        stream.handler { row ->
            val ok = channel.trySend(rowToUser(row)).isSuccess
            if (!ok) stream.pause()
        }
        stream.exceptionHandler { t -> channel.close(t) }
        stream.endHandler { channel.close() }
        channel.invokeOnClose { stream.close() }
        Future.succeededFuture<Void>()
    }.onFailure { t -> channel.close(t) }
    return channel.consumeAsFlow()
}
```

You use it as a coroutine `Flow`:

```kotlin
users.streamAll(null).collect { u -> resp.write(toJson(u) + "\n") }
```

Memory: O(fetchSize). Throughput: limited by the slowest hop. This is
the foundation for the gRPC server streaming endpoint in Chapter 13.

## 10.4 LISTEN / NOTIFY

PG supports `NOTIFY channel, payload` from any backend. A subscriber
receives push notifications without polling. We use it in the demo to
react to new user inserts (the schema has a trigger that calls
`pg_notify('users_created', id)`).

```kotlin
suspend fun listenForNewUsers(vertx: Vertx): Channel<Long> {
    val channel = Channel<Long>(Channel.BUFFERED)
    val subscriber = PgSubscriber.subscriber(vertx, (pool as PgPool).connectOptions)
    subscriber.connect().coAwait()
    subscriber.channel("users_created").handler { payload ->
        val id = payload.toLongOrNull() ?: return@handler
        try { channel.trySend(id) } catch (_: ClosedSendChannelException) { }
    }
    channel.invokeOnClose { subscriber.close() }
    return channel
}
```

`PgSubscriber` holds **a dedicated connection** (not from the pool). PG
notifications are tied to a connection; you can't share with query
traffic.

Use cases:

- Real-time UI invalidation (write to PG, push update to WebSocket
  subscribers).
- Cache invalidation across instances.
- Cheap "service worker waking" without Redis pub/sub.

Watch out: payloads are limited to ~8 KB. Don't try to ship rows;
ship IDs, then SELECT.

## 10.5 Pipelining: when it actually helps

PG processes pipelined frames in order. If you bulk-insert 1000 rows
one-by-one, pipelining batches the parse/bind/execute frames so the
driver doesn't have to wait for round-trip ACKs.

Single-tuple approach (slow without pipelining):

```kotlin
for (u in inputs) pool.preparedQuery(INSERT).execute(Tuple.of(u.email, u.fullName)).coAwait()
```

Batch approach (fast):

```kotlin
val tuples = inputs.map { Tuple.of(it.email, it.fullName) }
pool.preparedQuery(INSERT).executeBatch(tuples).coAwait()
```

`executeBatch` is what `UserRepository.createMany` uses. It is roughly
10–30 × faster than a serial loop for 1000 inserts on the same connection.

Pipelining also matters for **reads**. If you fan-out multiple
`preparedQuery.execute` on the same Pool, Vert.x can pipeline them on
one connection up to `pipeliningLimit`.

## 10.6 Read-after-write inside a transaction

```kotlin
pool.withTransaction { conn ->
    vertxFuture {
        val u = conn.preparedQuery(INSERT).execute(...).coAwait().first().let(::rowToUser)
        // Read it back via the same conn — sees uncommitted state
        val v = conn.preparedQuery(SELECT).execute(Tuple.of(u.id)).coAwait().first()
        v.let(::rowToUser)
    }
}.coAwait()
```

Reads on the same connection are inside the same transaction. Reads via
`pool` (different connection) cannot see the uncommitted writes.

## 10.7 Sliding window aggregate (worked example)

A common pattern: stream rows, group into batches of N, send each batch
downstream. We use `chunked` from `kotlinx.coroutines.flow`:

```kotlin
users.streamAll(null)
     .chunked(100)
     .collect { batch -> sink.send(batch) }
```

Memory stays bounded by `100 + fetchSize`.

> `chunked` is in the `kotlinx-coroutines-core` ext API in 1.10. If you
> are on an older version, write it yourself with `buffer` + a manual
> list.

## 10.8 Exercises

1. Add a `findActiveOrders(userId, since)` repository method that uses
   `streamAll` to stream rows; consume into a `Flow<Order>`.
2. Add a "cache invalidation" feature: subscribe to a PG NOTIFY and
   refresh an in-memory cache.
3. Compare pipelined vs serial inserts of 100 k rows. Plot p50 / p99.
   Where does pipelining stop scaling?

---

[← Chapter 9](09-postgresql-basics.md) · [Next → Chapter 11: Repository patterns](11-repository-patterns.md)
