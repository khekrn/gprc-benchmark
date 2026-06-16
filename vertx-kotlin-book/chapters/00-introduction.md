# Chapter 0 — Introduction & why Vert.x in the virtual-thread era

> By the end of this chapter you will be able to explain (a) what problem
> Vert.x solves, (b) where Kotlin coroutines fit in, and (c) why a reactive
> stack still earns its place in 2026 even though virtual threads are GA.
> You will also have the demo app building locally.

## 0.1 The cost of "one thread per request"

Pick the standard servlet-style Java web app — Tomcat + Spring MVC + JDBC.
A request comes in, an HTTP connector hands it to a thread, the thread
walks the controller, hits the database, waits 30 ms for the network
round-trip, gets a row back, formats JSON, writes the response, returns
to the pool.

```
  Inbound:  ─────►   ┌────────────────┐
                     │  Thread T_42   │  ← holds 512 KB of stack
                     └───────┬────────┘
                             │ JDBC call
                             ▼
                     ┌────────────────┐
                     │  Kernel: park  │  ← T_42 idle but pinned to a request
                     └───────┬────────┘
                             │ network 30 ms
                             ▼
                     ... eventually returns
```

For 30 ms the OS thread is parked. The Java heap still owns its stack,
the OS still owns its TCB, the scheduler still has to consider it. If 200
concurrent requests are in flight you need 200 threads; 10,000 concurrent
requests means 10,000 threads or a backlog. Each context switch costs
~1 μs of CPU plus cache pollution.

## 0.2 The reactor pattern in one diagram

Vert.x flips the model. You get **N event-loop threads** (default `2 × CPU
cores`). Each loop owns a **queue** of "things to do" and runs them in a
tight loop. Nothing ever blocks. A handler that needs to wait registers a
callback (or in our case, `suspend`s a coroutine) and the loop moves on.

```
┌────────────────────────────────────────────────────────────────┐
│           one OS thread per event loop                         │
│                                                                │
│   ┌─────────────────────────────┐                              │
│   │  EventLoop-0                │                              │
│   │     queue   │  T₀  T₁  T₂…  │  ← user tasks + I/O events   │
│   │             ▼               │                              │
│   │  while (running) {          │                              │
│   │    run pending tasks        │                              │
│   │    select() on Netty FDs    │  ← epoll/kqueue/io_uring     │
│   │    dispatch I/O events      │                              │
│   │  }                          │                              │
│   └─────────────────────────────┘                              │
│                                                                │
│   10,000 sockets → handful of file descriptors per loop        │
│   memory cost per connection ≈ small heap state, not a thread  │
└────────────────────────────────────────────────────────────────┘
```

The OS thread never parks. It just keeps draining the queue.

**Where do coroutines fit?** They make a handler "look" sequential
without blocking the loop. Inside a `suspend` function, every `await`
saves the current stack-frame to a tiny continuation object and returns
the OS thread to the queue. When the awaited result arrives, the
runtime re-schedules the continuation on a (likely the same) event-loop
thread, restores the frame, and we keep going.

You get the **readability of synchronous code** with the **scheduling cost
of an event loop**. That is the entire pitch.

## 0.3 But… virtual threads?

This question matters in 2026. Project Loom shipped GA virtual threads in
Java 21 and Java 25 added scoped values + structured concurrency. If a
virtual thread is "cheap" (a few hundred bytes), why not write the easy
blocking code and call it a day?

There are three honest answers.

**(1) Virtual threads still need non-blocking I/O underneath.** A
virtual thread that does `socket.read()` parks on a JDK-level
continuation, and the carrier thread is freed — but only if the I/O
library is non-blocking aware. The JDK socket APIs are. Most legacy JDBC
drivers are not (PG JDBC is, finally; many drivers still aren't).
Vert.x and the reactive Postgres driver were *built* on top of NIO from
day one. There is no monkey-patching.

**(2) Blocking semantics carry hidden coupling.** Each parked virtual
thread holds *its entire call stack*, including objects pinned by stack
references. That makes GC pauses more painful at very high concurrency.
A coroutine continuation only holds the variables the suspending
function actually needs. For 1M concurrent state machines, the latter
wins on memory.

**(3) Backpressure and streams.** Virtual threads have no built-in
notion of backpressure. If consumer Y is slower than producer X, X has
to use a queue, and now you are writing flow-control code yourself.
Coroutine Flows and Vert.x ReadStreams give you backpressure for free.
gRPC server streaming is a textbook case: you want the slow client to
slow the DB cursor, end-to-end.

**Then when *should* you reach for virtual threads instead?** If your
service is a thin REST adapter over slow blocking libraries (legacy
JDBC, JNDI, "enterprise" SDKs), virtual threads will give you 80 % of
Vert.x's scalability with 20 % of the learning curve. We discuss the
trade-off in detail in **Chapter 19**. The summary is: virtual threads
*compete* with Vert.x on simple proxy-style workloads. They *complement*
Vert.x on streaming, gRPC, and low-latency APIs.

## 0.4 Where Vert.x sits in the stack

```
                 Your Kotlin code
                 ──────────────────
   Vert.x APIs:  HttpServer • Router • PgPool • GrpcServer • EventBus
                 ──────────────────
   Vert.x core:  EventLoop  Future  Verticle  ContextLocal  Workers
                 ──────────────────
   Netty:        Channel  Pipeline  ByteBuf  NIO/Epoll/IoUring
                 ──────────────────
   JVM I/O:      java.nio Selector  •  jdk.internal.net.epoll  •  …
                 ──────────────────
   OS:           epoll • kqueue • io_uring
```

Vert.x is a **thin opinionated runtime** on top of Netty. Netty is the
low-level non-blocking I/O library used inside gRPC, Akka, Cassandra,
Elasticsearch and just about every JVM project that talks fast TCP. You
do not need to know Netty to use Vert.x, but **Chapter 1** dips into the
Netty channel pipeline because that is where the magic happens.

## 0.5 What about Spring WebFlux?

| Property        | Spring MVC          | Spring WebFlux                  | **Vert.x**                          |
|-----------------|---------------------|---------------------------------|-------------------------------------|
| Threading       | one thread / request| Reactor (Netty + Project Reactor) | Reactor (Netty + Vert.x core)    |
| Async type      | `CompletableFuture` | `Mono<T>` / `Flux<T>`           | `Future<T>` + `suspend` / `Flow`    |
| Opinion         | very high           | medium                          | low — bring your own everything     |
| Startup time    | ~3 s                | ~1 s                            | ~200 ms                             |
| Memory          | large               | medium                          | small                               |
| DI              | Spring container    | Spring container                | constructor injection by hand       |
| Best fit        | CRUD apps           | reactive pipelines              | low-latency APIs, gateways, streams |

If you came from WebFlux: you already understand the *idea* (event loop,
non-blocking) but a different *API* (`Flux` vs `Future` + `Flow`).
WebFlux requires you to think in operators (`map`, `flatMap`, `zip`).
Vert.x + Kotlin lets you write straight-line `suspend` code instead. We
will rarely use Reactive Streams operators in this book.

## 0.6 Prerequisites

- **JDK 25** — `brew install --cask temurin@25`, then
  `export JAVA_HOME=$(/usr/libexec/java_home -v 25)`.
- **Maven 3.9+** — `brew install maven`. (This repo has no Maven wrapper;
  use the `mvn` on your PATH.)
- **Docker** — for the local Postgres instance.
- **grpcurl** — `brew install grpcurl`, used in the gRPC chapters.

Sanity check:

```bash
java -version          # 25.x
mvn -v                 # 3.9.x, JDK 25
docker version
```

## 0.7 The demo app in one tree

```
code/
├── pom.xml                           ← parent, dep management, plugin pins
├── docker-compose.yml                ← local Postgres 17
└── full-app/
    ├── pom.xml                       ← module: deps + protoc plugin + shade
    ├── Dockerfile                    ← Temurin 25 JRE
    └── src/main/
        ├── kotlin/com/example/app/
        │   ├── Main.kt
        │   ├── verticles/AppVerticle.kt
        │   ├── http/Routes.kt
        │   ├── grpc/UserGrpcService.kt
        │   ├── db/{DbModule,DbMigrator,UserRepository}.kt
        │   ├── domain/{User,UserService}.kt
        │   ├── config/AppConfig.kt
        │   ├── coroutines/CoroutineExtensions.kt
        │   └── observability/{MdcSupport,Metrics,AppShutdown}.kt
        ├── proto/users.proto
        └── resources/
            ├── config/application.yaml
            ├── db/migration/V1__schema.sql
            └── logback.xml
```

Everything is one module on purpose. You will not be hunting across
`domain/`, `application/`, `infrastructure/` modules to follow a request.
Splitting later, if you want a hexagonal layout, is mechanical.

## 0.8 Build, run, smoke-test

From `code/`:

```bash
docker compose up -d postgres
mvn -B verify                          # build & test
mvn -pl full-app exec:java             # run (uses Main.kt as entry)
```

In a second terminal:

```bash
curl -sX POST localhost:8080/api/users \
  -H 'content-type: application/json' \
  -d '{"email":"a@x.io","fullName":"Alice"}' | jq

curl -s localhost:8080/api/users/1 | jq
```

The first request creates a row in the local Postgres; the second reads
it back through the non-blocking `vertx-pg-client`. The entire round-trip
ran on one event-loop thread. We will dissect what that means in
Chapter 1.

## 0.9 Exercises

1. Open `code/full-app/src/main/kotlin/com/example/app/Main.kt`. Trace
   each line into the file it touches. You should be able to draw a
   one-page block diagram of the app.
2. In `AppVerticle.start()`, swap the verticle deployment to
   `DeploymentOptions().setInstances(4)`. Observe how many event-loop
   threads handle traffic. (Hint: `jstack`.) We explain why this
   matters in Chapter 2.
3. Write down two functions in the codebase that *could* block. They
   are not currently — but if you replaced something, where would the
   trap be?

---

[Next → Chapter 1: The event loop & Netty internals](01-event-loop.md)
