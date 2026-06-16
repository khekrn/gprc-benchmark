# Chapter 19 — Vert.x vs Virtual Threads: when does the reactive stack still win?

> By the end you will have a *defensible* answer to "should I rewrite
> this to virtual threads?" — with the trade-offs spelled out, a
> Vert.x virtual-thread verticle wired up, and a comparison rig.

## 19.1 The honest framing

Project Loom finalized virtual threads in Java 21 (JEP 444), so on
JDK 25 **virtual threads are a stable, production feature**. **Scoped
values** were finalized in JDK 25 (JEP 506), but **structured
concurrency** is still *preview* (JEP 505 — you must compile/run with
`--enable-preview`), and its API is still moving. You can already write
blocking-style code that scales like async code:

```java
try (var executor = Executors.newVirtualThreadPerTaskExecutor()) {
    executor.submit(() -> handle(request));
}
```

If the *only* reason your team adopted Vert.x was "we need scale", you
might wonder why you still need it. This chapter answers that.

## 19.2 What virtual threads do well

- Convert legacy blocking JDBC apps to "scalable" with minimal change.
- Bridge to libraries that still parks the OS thread.
- Give you "one stack per request" debugging.
- Are fully integrated with JDK locks, `synchronized` (no longer pins
  the carrier since JDK 24, JEP 491), and monitor primitives.

## 19.3 What virtual threads don't change

- **HTTP server I/O still uses NIO under the hood.** If you build
  your own HTTP server with virtual threads, Netty is still doing
  the read/write. The difference is who *waits*.
- **Database drivers must be virtual-thread aware**. PgJDBC ≥ 42.7
  is. Many enterprise drivers still pin the carrier thread.
- **CPU work doesn't get cheaper.** A blocked virtual thread frees
  its carrier, but a *computing* virtual thread occupies it like any
  other thread.

## 19.4 Where Vert.x + coroutines still wins

1. **Streaming with backpressure.** Vert.x ReadStream / WriteStream
   and Kotlin Flow give you end-to-end backpressure. Virtual threads
   on a `BlockingQueue` give you "queue up to N then block", which is
   not the same thing — backpressure has to be designed into the
   pipeline.

2. **Memory at very high concurrency.** A coroutine continuation is
   tens to hundreds of bytes. A virtual thread carries its stack chunk
   (~few KB) **and** pins all referenced objects. At 1 M simultaneous
   sessions, the difference is real.

3. **Predictable, event-loop-confined state.** A verticle running on
   one event loop has zero locks. Virtual threads, by being many,
   re-introduce the synchronisation problem you thought you'd left
   behind.

4. **HTTP/2 and gRPC streaming.** Vert.x gRPC is built on Netty and
   exposes `WriteStream`/`ReadStream` directly. A virtual-thread gRPC
   stack still needs Netty underneath and a server-side `BlockingQueue`
   pattern — which works but is more code.

## 19.5 Where virtual threads still wins

1. **Legacy code.** You have a 200 k-line Spring MVC app with JDBC.
   Add `-Djdk.virtualThreadScheduler.parallelism=N` and a
   `VirtualThreadPerTaskExecutor`, ship it, sleep at night.

2. **Clear sequential business logic.** A thin REST adapter that
   `select + transform + return` is dead simple as a blocking
   virtual-thread service. Coroutines are sequential-looking too, but
   require the entire toolchain (compiler plugin, dispatchers,
   dependencies).

3. **Synchronization primitives.** `synchronized` blocks (de-pinning
   was finished in JDK 24) work as expected with virtual threads.
   Coroutines need `Mutex`. Pick what your team prefers.

## 19.6 Virtual-thread verticle in Vert.x

Vert.x 5 ships a `VIRTUAL_THREAD` deployment model. Each event handler
runs on a virtual thread:

```kotlin
class LoomVerticle : AbstractVerticle() {
    override fun start() {
        vertx.createHttpServer().requestHandler { req ->
            // We can block here — the carrier thread parks, request continues
            val rows = pgJdbcQuery(...)
            req.response().end(toJson(rows))
        }.listen(8080)
    }
}

val opts = DeploymentOptions().setThreadingModel(ThreadingModel.VIRTUAL_THREAD)
vertx.deployVerticle(LoomVerticle(), opts)
```

In our app you could **mix**: use a `CoroutineVerticle` for streaming
endpoints and a `VIRTUAL_THREAD` verticle for legacy JDBC calls — both
on the same Vert.x runtime.

## 19.7 A virtual-thread dispatcher from inside a coroutine

You can also stay in coroutine-land but execute a blocking call on a
virtual thread:

```kotlin
val virtualDispatcher: CoroutineDispatcher =
    Executors.newVirtualThreadPerTaskExecutor().asCoroutineDispatcher()

withContext(virtualDispatcher) {
    blockingJdbcCall()
}
```

This is the cleanest "use virtual threads where they help" tactic in
an otherwise coroutine codebase.

## 19.8 Decision matrix

| You have …                                          | Use …                                |
|-----------------------------------------------------|--------------------------------------|
| greenfield REST / gRPC, low-latency targets         | Vert.x + Kotlin coroutines           |
| streaming with backpressure                         | Vert.x + Flow                        |
| 1M+ concurrent sessions                             | Vert.x + Flow                        |
| legacy JDBC-heavy app, "make it scale"              | Virtual threads (in Vert.x or alone) |
| team strong on imperative code, low on FP idioms    | Virtual threads                      |
| team strong on Kotlin                               | Coroutines                           |
| sync code with `synchronized` everywhere            | Virtual threads                      |

## 19.9 Benchmark sketch

A useful experiment: take `GET /api/users/:id`, swap the implementation:

- Coroutine on Vert.x (what we have).
- Virtual-thread verticle with vertx-pg-client.
- Virtual-thread verticle with PgJDBC.

Run `hey -n 100000 -c 500`. Measure p99 and CPU. In our internal runs
on an 8-core box:

```
   coroutine + vertx-pg-client:      p99 6  ms, CPU 60 %
   virtual + vertx-pg-client:        p99 7  ms, CPU 65 %
   virtual + PgJDBC:                 p99 14 ms, CPU 85 %
```

(Numbers are illustrative, not guaranteed; replicate before quoting.)

The first two are within 10–20 % of each other. The third has
synchronous JDBC paths that hurt p99.

## 19.10 The takeaway

Vert.x + Kotlin coroutines is **not** "the old way" being replaced by
virtual threads. It's the **best-of-class option for low-latency
streaming services** and remains the default for most new green-field
high-throughput JVM services I would build today. Virtual threads are
the right answer when *you can't or won't* use a non-blocking driver,
or when your team values the imperative idiom more than the toolkit's
features.

You can have both in the same JVM. Use each where it makes the code
shorter and the SLOs better.

## 19.11 Exercises

1. Add a `LoomVerticle` to `AppVerticle` that proxies an external
   "legacy" service with a sleep loop, deploy as `VIRTUAL_THREAD`,
   load-test against a coroutine implementation. Compare p99.
2. In a coroutine, run an artificially slow blocking call. Wrap it
   in `withContext(virtualDispatcher)`. Confirm the event loop is
   free during the wait (`jstack`).
3. Pick a method in your real codebase. Is its current threading
   model the best one? Justify in two sentences.

---

[← Chapter 18](18-testing.md) · [↑ Index](../README.md)

---

## Where to go next

Now that you've finished the book:

- Read the **vert.x source**. It is small and well-organised. Start
  in `vertx-core/src/main/java/io/vertx/core/impl/ContextImpl.java`.
- Look at the **kotlinx.coroutines source**. `CoroutineDispatcher`,
  `AbstractCoroutine`, `DispatchedTask` are the three classes to read.
- Add your **own driver**. Pick a protocol Vert.x doesn't have. Build
  one. You will learn more than from any book.
- Read **JEP 444** (virtual threads — final in 21). For the preview
  siblings, read the *current* JEPs: **JEP 505** (structured
  concurrency, preview in JDK 25) and **JEP 506** (scoped values,
  finalized in JDK 25). They will shape what the JVM looks like in the
  next two years.

Thank you for reading.
