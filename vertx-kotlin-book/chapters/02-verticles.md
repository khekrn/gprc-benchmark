# Chapter 2 — Verticles & deployment models

> By the end of this chapter you will be able to deploy a verticle, choose
> between standard / worker / virtual-thread verticles, scale by
> instances, and understand exactly which thread your `start()` runs on.

## 2.1 What is a verticle?

A verticle is **the unit of deployment in Vert.x**. It is a class with a
lifecycle (`start`, `stop`), bound to a single Vert.x `Context`, which is
bound to a single event loop. Think of a verticle as a tiny actor: one
inbox (its event loop's task queue), no shared mutable state with other
verticles, communication via the **event bus** (Chapter 4 — covered in
this book's appendix).

In our app the verticle is `AppVerticle`:

```kotlin
// code/full-app/src/main/kotlin/com/example/app/verticles/AppVerticle.kt
class AppVerticle : CoroutineVerticle() {
    override suspend fun start() {
        // wire pool, repo, service, http, grpc
    }
    override suspend fun stop() {
        // close pool, http, grpc
    }
}
```

The base class `CoroutineVerticle` gives us:

- a `vertx` field — the Vert.x instance,
- a `CoroutineScope` whose `Job` is cancelled in `stop()` — structured
  concurrency for free,
- a default `coroutineContext` that uses `Dispatchers.Vertx` so
  `coAwait`/`launch` resume back on **the same event loop** that ran
  `start()`,
- `suspend fun start()` / `suspend fun stop()` lifecycle hooks.

## 2.2 The three verticle flavours

```
┌─────────────────────────────────────────────────────────────────────┐
│  Standard verticle    │  All callbacks run on ONE event loop.       │
│  AbstractVerticle     │  Never block.                                │
│                       │  Use for typical async I/O.                  │
├─────────────────────────────────────────────────────────────────────┤
│  Worker verticle      │  Runs on the worker thread pool.            │
│  setWorker = true     │  Blocking is allowed.                       │
│                       │  Use for legacy / JDBC / file zipping etc.  │
├─────────────────────────────────────────────────────────────────────┤
│  Virtual-thread       │  ThreadingModel.VIRTUAL_THREAD (Vert.x 5).  │
│  verticle             │  Each event handler runs on a virtual       │
│                       │  thread that can park on blocking calls.    │
└─────────────────────────────────────────────────────────────────────┘
```

The Kotlin variant we use, `CoroutineVerticle`, is a **standard verticle**.
Its event loop thread is never blocked because every `await` releases it.

Deploying a worker verticle:

```kotlin
val opts = DeploymentOptions().setThreadingModel(ThreadingModel.WORKER)
vertx.deployVerticle(LegacyJdbcVerticle(), opts)
```

Deploying a virtual-thread verticle (since Vert.x 4.5):

```kotlin
val opts = DeploymentOptions().setThreadingModel(ThreadingModel.VIRTUAL_THREAD)
vertx.deployVerticle(LoomFriendlyVerticle(), opts)
```

We compare virtual-thread verticles to coroutine verticles in **Chapter 19**.

## 2.3 Scaling out: `setInstances(N)`

```kotlin
val opts = DeploymentOptions().setInstances(4)
vertx.deployVerticle(::AppVerticle, opts)
```

Vert.x will create four instances of `AppVerticle`. Each is bound to a
different event loop (round-robin). When the HTTP server starts on a
verticle, Vert.x uses **`SO_REUSEPORT`** so the same `:8080` is shared.
The kernel load-balances incoming TCP connections.

```
            ┌── AppVerticle #1 ── EventLoop-0
listen(8080)│── AppVerticle #2 ── EventLoop-1
            │── AppVerticle #3 ── EventLoop-2
            └── AppVerticle #4 ── EventLoop-3

kernel SO_REUSEPORT distributes new TCP sockets across the four sockets
```

Rule of thumb: **instances = cores** is a good starting point if your
workload is CPU-light per request, but **instances = N where N < cores**
gives you headroom for other work (GC threads, native transport threads).
In **Chapter 17** we measure both.

> **Note: `deployVerticle(::AppVerticle)` vs `deployVerticle(AppVerticle())`** —
> when you pass a factory (`::AppVerticle`), Vert.x calls it `N` times to
> get `N` fresh instances. If you pass an instance, you get exactly one,
> and `setInstances(N)` is silently ignored. Easy mistake.

## 2.4 The verticle lifecycle in detail

```
Time ────────────────────────────────────────────────────────────►

  deployVerticle
        │
        ▼
   pick event loop ───────── verticle bound to Context_i  ────────────────
        │                                                  │
        │                                                  │ start() runs
        │                                                  │ on EventLoop_i
        │                                                  ▼
        │                                                .................
        │                                                . user code     .
        │                                                .................
        │                                                  │
   deployment id ◄────────────────────────── start() returns │
                                                              │
   handler events arrive on Context_i, run on EventLoop_i ───┤
                                                              │
   stop() called on Context_i  ─────── undeploy() invoked ───┤
                                                              │
   Context's coroutine Job is cancelled, scope children stopped
                                                              │
   resources released                                         ▼
                                                          done
```

`start()` and `stop()` run on the **same** event-loop thread. Any handler
that you register in `start()` will fire on that thread. The Vert.x
`Context` is the identity that ties them together.

## 2.5 Why a verticle is *not* just an init function

You could in principle write all the wiring in `main()` and skip the
verticle. People do this. You lose three things:

1. **Lifecycle.** Verticles deploy and undeploy cleanly. You can replace
   a misbehaving verticle without restarting the JVM (e.g. config hot
   reload).
2. **Instance multiplication.** `setInstances(N)` is one line. Replicating
   the wiring in `main()` is many lines.
3. **Event-loop affinity.** Verticles are pinned to a Context. Code you
   write in `main()` runs on `main`'s thread.

We use a verticle for those reasons. We do *not* use the event bus to
communicate between verticles in this book — for a single REST/gRPC
service it adds little value. The event bus shines in larger apps.

## 2.6 Multiple verticles vs one big verticle

Should you split `AppVerticle` into an `HttpVerticle` and a
`GrpcVerticle`?

Arguments for:

- Different lifecycles. You can take HTTP offline for maintenance.
- Different concurrency knobs. You can give HTTP 4 instances and gRPC 2.

Arguments against:

- Two verticles means you may need the event bus to share `UserService`,
  or you need to put the service in shared state on `vertx`.
- More moving parts.

For the demo app, one verticle is simpler. In Chapter 17 we revisit this
when measuring per-protocol throughput.

## 2.7 Wiring without a DI framework

Look at the top of `AppVerticle.start()`:

```kotlin
pool = DbModule.pool(vertx, cfg.db)
if (cfg.db.schemaOnStartup) DbMigrator.migrate(pool)

val repo    = UserRepository(pool)
val service = UserService(repo)
```

That is the whole "DI". The composition root is a function. Every
dependency is constructor-injected. You can follow data flow by reading
top-to-bottom. When you need test doubles, build them and pass them in.

If you really want Koin or Guice for a bigger app, fine — but resist
making them the *organising principle*. Vert.x apps are small enough
that a single composition function is readable.

## 2.8 Exercises

1. Deploy `AppVerticle` with `setInstances(4)`. Print
   `Thread.currentThread().name` in the HTTP handler. Observe four
   distinct names.
2. Add `setHa(true)` to your `DeploymentOptions` after enabling
   clustering (we don't, in this app). What changes? (Hint: read the
   Vert.x HA docs.)
3. Convert `AppVerticle` from a `CoroutineVerticle` to an
   `AbstractVerticle`. Notice how many `Future.onSuccess/onFailure`
   chains you have to write. This is the case for coroutines.

---

[← Chapter 1](01-event-loop.md) · [Next → Chapter 3: Futures & async APIs](03-futures.md)
