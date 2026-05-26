# Chapter 2 — Verticles & Deployment

> **You should finish this chapter able to:** choose between standard, worker,
> and coroutine verticles, deploy multiple instances and reason about how
> Vert.x distributes them across event loops, and understand
> `DeploymentOptions`.

## 2.1  What is a verticle, really?

A verticle is just **a Vert.x deployment unit**. It is a class with two
lifecycle methods, deployed at runtime to a specific `Context` (event loop).
Conceptually:

```
+----------------------------------------------------+
|  Verticle                                          |
|  ├── start()  : called once, on its event loop     |
|  ├── stop()   : called once on undeploy            |
|  └── all callbacks registered in start() run on    |
|      the same event loop (same Context)            |
+----------------------------------------------------+
```

That's it. No reflection, no annotations, no DI container. Deploy one as a
unit of isolation; deploy many for horizontal scale within one process.

## 2.2  The three flavors

Vert.x ships three base classes; in Kotlin you mainly use the third.

### `AbstractVerticle` — callback style (Java idiomatic)

```kotlin
class Old : AbstractVerticle() {
    override fun start(startPromise: Promise<Void>) {
        vertx.createHttpServer()
            .requestHandler { it.response().end("hi") }
            .listen(8080)
            .onSuccess { startPromise.complete() }
            .onFailure(startPromise::fail)
    }
}
```

You will only see this in older codebases. Use it if you are restricted to
plain Java.

### `WorkerVerticle` — runs on the worker pool, can block

You declare a verticle as a worker via deployment options:

```kotlin
vertx.deployVerticle(BlockyVerticle(), DeploymentOptions()
    .setThreadingModel(ThreadingModel.WORKER))
```

A worker verticle's `start()` and every handler runs on a thread from the
worker pool. It is *allowed* to block. This is rarely the right tool — using
`executeBlocking` (chapter 5) for short blocking calls is finer-grained.

### `CoroutineVerticle` — our default

```kotlin
class AppVerticle : CoroutineVerticle() {
    override suspend fun start() {
        val server = vertx.createHttpServer()
            .requestHandler { ... }
            .listen(8080)
            .coAwait()                     // suspends, no callbacks
    }

    override suspend fun stop() { /* clean up */ }
}
```

`CoroutineVerticle` gives you:

- `suspend fun start()` and `suspend fun stop()`.
- A `CoroutineScope` (`this` is a scope) bound to the verticle lifecycle, so
  `launch { ... }` you start in `start()` is cancelled automatically on
  `stop()`.
- A `CoroutineDispatcher` (`vertx.dispatcher()`) that wraps the event loop
  Context — *every* coroutine launched on it runs on the verticle's event loop.

This is the only base class we use in `full-app`. See
[`AppVerticle.kt`](../code/full-app/src/main/kotlin/com/example/app/verticles/AppVerticle.kt).

## 2.3  Deployment options that actually matter

`DeploymentOptions` has many setters; the ones you will use 99% of the time:

| Setter | What it does | Default |
|---|---|---|
| `setInstances(n)` | Deploy `n` independent verticle instances. | 1 |
| `setThreadingModel(...)` | `EVENT_LOOP`, `WORKER`, or `VIRTUAL_THREAD`. | `EVENT_LOOP` |
| `setConfig(json)` | A `JsonObject` available to the verticle via `config()`. | empty |
| `setWorkerPoolName("x")` | Use a separately-sized worker pool for this verticle. | shared |
| `setWorkerPoolSize(n)` | Size that pool. | 20 |
| `setHa(true)` | High-availability re-deployment in a clustered Vert.x. | false |
| `setMaxWorkerExecuteTime(...)` | Override the blocked-thread checker for workers. | 60s |

### Instances and event loop assignment

With `instances=N` and the default `ThreadingModel.EVENT_LOOP`, Vert.x assigns
each instance to an event loop in round-robin order. If you have 4 event
loops and 8 instances, instances 0/4 share loop 0, 1/5 share loop 1, etc.

```
event loops:   EL-0    EL-1    EL-2    EL-3
                │       │       │       │
                ▼       ▼       ▼       ▼
instances:    [V0,V4] [V1,V5] [V2,V6] [V3,V7]
```

This matters for two reasons:

1. **Connection acceptance scales with instances**, not loops. An HTTP server
   created inside `start()` is bound *per instance*, so deploying `N`
   instances effectively gives you `N` accept points (Vert.x uses
   `SO_REUSEPORT` so they all bind to the same port — see
   [`HttpServerFactory.kt`](../code/full-app/src/main/kotlin/com/example/app/http/HttpServerFactory.kt#L13)).
2. **State within an instance is single-threaded.** A `var counter = 0` in
   the verticle class is safely incremented from any handler in that
   instance without a lock. Across instances you need the event bus.

### A common deployment pattern

Our `Main.kt` does this:

```kotlin
val deploymentOptions = DeploymentOptions()
    .setInstances(Runtime.getRuntime().availableProcessors())   // 1 per core
    .setThreadingModel(ThreadingModel.EVENT_LOOP)

vertx.deployVerticle(::AppVerticle, deploymentOptions).coAwait()
```

We use the *supplier* form `::AppVerticle` (a function reference) rather
than `AppVerticle()`. With a supplier, Vert.x calls it `N` times to build
`N` distinct instances. If we passed a single instance, all "instances"
would be the same object and you would have unintentional shared mutable
state. **Always use the supplier form for `instances > 1`.**

## 2.4  Lifecycle: when does start() actually run?

```
                deployVerticle()
                       │
                       ▼
       ┌───────────────────────────────┐
       │  Vert.x picks an event loop   │
       │  via round-robin              │
       └────────────────┬──────────────┘
                        │
                        ▼
       ┌───────────────────────────────┐
       │  schedules a task on that EL  │
       └────────────────┬──────────────┘
                        │
                        ▼
       ┌───────────────────────────────┐
       │  EL calls verticle.init(...)   │  (gives access to vertx, config)
       │  EL calls verticle.start(p)    │  (your code runs)
       │  start() completes the promise │
       │  → deployVerticle's Future     │
       │    completes                   │
       └────────────────┬──────────────┘
                        │ deployment id
                        ▼
                  back to caller
```

If `start()` throws or completes exceptionally, the verticle is *not*
deployed — `deployVerticle().coAwait()` rethrows. The half-initialized
verticle is discarded.

## 2.5  Hierarchical deployment (composition)

A verticle can deploy *child* verticles from its own `start()`. The most
common shape is "one bootstrap verticle that wires the world":

```
              ┌──────────────────┐
              │  AppVerticle     │   our pattern in full-app
              │  start():        │
              │    build deps    │
              │    listen 8080   │
              │    listen 9090   │
              └─────────┬────────┘
                        │ deployVerticle( ... )
              ┌─────────▼────────┐
              │  WorkerVerticle  │   (optional — for CPU work)
              └──────────────────┘
```

When the parent is undeployed, children are *not* automatically undeployed.
Track child deployment IDs in `start()` and undeploy them in `stop()`.

## 2.6  Virtual threads (Java 21+)

Vert.x 5 introduces `ThreadingModel.VIRTUAL_THREAD`. Each handler runs on a
fresh virtual thread:

```kotlin
val opts = DeploymentOptions().setThreadingModel(ThreadingModel.VIRTUAL_THREAD)
vertx.deployVerticle({ MyVerticle() }, opts)
```

The verticle code can call blocking APIs (JDBC, file I/O) and the JVM will
park the *virtual* thread without parking the underlying carrier. This is
genuinely useful for code paths you cannot easily make non-blocking (legacy
SDKs, ImageIO). But:

- A virtual-thread verticle is **not** a substitute for the event loop. The
  event loop is faster for I/O-bound code that you already wrote non-blocking.
- Mixing styles is fine. Have most verticles on `EVENT_LOOP` and one on
  `VIRTUAL_THREAD` that wraps the blocking SDK.

We cover virtual threads in depth in chapter 5.

## 2.7  Undeployment and graceful shutdown

`vertx.undeploy(id)` calls `stop()` and removes the verticle. The Future
completes when `stop()` returns (or throws). Our
[`AppShutdown.kt`](../code/full-app/src/main/kotlin/com/example/app/observability/AppShutdown.kt)
hooks SIGTERM to run undeploy then `vertx.close()`:

```kotlin
Runtime.getRuntime().addShutdownHook(Thread {
    vertx.undeploy(deploymentId).toCompletionStage().toCompletableFuture().get()
    vertx.close().toCompletionStage().toCompletableFuture().get()
})
```

We block in the hook because the JVM is already shutting down — there is no
"event loop work" to preserve, and the OS gives us a finite grace period.

## 2.8  Things people get wrong

**Mutating verticle state from a coroutine inside another verticle.**
Coroutines launched on verticle A's dispatcher do not magically marshal to
verticle B. Use the event bus.

**Deploying many verticles "just in case."** Each verticle is cheap but not
free (one Context, a name in JMX, etc.). Match `instances` to your CPU
budget, not arbitrarily.

**Setting `setMaxEventLoopExecuteTime` very high "because my handler is slow."**
That hides the symptom of a blocked event loop without fixing the root cause.
If a handler legitimately needs >100 ms of CPU, move it to a worker
(chapter 5).

**Sharing pools across modules incorrectly.** Postgres pool / Redis client
are tied to a Vert.x instance, not a verticle. Create them once in your
root verticle and pass them to children, or use `Pool.shared(...)`. We
discuss this in chapters 8 and 9.

## 2.9  Try it

1. In `Main.kt`, change `instances` from `availableProcessors()` to `1`. Run
   `wrk -t 8 -c 256 -d 30s http://localhost:8080/api/v1/users/...`. Compare
   throughput to the default.
2. Subclass `AppVerticle` to print `"instance ${context.deploymentID()}: hi
   from ${Thread.currentThread().name}"` in `start()`. Deploy 4 instances.
   Observe: how many *distinct* thread names do you see?
3. Implement a `MetricsVerticle` that registers a Micrometer
   `JvmThreadMetrics` and a `vertx.setPeriodic(10_000) { ... }` to log JVM
   thread count. Wire it into `AppVerticle.start()` via `deployVerticle`.

[← Ch 1](01-event-loop.md) · [Next: Chapter 3 — Kotlin coroutines integration →](03-coroutines.md)
