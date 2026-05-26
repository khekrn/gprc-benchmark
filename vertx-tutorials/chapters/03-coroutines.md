# Chapter 3 — Kotlin Coroutines with Vert.x

> **You should finish this chapter able to:** use `coAwait` correctly, bridge
> between `Future<T>` and `suspend`, understand which dispatcher your code
> is running on, and apply structured concurrency to Vert.x handlers.

## 3.1  Why coroutines beat callbacks

Compare:

```kotlin
// Callback / Future style
fun handleGet(ctx: RoutingContext) {
    val id = ctx.pathParam("id")
    cache.get(id).onComplete { ar ->
        if (ar.succeeded() && ar.result() != null) {
            ctx.json(ar.result())
        } else {
            db.findById(id).onComplete { dbAr ->
                if (dbAr.failed()) { ctx.fail(dbAr.cause()); return@onComplete }
                val user = dbAr.result() ?: return@onComplete ctx.response().setStatusCode(404).end()
                cache.put(id, user).onComplete { ctx.json(user) }
            }
        }
    }
}
```

```kotlin
// Coroutine style
suspend fun handleGet(ctx: RoutingContext) {
    val id = ctx.pathParam("id")
    val user = cache.get(id) ?: db.findById(id)?.also { cache.put(id, it) }
    user?.let(ctx::json) ?: ctx.response().setStatusCode(404).end()
}
```

Both execute on the same event loop with the same throughput characteristics.
The coroutine version is dramatically easier to read, has *one* exception
flow instead of three, and works with normal Kotlin control flow (`?.let`,
`try/finally`, loops, etc.).

The translation is mechanical: `kotlinx-coroutines` saves the rest of the
function as a state-machine continuation on the heap, returns the event loop
to do other work, and resumes the continuation when the future completes.

## 3.2  The five things to know

You need to understand five names. Everything else is a consequence.

### 3.2.1  `vertx.dispatcher()` — a `CoroutineDispatcher`

A dispatcher answers "what thread should this coroutine run on?". Vert.x
gives you one that schedules on the current `Context` (event loop):

```kotlin
import io.vertx.kotlin.coroutines.dispatcher

GlobalScope.launch(vertx.dispatcher()) { ... }
```

Inside a `CoroutineVerticle`, `this` is already a `CoroutineScope` bound to
this dispatcher, so you can write `launch { ... }` and you are on the verticle's
event loop automatically.

### 3.2.2  `coAwait()` — `Future<T>.coAwait(): T`

Extension on Vert.x's `Future<T>`. Suspends the coroutine until the future
completes, then returns the value (or throws the failure):

```kotlin
val server: HttpServer = vertx.createHttpServer().listen(8080).coAwait()
```

`coAwait()` does not give up the event loop in the OS-thread sense — it
*suspends the coroutine*, which means the event loop runs the next event
while this coroutine is paused. When the future completes, a continuation is
scheduled on the originating Context.

### 3.2.3  `vertxFuture { ... }` — `suspend → Future<T>`

The reverse direction. Wraps a suspending block and returns a `Future<T>`:

```kotlin
fun getUser(id: String): Future<User?> = vertxFuture {
    cache.get(id) ?: db.findById(id)
}
```

This is exactly how we expose suspending domain code to Vert.x APIs that
expect a `Future` — e.g., our generated gRPC service in
[`UserGrpcService.kt`](../code/full-app/src/main/kotlin/com/example/app/grpc/UserGrpcService.kt#L30).

The block runs on the current event-loop Context, on a coroutine, and the
returned Future completes with its result or exception.

### 3.2.4  `awaitBlocking { ... }` — bridge to blocking code

Sometimes you must call a blocking API (legacy driver, file I/O). Wrap it:

```kotlin
val bytes: ByteArray = awaitBlocking { Files.readAllBytes(Path.of("/etc/hosts")) }
```

This runs the block on Vert.x's worker pool and suspends until done. **Do not
use it for "I want to wait for a Future"** — that is `coAwait()`. Use
`awaitBlocking` strictly for code that itself blocks the calling thread.

### 3.2.5  `CoroutineVerticle` — a verticle with a coroutine scope

Already used in `AppVerticle.kt`. Properties:

- `start()` / `stop()` are `suspend`.
- `this` implements `CoroutineScope`. Its `coroutineContext` is
  `vertx.dispatcher() + Job()`, scoped to the verticle.
- `stop()` cancels the scope, so any background `launch { ... }` you started
  in `start()` is cancelled cleanly.

## 3.3  Worked example: the request handler

Here is the actual REST handler from
[`HttpServerFactory.kt`](../code/full-app/src/main/kotlin/com/example/app/http/HttpServerFactory.kt#L51):

```kotlin
router.coroutineRouter {
    route("/api/v1/users/:id").coHandler { ctx ->
        val id = ctx.pathParam("id")
        val user = users.get(id)                 // suspends
        if (user == null) ctx.response().setStatusCode(404).end()
        else ctx.json(JsonObject.mapFrom(user))
    }
}
```

`coroutineRouter { }` is the Kotlin DSL from `vertx-lang-kotlin-coroutines`.
`coHandler { ctx -> ... }` registers a *suspending* handler. Underneath,
Vert.x Web wraps it in a normal `Handler<RoutingContext>` that calls
`vertxFuture { yourBlock(ctx) }` and forwards any exception to the router's
failure handler.

This is the entire bridge. There is no other magic.

## 3.4  Structured concurrency in Vert.x

Inside a coroutine you can `async { ... }` to run sub-tasks concurrently:

```kotlin
suspend fun fetchDashboard(userId: String): Dashboard = coroutineScope {
    val u = async { userService.get(userId) }
    val o = async { orderService.last10(userId) }
    val n = async { notificationService.unread(userId) }
    Dashboard(u.await(), o.await(), n.await())
}
```

All three sub-calls run on the same event loop. They do not run on
"parallel CPU threads" — they interleave on the loop. But because each is
mostly I/O-bound (DB / cache / RPC), they overlap nicely: while `u` is
waiting for the DB, `o` is sending its query, etc.

`coroutineScope` enforces structured concurrency: if any child fails, the
others are cancelled and the scope rethrows. If the outer coroutine is
cancelled (e.g., HTTP client closed the connection), all children are
cancelled. This is exactly what you want for request-scoped work.

## 3.5  Bridging the other way: a Future-returning callback

If you wrote a non-suspending API that returns a `Future<T>`, you can
consume it from suspending code with `coAwait()`. Going the other way —
*calling* a suspending function from non-suspending Vert.x code — needs
`vertxFuture`:

```kotlin
class UserHandler(private val users: UserService) {
    // Non-suspending Vert.x handler.
    fun handle(ctx: RoutingContext) {
        vertxFuture { users.get(ctx.pathParam("id")) }
            .onSuccess { user ->
                if (user == null) ctx.response().setStatusCode(404).end()
                else ctx.json(JsonObject.mapFrom(user))
            }
            .onFailure(ctx::fail)
    }
}
```

This is functionally identical to using `coHandler { ... }`. Use whichever
fits the calling style.

## 3.6  When NOT to use coroutines

Coroutines are general; Vert.x is opinionated. Two cases where a callback
or `Future` chain is genuinely simpler:

1. **Fire-and-forget metric updates.** `meterRegistry.counter("foo").increment()`
   is synchronous. No coroutine.
2. **Single-future glue.** `server.listen(8080).onSuccess { ... }` inside a
   non-suspending callback site is fine. Don't introduce `vertxFuture` just
   to use `coAwait`.

## 3.7  The "what thread am I on?" debugging trick

When you cannot tell, log it:

```kotlin
log.info("thread={}, context={}",
    Thread.currentThread().name,
    Vertx.currentContext()?.deploymentID() ?: "no-context")
```

Drop this at the entry of any suspending block during development. The
thread name from §1.6 tells you whether you are still on an event loop. If
the thread name is `vert.x-worker-thread-N`, you must have crossed onto the
worker pool (via `executeBlocking` or `awaitBlocking`) — usually
deliberately, sometimes by accident.

## 3.8  Cancellation

When the HTTP client disconnects, Vert.x cancels the request handler's
coroutine scope. *In-flight* suspending calls receive `CancellationException`
at their next suspension point. `coAwait()` and `awaitBlocking` propagate
cancellation cooperatively. SQL queries in `vertx-pg-client` *do not yet*
cancel their underlying network message — the cancellation will surface
once the response arrives. This is usually fine for short-lived queries.

For tasks that must run to completion regardless (writing audit logs),
launch in a scope that survives cancellation:

```kotlin
class AppVerticle : CoroutineVerticle() {
    private val supervisor = SupervisorJob()
    private val audit = CoroutineScope(vertx.dispatcher() + supervisor)

    override suspend fun start() {
        audit.launch { /* never cancelled by a request */ }
    }
    override suspend fun stop() { supervisor.cancelAndJoin() }
}
```

## 3.9  Try it

1. Replace the gRPC `vertxFuture { ... }` wrapper in `UserGrpcService.kt`
   with a manual `Future`-style implementation. Compare line counts.
2. Add `coroutineScope { ... }` with two parallel `async` calls (one to
   Postgres, one to Redis) in `UserService.get`. Measure the latency
   difference for a cache miss.
3. Cause the request handler to throw inside a child `async`. Verify the
   sibling `async` is cancelled (add a `try/finally` log) and the outer
   response becomes a 500.

[← Ch 2](02-verticles.md) · [Next: Chapter 4 — Event Bus →](04-event-bus.md)
