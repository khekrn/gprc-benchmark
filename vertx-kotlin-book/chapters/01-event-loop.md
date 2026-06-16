# Chapter 1 — The event loop & Netty internals

> By the end of this chapter you will be able to (a) explain where bytes
> physically arrive on disk and how Vert.x dispatches them, (b) point at
> the line of code in `Main.kt` that decides how many loops you have,
> (c) recognise an event-loop violation in `jstack`.

## 1.1 The mental model: a producer/consumer with one consumer

```
   network FD ── select() ──►  ┌────────────────────────────────────┐
                               │  EventLoop-0 (one OS thread)       │
                               │                                    │
  vertx.runOnContext { … } ──► │  taskQueue: T₀ T₁ T₂ T₃ …          │
                               │                                    │
   vertx.setTimer(…)        ── │  scheduledQueue: due in 50 ms      │
                               │                                    │
                               │  while (!shutting_down) {          │
                               │    runAllReadyTasks(taskQueue);    │
                               │    runDueTimers(scheduledQueue);   │
                               │    selector.select(timeout);       │
                               │    dispatchIO(selector);           │
                               │  }                                 │
                               └────────────────────────────────────┘
```

Three sources of work:

1. **I/O events** from the kernel via a `Selector` (NIO) or epoll/io_uring
   (native transport). A TCP packet arrived; an HTTP server can advance.
2. **User tasks** scheduled with `vertx.runOnContext { }`, or just the
   continuations Kotlin coroutines resume on this dispatcher.
3. **Timers**, fired when their deadline elapses.

All three drain into one queue per event loop. The OS thread runs that
queue. It never blocks. It never sleeps except in `selector.select(…)`,
and even that wakes up the moment a packet lands.

## 1.2 How many event loops do you have?

Default: `2 × Runtime.getRuntime().availableProcessors()` for general I/O.
We chose to use **exactly N** in `Main.kt` so each loop has a real chance
of being pinned to its own core:

```kotlin
// code/full-app/src/main/kotlin/com/example/app/Main.kt
Vertx.builder()
    .with(
        VertxOptions()
            .setEventLoopPoolSize(Runtime.getRuntime().availableProcessors())
            ...
    )
    .withMetrics(Metrics.factory())
    .build()
```

Why care? Because **once a connection is bound to an event loop, every
event for that connection runs on that loop**. No synchronisation. No
cache-line bouncing between cores. The "thread per core, pinned" pattern
is how you avoid the worst case of L1/L2 cache misses on a hot path.

```
  CPU 0  ─────  EventLoop-0  ─────  HTTP server, connections {c1, c2…}
  CPU 1  ─────  EventLoop-1  ─────  HTTP server, connections {c3, c4…}
  …
```

Vert.x distributes new connections **round-robin** across event loops.
If your traffic is uneven across event loops, the cure is usually more
verticle instances (Chapter 2), not more event loops.

## 1.3 Netty under the cover

When you call `vertx.createHttpServer()`, Vert.x does this approximately:

```
HttpServer
   └── Netty ServerBootstrap
           ├── parentGroup  (1 EventLoop : accepts)
           ├── childGroup   (N EventLoops : connections)
           └── ChannelInitializer:
                  pipeline:
                     HttpServerCodec        ← decodes bytes → HttpRequest
                     HttpObjectAggregator   ← reassemble chunks
                     Http2OrHttp1Handler    ← protocol fork
                     Vert.x HTTP handler    ← surfaces RoutingContext
```

A Netty **Channel** owns a **ChannelPipeline**, which is a doubly-linked
list of **ChannelHandlers**. Inbound bytes pass *up* the pipeline being
decoded; outbound objects pass *down* being encoded.

When the kernel signals "new bytes on FD #17", the event loop:

1. Calls `read()` on FD #17, fills a Netty `ByteBuf`.
2. Walks the inbound pipeline: `ByteBuf` → `HttpRequest` → routing
   handler → your code via `requestHandler`.
3. Your code generates a response (immediately or after `await`).
4. The response object walks back down: `HttpResponse` → `ByteBuf` →
   `write()` on FD #17.

```
┌───────────────────────────────────────────────────────────────┐
│  Inbound  (decode + dispatch)            Outbound (encode)    │
│                                                               │
│  +-----------+   +-------+   +--------+      +--------+       │
│  | TCP read  |─►|  HTTP  |─►| Vert.x  |◄─── | HTTP    |◄── …  │
│  | (NIO)     |  | decode |   | router  |     | encode  |      │
│  +-----------+   +-------+   +--------+      +--------+       │
└───────────────────────────────────────────────────────────────┘
```

You almost never write a `ChannelHandler` directly when using Vert.x.
But you should know they exist, because every "I want to add request
logging / tracing / mTLS / compression" knob is a `ChannelHandler` you
either configure on Vert.x or plug in yourself.

## 1.4 Native transports

On Linux you can ask Netty to skip `java.nio.Selector` and call
`epoll_wait` directly. Less object allocation, fewer system calls per
event, ~10–20 % more throughput on connection-dense services.

```kotlin
Vertx.vertx(
    VertxOptions()
        .setPreferNativeTransport(true)   // already on in Main.kt
)
```

Vert.x will use `EpollEventLoopGroup` on Linux, `KQueueEventLoopGroup`
on macOS/BSD, and fall back to NIO on Windows.

`io_uring` support is available via `Netty 4.2`'s `IOUringEventLoopGroup`.
We do not enable it by default because it requires Linux ≥ 5.6 and we
target Linux ≥ 5.4 in our images. Chapter 17 shows the toggle.

## 1.5 The cardinal sin: blocking the event loop

```kotlin
override suspend fun start() {
    vertx.createHttpServer().requestHandler { req ->
        // DO NOT DO THIS
        val rows = JdbcConnection.executeQuery(...)        // BLOCKING
        req.response().end(rows.toJson())
    }.listen(8080)
}
```

Every connection bound to the same event loop now has to wait for that
JDBC call. Throughput collapses. Vert.x has a built-in detector that
will log a warning if a handler doesn't return within 2 seconds (we set
`setWarningExceptionTime(2_000_000_000L)`). In production we recommend
setting **`setBlockedThreadCheckInterval`** to 100 ms and
**`setMaxEventLoopExecuteTime`** to 100 ms.

What you should do instead:

```kotlin
// (a) Use a non-blocking client – the right answer 90 % of the time
val rows = pool.preparedQuery(SQL).execute(Tuple.of(id)).coAwait()

// (b) If the call must block, push it to a worker thread
val rows = vertx.executeBlocking({ heavyJdbc() }, false).coAwait()

// (c) Or push it to a Virtual Thread executor (we'll wire this in ch 19)
withContext(virtualThreadDispatcher) { heavyJdbc() }
```

## 1.6 How a single coroutine "appears" to the event loop

```
Thread:  vert.x-eventloop-thread-0
─────────────────────────────────────────────────────────────────

  ┌──── coroutine A ────┐   ┌──── coroutine B ────┐
  │ ... do work ...     │   │ ... do work ...     │
  │ pool.execute(SQL)   │   │ pool.execute(SQL)   │
  │ ──────►  await ─────┼─► │                     │
  │   (suspended)       │   │ ──────►  await ─────┼─►
  │                     │   │   (suspended)       │
  └─────────────────────┘   └─────────────────────┘
                            …other tasks…
  ┌──── coroutine A ────┐
  │ result arrives →    │
  │ resume here         │
  │ build response      │
  │ res.end()           │
  └─────────────────────┘
                            ┌──── coroutine B ────┐
                            │ result arrives →    │
                            │ resume here         │
                            │ build response      │
                            │ res.end()           │
                            └─────────────────────┘
```

The event loop never "waited" for the Postgres reply. It ran A until A
suspended, then ran B until B suspended, then drained other tasks, then
resumed each one as the DB sent bytes back. The DB connection is held by
Vert.x internally and writes the *resume callback* into the event loop's
queue when bytes arrive.

## 1.7 Looking under the hood with `jstack`

While the app is running, in another terminal:

```bash
jstack $(pgrep -f full-app)
```

You will see threads named like:

```
"vert.x-eventloop-thread-0"  at sun.nio.ch.SelectorImpl.lockAndDoSelect
"vert.x-eventloop-thread-1"  at sun.nio.ch.SelectorImpl.lockAndDoSelect
...
"vert.x-worker-thread-0"     at java.lang.Object.wait
```

The event-loop threads are *always* parked in `select()` between bursts of
work. The worker threads are idle until `executeBlocking` schedules
something on them.

## 1.8 What you will and will not configure

In Chapter 17 we'll tune:

- `eventLoopPoolSize`
- `workerPoolSize`
- `internalBlockingPoolSize`
- `maxEventLoopExecuteTime`
- `maxWorkerExecuteTime`
- HTTP server-specific: `maxInitialLineLength`, `maxHeaderSize`,
  `idleTimeout`, `acceptBacklog`, TCP `noDelay`, `quickAck`, `tcpFastOpen`.

For now: defaults are sane. Resist the urge to tune until you have data.

## 1.9 Exercises

1. Print `Thread.currentThread().name` inside a HTTP handler. Hit the
   server with 1000 requests using `hey -n 1000 -c 100 …`. How many
   distinct thread names do you see? Why?
2. Put a `Thread.sleep(500)` inside the handler. Re-run the load test.
   Observe the throughput collapse and find the warning log line that
   tells you why.
3. Replace `Thread.sleep(500)` with `kotlinx.coroutines.delay(500)`.
   Re-run the load test. Same latency, restored throughput. Why?

---

[← Chapter 0](00-introduction.md) · [Next → Chapter 2: Verticles & deployment](02-verticles.md)
