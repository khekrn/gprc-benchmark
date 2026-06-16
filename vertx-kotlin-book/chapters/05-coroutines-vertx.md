# Chapter 5 — Coroutines + Vert.x: killing the callback

> By the end of this chapter you will rewrite a tangled callback chain
> into linear suspending code, know when to use `coAwait`,
> `vertxFuture { }`, `suspendCancellableCoroutine`, and `withContext`,
> and have built reusable bridges for any Vert.x async API.

This is the most practically useful chapter in Part 2. If you skim
nothing else, read this.

## 5.1 What the bridge actually is

There are two adapters between Kotlin coroutines and Vert.x:

| Direction                  | Tool                       | Module                        |
|----------------------------|----------------------------|-------------------------------|
| `Future<T>` → suspend value | `Future<T>.coAwait(): T`   | `vertx-lang-kotlin-coroutines`|
| suspend block → `Future<T>` | `vertxFuture { … }: Future<T>` | `vertx-lang-kotlin-coroutines`|
| anything async → suspend   | `suspendCancellableCoroutine { … }` | `kotlinx-coroutines-core`|

`coAwait` is the workhorse. It uses `suspendCancellableCoroutine` under
the hood and registers an `onComplete` handler that resumes.

`vertxFuture { }` is the inverse. You want to expose a `Future`-returning
function to callers that don't speak `suspend` (e.g. the gRPC generator).
Inside the block, you write suspending code. The result becomes the
Future's value; a thrown exception becomes its failure.

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
fun pricedSummary(id: Long): Future<Summary> = vertxFuture {
    val user = repo.findById(id) ?: throw UserError.NotFound(id)
    val orders = repo.lastOrders(user.id, 10)
    val prices = coroutineScope {
        orders.map { o -> async { pricer.priceFor(o) } }.awaitAll()
    }
    Summary(user, orders, prices)
}
```

`vertxFuture { }` launches a coroutine on the **current Vert.x Context**
(i.e. the event loop you're on). The Future completes when the block
returns; an exception fails it.

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
fun <T> ReadStream<T>.asFlow(capacity: Int = Channel.BUFFERED): Flow<T> {
    val channel = Channel<T>(capacity)
    handler { item ->
        val ok = channel.trySend(item).isSuccess
        if (!ok) pause()
    }
    exceptionHandler { t -> channel.close(t) }
    endHandler { channel.close() }
    return channel.consumeAsFlow()
}
```

A `Flow` is cold. A `ReadStream` is hot. The channel gives us a back-pressure
boundary. When the collector is slow, `trySend` returns failure, we
`pause()` the upstream stream. When the channel drains, the consumer
calls `resume` (implicitly via channel sends), which Vert.x resumes via
`fetch(1)`. Chapter 6 explains backpressure end-to-end.

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
   Use `vertxFuture { }` or `launch { }` instead.
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

Callback version:

```kotlin
fun streamAll(): Future<List<User>> {
    val out = mutableListOf<User>()
    return pool.withTransaction { conn ->
        val p = Promise.promise<Void>()
        val stream = conn.preparedQuery(SQL_STREAM_ALL).createStream(100, Tuple.tuple())
        stream.handler { row -> out.add(rowToUser(row)) }
        stream.exceptionHandler { t -> p.fail(t) }
        stream.endHandler { p.complete() }
        p.future()
    }.map { out }
}
```

Coroutine + Flow version (we use this in production):

```kotlin
fun streamAll(emailPrefix: String?, fetchSize: Int = 100): Flow<User> {
    val channel = Channel<User>(capacity = fetchSize)
    pool.withTransaction { conn ->
        val stream = conn.preparedQuery(SQL).createStream(fetchSize, params)
        stream.handler { row -> if (!channel.trySend(rowToUser(row)).isSuccess) stream.pause() }
        stream.exceptionHandler { t -> channel.close(t) }
        stream.endHandler { channel.close() }
        channel.invokeOnClose { stream.close() }
        Future.succeededFuture<Void>()
    }.onFailure { t -> channel.close(t) }
    return channel.consumeAsFlow()
}
```

The Flow version is **lazy and back-pressured**. The first version
buffers everything in `out` before returning.

## 5.8 Exercises

1. Take the `Future`-chain example in §5.2 and step through it in a
   debugger. Count the stack frames you walk. Then do the same for the
   coroutine version.
2. Bridge `vertx.fileSystem().readFile(path)` (which returns a
   `Future<Buffer>`) to a `suspend fun readText(path: String): String`.
3. In `Routes.kt`, find `handleStreamUsers`. Convert it to use
   `vertxFuture { }` even though it doesn't strictly need to. Is the
   code clearer or worse?

---

[← Chapter 4](04-coroutines-internals.md) · [Next → Chapter 6: Structured concurrency, channels, flows](06-structured-concurrency.md)
