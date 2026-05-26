# Chapter 1 — The Event Loop & The Reactor Pattern

> **You should finish this chapter able to:** draw the event loop dispatch
> cycle, explain how Netty selectors connect to Vert.x verticles, and predict
> on which thread any given line of code will run.

This is the most important chapter. Everything else — coroutines, the event
bus, pool sizing, performance tuning — is a consequence of how the event
loop works.

## 1.1  Where the model came from

In 1995 Douglas C. Schmidt wrote a paper called *Reactor: An Object Behavioral
Pattern for Demultiplexing and Dispatching Handles for Synchronous Events*.
The idea: instead of dedicating a thread to each connection, register the
connections with the OS, ask the OS "which of these is ready?", and dispatch
each ready event to its handler from a small fixed pool of threads.

The OS primitives that make this possible are:

| OS | Primitive |
|---|---|
| Linux | `epoll_wait`, `io_uring` |
| BSD/macOS | `kqueue` |
| Windows | I/O Completion Ports (IOCP) |
| Portable Java | `java.nio.channels.Selector` (wraps the above) |

Netty provides Java-idiomatic wrappers (`NioEventLoop`, `EpollEventLoop`,
`KQueueEventLoop`, `IOUringEventLoop`) over each. Vert.x uses these directly.

## 1.2  Single reactor vs multi-reactor

Real servers do not run with one event loop. They run with one event loop
*per CPU core* and balance new connections across them.

```
SINGLE REACTOR (toy)                    MULTI-REACTOR (Vert.x default)
┌──────────────┐                        ┌──────────────┐  ┌──────────────┐
│  Selector    │                        │  Selector-1  │  │  Selector-2  │
│  ┌────────┐  │                        │  ┌────────┐  │  │  ┌────────┐  │
│  │ fd 4   │  │                        │  │ fd 4   │  │  │  │ fd 5   │  │
│  │ fd 5   │  │      ───────►          │  │ fd 6   │  │  │  │ fd 7   │  │
│  │ fd 6   │  │                        │  └────────┘  │  │  └────────┘  │
│  │ fd 7   │  │                        │  loop on T1  │  │  loop on T2  │
│  └────────┘  │                        └──────────────┘  └──────────────┘
│ loop on T1   │                              │                  │
└──────────────┘                              ▼                  ▼
                                         handlers run on    handlers run on
                                         T1 only            T2 only
```

In Vert.x the default number of event loops is `2 × Runtime.getRuntime().
availableProcessors()`. On a 4-core box that is 8 loops. The "boss/worker"
distinction you might know from older Netty docs does not really exist in
Vert.x — accepting new connections and reading from existing ones are both
handled by the same loops, just on different selectors.

> **Why 2× cores?** Empirically a tiny amount of useful work is done while
> waiting for the next selector event, so a small amount of oversubscription
> increases throughput without measurably hurting latency. You can tune this
> down in pinned-core deployments; see chapter 14.

## 1.3  The dispatch cycle

Each event loop runs the same simple loop forever:

```
                  ┌─────────────────────────────────────┐
                  │ 1. submit & process I/O events       │
                  │    (selector.select / epoll_wait)    │
                  └──────────────────┬───────────────────┘
                                     │
                                     ▼
                  ┌─────────────────────────────────────┐
                  │ 2. run scheduled tasks               │
                  │    (timers, runOnContext, futures    │
                  │    completed by other threads)       │
                  └──────────────────┬───────────────────┘
                                     │
                                     ▼
                  ┌─────────────────────────────────────┐
                  │ 3. compute next selector timeout     │
                  │    (nearest timer deadline)          │
                  └──────────────────┬───────────────────┘
                                     │
                                     ▼
                  ┌─────────────────────────────────────┐
                  │ 4. block in select() until I/O or    │
                  │    timeout (the only "block")        │
                  └──────────────────┬───────────────────┘
                                     │
                                     ▼
                              (back to step 1)
```

Step 4 is where the thread actually parks in the kernel via `epoll_wait`.
**Step 4 is the ONLY thing that should ever block an event-loop thread.**

This is why we say "never block the event loop": if your handler in step 1
or 2 sleeps for 50 ms, that 50 ms is 50 ms during which no other handler
on this event loop runs. Every connection assigned to that loop stalls.

## 1.4  What does "handler" really mean?

In Vert.x, "handler" means a callback registered with some async API:

```kotlin
server.requestHandler   { req     -> ... }   // an HTTP request arrived
client.send("ping").onComplete { res -> ... } // a response came back
eventBus.consumer<String>("addr") { msg -> ... }   // an event-bus message
vertx.setTimer(1000)    { id      -> ... }   // a timer fired
```

When the event loop picks up an event from the selector, it walks Netty's
*channel pipeline* (chapter 15 details this), and the last handler in the
pipeline calls *your* handler from step 1 of the cycle above.

## 1.5  The Context — Vert.x's per-handler thread affinity

Every handler in Vert.x is associated with a `io.vertx.core.Context`. A
Context is a lightweight wrapper around "an event loop + some state". Once a
handler is associated with a Context, **all callbacks chained off that
handler run on the same Context's thread**, no matter who completes them.

```
ctx = vertx.currentContext()           // captured when handler ran
db.query(...)                          // executes on whatever thread
    .onComplete { row ->                // resumes on `ctx` → same loop thread
        cache.put(...)
            .onComplete { ... }        // still on `ctx` → same loop thread
    }
```

This is the most subtle and most important property of Vert.x. You get the
performance of multi-threaded I/O *with the programming simplicity of
single-threaded code*. Race conditions between handlers on the same Context
are impossible because they cannot execute concurrently.

> **Practical consequence:** verticles deployed with `instances=N` run on
> N different Contexts. State *within* a verticle instance does not need
> synchronization. State *across* verticle instances does — but the idiomatic
> solution is the event bus (chapter 4), not shared memory.

## 1.6  Where each "thread name" comes from

Run a verticle and look at thread names — they encode role and topology:

| Thread name | Purpose | Block? |
|---|---|---|
| `vert.x-eventloop-thread-N` | One of the event loops. Where almost all your code runs. | **NEVER** |
| `vert.x-worker-thread-N` | Worker pool for `executeBlocking` and worker verticles. | OK to block |
| `vert.x-internal-blocking-N` | Internal pool for DNS, file I/O fallback. | OK to block |
| `vert.x-acceptor-thread-N` | Single thread per HTTP server that calls `accept()`. | OK to block briefly |
| `globalEventExecutor-N-N` | Netty's global timer/scheduler. | Hands-off. |
| `your-app-virtual-XX` | If you opt into virtual-thread verticles (Java 21+). | Conditional — see chapter 5 |

The blocked-thread checker (chapter 5) prints a stack trace whenever an
event-loop thread runs a single task for more than 2 s (default — we lower
this to 100 ms in `Main.kt`).

## 1.7  Following a request, end to end

Take a single HTTP `GET /api/v1/users/123` request to our `full-app`. Here is
exactly what happens — referencing
[`Main.kt`](../code/full-app/src/main/kotlin/com/example/app/Main.kt) and
[`AppVerticle.kt`](../code/full-app/src/main/kotlin/com/example/app/verticles/AppVerticle.kt):

```
0. OS:    SYN, SYN-ACK, ACK to port 8080
1. OS:    HTTP bytes arrive on socket fd 42
2. Linux: epoll_wait wakes vert.x-eventloop-thread-3 with fd 42 readable
3. Netty: NioEventLoop reads bytes into a ByteBuf
4. Netty: HttpServerCodec parses bytes into HttpRequest
5. Vert.x: WebServerRequestImpl wraps it; passes to Router (vertx-web)
6. Router: matches route /api/v1/users/:id; runs the chain of middlewares
7. coroutineRouter: launches a coroutine on the current Context's dispatcher
8. UserService.get(id) suspends on cache.getJson(...)
9. Redis client writes "GET user:123" over TCP fd 51 (also on this event loop)
10. (event loop continues serving OTHER requests while we wait)
11. epoll_wait wakes the same thread with fd 51 readable
12. Redis client parses RESP, completes its Future, which schedules the
    coroutine continuation on the captured Context (our event loop)
13. coroutine resumes: cache miss → suspends on PG SELECT (fd 60)
14. ... event loop keeps working ...
15. PG response arrives, coroutine resumes, builds JSON, writes response
16. Netty flushes bytes to fd 42 → kernel sends them out
```

At no point did our event loop thread sit idle waiting. While we were
"waiting" for Redis at step 10 we were serving other requests. The state of
*our* coroutine was suspended on the heap as a small Java object.

If you internalize that picture, the rest of this series is just details.

## 1.8  Common misconceptions

**"More threads = more throughput."** Past a small multiple of cores, more
threads only adds context-switch overhead. Vert.x's whole point is to use
few threads efficiently.

**"The event loop is single-threaded."** No: there are usually N event loops.
"Single-threaded" applies to *one* event loop's view of *its own* state.

**"`Future.onComplete` runs on the same thread."** Yes — it runs on the
Context that scheduled the operation. That Context is normally the event
loop the call originated on.

**"If I do work in a handler, that's fine because Vert.x is non-blocking."**
Vert.x cannot make CPU-bound code non-blocking. A 50 ms JSON serialization
on the event loop is still 50 ms of latency added to every concurrent
request that loop serves. Move CPU work to the worker pool (chapter 5).

**"I need a thread pool for my Postgres calls."** No — `vertx-pg-client`
is non-blocking. The "pool" you configure is a *connection pool* (logical
DB connections multiplexed over TCP), not a thread pool. Chapter 8.

## 1.9  Try it

1. Add `Thread.sleep(200)` inside your `requestHandler` from chapter 0. Hit
   the endpoint with `ab -n 1000 -c 10 http://localhost:8080/`. What happens
   to p99 latency? Now run with `setEventLoopPoolSize(1)`. Compare.
2. Run `jstack <pid>` on a running Vert.x app. Identify each thread by name
   using the table in 1.6. Which thread is doing the bulk of the work?
3. Read [`AppVerticle.kt`](../code/full-app/src/main/kotlin/com/example/app/verticles/AppVerticle.kt#L29).
   Trace which `Context` the HTTP handler and the gRPC handler run on. Are
   they the same? (Hint: both are deployed by the same verticle.)

[← Ch 0](00-introduction.md) · [Next: Chapter 2 — Verticles & Deployment →](02-verticles.md)
