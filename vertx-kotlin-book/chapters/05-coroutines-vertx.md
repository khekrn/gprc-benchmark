# Chapter 5 — Coroutines + Vert.x: killing the callback

> By the end of this chapter you will rewrite a tangled callback chain
> into linear suspending code, know when to use `coAwait`,
> `vertxFuture(vertx, scope) { }`, `suspendCancellableCoroutine`, and
> `withContext`, and have built reusable bridges for any Vert.x async API.

This is the most practically useful chapter in Part 2. If you skim
nothing else, read this.

## 5.1 What the bridge actually is

There are two adapters between Kotlin coroutines and Vert.x:

| Direction                  | Tool                       | Module                        |
|----------------------------|----------------------------|-------------------------------|
| `Future<T>` → suspend value | `Future<T>.coAwait(): T`   | `vertx-lang-kotlin-coroutines`|
| suspend block → `Future<T>` | `vertxFuture(vertx, scope) { … }: Future<T>` | `vertx-lang-kotlin-coroutines`|
| anything async → suspend   | `suspendCancellableCoroutine { … }` | `kotlinx-coroutines-core`|

`coAwait` is the workhorse. It uses `suspendCancellableCoroutine` under
the hood and registers an `onComplete` handler that resumes.

`vertxFuture(...)` is the inverse. You want to expose a `Future`-returning
function to callers that don't speak `suspend` (e.g. the gRPC generator).
Inside the block, you write suspending code. The result becomes the
Future's value; a thrown exception becomes its failure.

A note on the call shape: `vertxFuture` is a **top-level function**, not a
method on a scope. Its signatures are `vertxFuture(scope) { }` and
`vertxFuture(vertx, scope) { }` — you pass the `CoroutineScope` (and, off a
Vert.x context, the `Vertx`) as arguments. There is no `scope.vertxFuture { }`
or `vertx.vertxFuture { }`. In our code we always pass both explicitly:
`vertxFuture(vertx, scope) { … }`.

## 5.2 The callback nightmare we are leaving behind

Imagine a route that:

1. Reads a user by id.
2. Reads their last 10 orders.
3. Calls an external pricing service for each order.
4. Returns aggregate JSON.

In raw Vert.x (Java-style chained Futures, before coroutines):

```kotlin
fun summary(ctx: RoutingContext) {
    val id = ctx.pathParam("id").toLong()
    repo.findById(id).compose { user ->
        if (user == null) Future.failedFuture(NotFound())
        else repo.lastOrders(user.id, 10).compose { orders ->
            val priced = orders.map { o -> pricer.priceFor(o) }
            Future.all(priced).map { cf ->
                val list = (0 until cf.size()).map { cf.resultAt<Price>(it) }
                Summary(user, orders, list)
            }
        }
    }.onSuccess { s -> ctx.response().end(toJson(s)) }
     .onFailure { e -> handle(ctx, e) }
}
```

Three nested `compose`. Two anonymous `Future.all`. Errors hop through
`.onFailure`. Stack traces lose half their meaning.

The same logic with coroutines:

```kotlin
private suspend fun summary(ctx: RoutingContext) {
    val id = ctx.pathParam("id").toLong()
    val user = repo.findById(id) ?: throw UserError.NotFound(id)
    val orders = repo.lastOrders(user.id, 10)
    val prices = coroutineScope {
        orders.map { o -> async { pricer.priceFor(o) } }.awaitAll()
    }
    ctx.response().end(toJson(Summary(user, orders, prices)))
}
```

Reads like normal code. Single-step debuggable. Errors bubble up.
Cancellation just works (Chapter 6).

## 5.3 The four bridges you'll write

### Bridge 1 — Suspend a Future

```kotlin
val u: User = repo.findById(1).coAwait()
```

You already know this. The point: it gives you a value or throws. No
`if (ar.succeeded()) … else …`. The compiler can flow-check it for nulls.

### Bridge 2 — Produce a Future from suspending code

```kotlin
fun pricedSummary(id: Long): Future<Summary> = vertxFuture(vertx, scope) {
    val user = repo.findById(id) ?: throw UserError.NotFound(id)
    val orders = repo.lastOrders(user.id, 10)
    val prices = coroutineScope {
        orders.map { o -> async { pricer.priceFor(o) } }.awaitAll()
    }
    Summary(user, orders, prices)
}
```

`vertxFuture(vertx, scope) { }` launches a coroutine in the scope you
hand it, dispatched on the **Vert.x event loop** (the `scope` is built
from `vertx.dispatcher()`). The Future completes when the block returns;
an exception fails it. The `scope` you pass is what ties the coroutine's
lifecycle to your verticle — see `UserGrpcService.kt`, where each unary
RPC is `vertxFuture(vertx, scope) { … }` over a
`CoroutineScope(SupervisorJob() + vertx.dispatcher())`.

### Bridge 3 — Bridge a custom async API

Suppose you have an SDK that takes a callback:

```kotlin
fun loadAsync(id: Long, cb: (Result<User>) -> Unit) { … }
```

You expose it as a suspending function with one line:

```kotlin
suspend fun load(id: Long): User = suspendCancellableCoroutine { cont ->
    loadAsync(id) { result ->
        result.onSuccess { cont.resume(it) }
              .onFailure { cont.resumeWithException(it) }
    }
}
```

The `cancellable` flavour lets the coroutine cancel the underlying
operation if the caller is cancelled. If your SDK can't cancel, just
let the result drop on resume (you'll trigger a `CancellationException`
when the value tries to come back).

### Bridge 4 — Adapt a ReadStream to a Flow

We have this in `CoroutineExtensions.kt`:

```kotlin
fun <T> ReadStream<T>.asFlow(capacity: Int = 16): Flow<T> {
    val stream = this
    return flow {
        val channel = Channel<T>(capacity)
        stream.handler { item -> channel.trySend(item) }
        stream.endHandler { channel.close() }
        stream.exceptionHandler { t -> channel.close(t) }
        stream.pause()
        stream.fetch(capacity.toLong())     // prime: request `capacity` items
        for (item in channel) {
            emit(item)
            stream.fetch(1)                 // request one more per item drained
        }
    }
}
```

A `Flow` is cold. A `ReadStream` is hot. The channel gives us a back-pressure
boundary. This is the *demand-driven* version: the stream starts paused, we
prime it with exactly `capacity` items via `fetch(capacity)`, and then pull
**one** more with `fetch(1)` for every item the collector drains. Because the
stream only ever delivers what we have asked for, the bounded channel can
never overflow and back-pressure propagates all the way upstream.

> **Why not the naive `pause()`-on-full version?** A tempting shortcut is to
> let the handler `trySend` and `pause()` when the channel is full, expecting
> a later `resume()`. That version *deadlocks*: once the buffer fills, the
> stream is paused but nothing in the cold-`Flow` collector path ever calls
> `resume()`/`fetch()`, so no further items arrive, `channel` never drains,
> and the collector blocks forever. Driving the stream with explicit
> `fetch(1)` per emitted item is what makes the bridge correct. Chapter 6
> explains back-pressure end-to-end.

## 5.4 Dispatching: when to use `withContext`

99 % of the time inside a `CoroutineVerticle`, you do not use
`withContext`. The default dispatcher *is* the Vert.x dispatcher. Every
suspension resumes on your event loop.

The exceptions:

- **CPU-heavy work** (JSON of 1 MB, image resize, regex on a long
  string). Use `withContext(Dispatchers.Default) { … }` to move it off
  the event loop. Or hop to a worker via `executeBlocking`.
- **Blocking SDK call** you can't avoid. Use
  `withContext(Dispatchers.IO) { blockingCall() }` but understand: the
  Vert.x event loop is fine while you're parked there; the *IO pool*
  thread is blocked. Or, better, hop to a virtual-thread dispatcher
  (Chapter 19).
- **MDC propagation**. `withContext(MDCContext(map))` (we have this in
  `MdcSupport.kt`).

## 5.5 Structured concurrency in one paragraph

When you start a coroutine inside another, the child's lifecycle is tied
to the parent's. If the parent is cancelled, all children are cancelled.
If a child fails, by default the parent is cancelled. If you don't want
that, wrap the children in a `supervisorScope`.

This is what removes the "leaked Future" bug from §3.8. There is no way
for a coroutine to escape its scope by accident. Chapter 6 goes deeper.

## 5.6 Three traps to avoid

1. **`runBlocking` inside a verticle.** It would block the event loop.
   Use `vertxFuture(vertx, scope) { }` or `launch { }` instead.
2. **`GlobalScope.launch`.** It detaches from your verticle scope. If
   the verticle stops, your coroutine keeps running and may leak the
   Pool / connection. Use the verticle's `launch` (it inherits the
   correct scope).
3. **Catching `CancellationException`.** Don't. It is the mechanism
   that propagates cancellation. If you catch it, rethrow:

   ```kotlin
   try {
       work()
   } catch (e: CancellationException) {
       throw e
   } catch (t: Throwable) {
       log.error("oops", t); throw t
   }
   ```

## 5.7 Worked example: replace a callback chain in the repository

There is no callback chain left in our repository because we wrote it
coroutine-first. Here is what `streamAll` would look like as a callback
chain (for comparison) and what it is now:

Callback version (note the API: you `prepare(sql)` to get a
`PreparedStatement`, then `createStream(fetchSize, tuple)` — there is no
`PreparedQuery.createStream`):

```kotlin
fun streamAll(): Future<List<User>> {
    val out = mutableListOf<User>()
    return pool.withTransaction { conn ->
        conn.prepare(SQL_STREAM_ALL).compose { ps ->
            val p = Promise.promise<Void>()
            val stream = ps.createStream(100, Tuple.tuple())
            stream.handler { row -> out.add(rowToUser(row)) }
            stream.exceptionHandler { t -> p.fail(t) }
            stream.endHandler { p.complete() }
            p.future()
        }
    }.map { out }
}
```

Coroutine + Flow version (this is what we actually run — a server-side
**cursor** read in `fetchSize` batches, exposed as a cold `Flow`):

```kotlin
fun streamAll(emailPrefix: String?, fetchSize: Int = 100): Flow<User> = flow {
    val sql = if (emailPrefix.isNullOrBlank()) SQL_STREAM_ALL else SQL_STREAM_PREFIX
    val params = if (emailPrefix.isNullOrBlank()) Tuple.tuple() else Tuple.of("$emailPrefix%")
    val conn = pool.connection.coAwait()
    try {
        val tx = conn.begin().coAwait()
        val cursor = conn.prepare(sql).coAwait().cursor(params)
        try {
            do {
                val rows = cursor.read(fetchSize).coAwait()
                for (row in rows) emit(rowToUser(row))   // suspends until collector is ready
            } while (cursor.hasMore())
        } finally {
            cursor.close().coAwait()
        }
        tx.commit().coAwait()
    } finally {
        conn.close().coAwait()
    }
}
```

The Flow version is **lazy and back-pressured**: because `emit` suspends
until the collector is ready, we never fetch a batch we cannot hand off, and
the dedicated connection + transaction are held open only for the lifetime of
the stream. The callback version buffers everything in `out` before
returning. (Full code, including the `LIKE`-prefix variant and row mapping,
is in `UserRepository.streamAll`.)

## 5.8 Exercises

1. Take the `Future`-chain example in §5.2 and step through it in a
   debugger. Count the stack frames you walk. Then do the same for the
   coroutine version.
2. Bridge `vertx.fileSystem().readFile(path)` (which returns a
   `Future<Buffer>`) to a `suspend fun readText(path: String): String`.
3. In `Routes.kt`, find `handleStreamUsers`. Convert it to use
   `vertxFuture(vertx, scope) { }` even though it doesn't strictly need
   to. Is the code clearer or worse?

---

[← Chapter 4](04-coroutines-internals.md) · [Next → Chapter 6: Structured concurrency, channels, flows](06-structured-concurrency.md)
