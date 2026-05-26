# Chapter 0 — Introduction & Setup

> **You should finish this chapter able to:** explain what Vert.x is and why it
> exists, install every prerequisite, and run a "hello world" verticle locally.

## 0.1  What problem does Vert.x solve?

A traditional Java web app (think Tomcat + Servlet + Spring MVC) uses one thread
per request. If a request hits the database for 50 ms, that thread sits idle
holding stack memory and an OS thread the whole time. To serve 10,000
concurrent requests you need 10,000 threads — which is expensive on memory
(each thread is ~512 KB of stack) and even more expensive on context-switching.

```
TRADITIONAL (thread-per-request)
┌─────────────────────────────────────────────────┐
│  Pool of 200 threads                            │
│  ┌────┐ ┌────┐ ┌────┐ ┌────┐ ... ┌────┐         │
│  │ T1 │ │ T2 │ │ T3 │ │ T4 │     │T200│         │
│  └─┬──┘ └─┬──┘ └─┬──┘ └─┬──┘     └─┬──┘         │
│    │      │      │      │          │            │
│    DB     DB     DB     DB         DB           │  ← each thread parks
│  (50ms) (50ms) (50ms) (50ms)     (50ms)         │    on a syscall
└─────────────────────────────────────────────────┘
 Beyond 200 concurrent connections: queue and wait.
```

Vert.x replaces "one thread per request" with **one thread per CPU core, never
blocked**. Each thread runs an *event loop* that pulls events (incoming HTTP
bytes, DB response bytes, timer firings) from a queue and dispatches them to
handlers. A handler must not block — instead it kicks off another async
operation and registers a callback (or in our case, *suspends* via a Kotlin
coroutine).

```
VERT.X (event-loop / reactor)
┌─────────────────────────────────────────────────┐
│  N event loops  (N ≈ CPU cores)                 │
│  ┌─────────────────────────────────────────┐    │
│  │ EventLoop-1                             │    │
│  │  ┌─────────┐                            │    │
│  │  │ Queue   │ ──► dispatch ──► handler ──┼──► async I/O (return immediately)
│  │  └─────────┘                            │    │
│  └─────────────────────────────────────────┘    │
│  10,000 connections ÷ N loops = no per-conn thread cost
└─────────────────────────────────────────────────┘
```

The cost of "10,000 concurrent connections" in Vert.x is just 10,000 small
state machines living in heap memory plus an OS file descriptor each.

## 0.2  Where does Vert.x sit?

```
   Your Kotlin code
   ───────────────────
   Vert.x APIs (HTTP, gRPC, DB, etc.)
   ───────────────────
   Vert.x core: event loop, future, event bus
   ───────────────────
   Netty: NIO selectors, ByteBuf, channel pipelines
   ───────────────────
   JVM I/O: NIO Selector, epoll/kqueue/io_uring
   ───────────────────
   OS kernel
```

**Netty** is the low-level non-blocking I/O library that powers gRPC, Akka,
Spring WebFlux, Elasticsearch, Cassandra, and many others. **Vert.x** is a
thin, opinionated runtime on top of Netty that gives you a coherent
programming model: verticles, the event bus, and a uniform `Future<T>` async
type. Chapter 15 takes the Netty cover off so you can see what's underneath.

## 0.3  Vert.x vs Spring (one-line cheat sheet)

| | Spring MVC | Spring WebFlux | **Vert.x** |
|---|---|---|---|
| Threading model | thread-per-request | reactor (Project Reactor + Netty) | **reactor (Vert.x + Netty)** |
| Async type | none / `CompletableFuture` | `Mono<T> / Flux<T>` | **`Future<T>` + suspending** |
| Opinion level | very high (DI, AOP, magic) | medium | **low — bring your own everything** |
| Startup time | slow | medium | **fast (~200 ms)** |
| Memory footprint | large | medium | **small** |
| Best fit | CRUD apps, batch | reactive streams pipelines | **low-latency APIs, gateways, streaming** |

Vert.x feels like a *toolkit*, not a framework. There is no annotation magic.
Code reads top-to-bottom.

## 0.4  Prerequisites

You need four things on your machine:

1. **JDK 25** — get it from [adoptium.net](https://adoptium.net/). On macOS:
   ```bash
   brew install --cask temurin@25
   /usr/libexec/java_home -V        # verify
   export JAVA_HOME=$(/usr/libexec/java_home -v 25)
   ```
   We compile to Java 21 bytecode but *run* on Java 25 in production so we
   pick up the generational ZGC default and structured concurrency.
2. **Kotlin 2.3.x** — already shipped via the `kotlin-maven-plugin` we
   declare in our `pom.xml`, no separate install needed.
3. **Maven 3.9.x** — `brew install maven`. (We use the Maven Wrapper `mvnw`
   so even this is optional once you clone.)
4. **Docker** for Postgres + Redis. `brew install --cask docker`.

Verify everything:

```bash
java -version          # should show 25
mvn -v                 # should show 3.9.x with JDK 25
docker version
```

## 0.5  Project layout

The companion code under `code/` is laid out as a Maven multi-module project:

```
code/
├── pom.xml                       parent: dependency-management + plugins
└── full-app/                     the only module (we keep things simple)
    ├── pom.xml                   module pom: deps + protobuf plugin + shade
    ├── Dockerfile                multi-stage image, JDK 25 runtime
    └── src/main/
        ├── kotlin/com/example/app/
        │   ├── Main.kt
        │   ├── verticles/AppVerticle.kt
        │   ├── http/HttpServerFactory.kt
        │   ├── grpc/UserGrpcService.kt
        │   ├── db/UserRepository.kt
        │   ├── cache/RedisCache.kt
        │   ├── config/AppConfig.kt
        │   ├── domain/UserService.kt
        │   └── observability/{HealthEndpoints, AppShutdown}.kt
        ├── proto/user.proto
        └── resources/{logback.xml, config/application.yaml}
```

We deliberately keep everything in *one* module. You will not be hunting
across `domain`, `infrastructure`, `application` modules to follow a request.
For production, splitting is fine — the lessons here transfer cleanly.

## 0.6  Hello, verticle

The smallest possible working program. Save this as
`code/full-app/src/main/kotlin/com/example/app/HelloVerticle.kt`:

```kotlin
package com.example.app

import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.CoroutineVerticle
import io.vertx.kotlin.coroutines.coAwait
import kotlinx.coroutines.runBlocking

class HelloVerticle : CoroutineVerticle() {                          // 1
    override suspend fun start() {                                    // 2
        vertx.createHttpServer()                                      // 3
            .requestHandler { req -> req.response().end("hello\n") }  // 4
            .listen(8080)                                             // 5
            .coAwait()                                                // 6
        println("listening on 8080 (event loop: ${Thread.currentThread().name})")
    }
}

fun main(): Unit = runBlocking {                                      // 7
    val vertx = Vertx.vertx()                                         // 8
    vertx.deployVerticle(HelloVerticle()).coAwait()                   // 9
}
```

**Line by line:**

1. `CoroutineVerticle` is the Kotlin-flavored base class. It exposes
   `vertx`, gives you a `CoroutineScope` tied to the verticle lifecycle,
   and lets you declare `suspend` lifecycle methods.
2. `start()` is called *once* when the verticle is deployed. We mark it
   `suspend` so we can `coAwait` inside it.
3. `createHttpServer()` returns an `HttpServer` object. No socket has been
   opened yet — only configuration is being built.
4. `requestHandler` registers a function called for every HTTP request.
   Since the function is a closure, it captures nothing here, but be careful
   not to capture mutable state — the handler runs on the event loop.
5. `listen(8080)` returns a `Future<HttpServer>`. The *act of binding* is
   async; in production you might bind to many ports across many event loops.
6. `coAwait()` is the magic. It suspends the coroutine, releases the event
   loop thread back to do other work, and resumes the coroutine when the
   `Future` completes. **This is the single most important pattern in the
   series.**
7. In `main()` we use `runBlocking` *only* because we need a coroutine
   context to drive the suspending `coAwait()`. Never use `runBlocking`
   inside a verticle — it would block the event loop.
8. `Vertx.vertx()` boots the runtime: it spins up event-loop threads
   (default: 2 × CPU cores), starts the timer wheel, opens the
   internal blocked-thread checker.
9. `deployVerticle()` schedules a `start()` call on one of the event loops.

Run it from `code/`:

```bash
./mvnw -q -pl full-app exec:java -Dexec.mainClass=com.example.app.HelloVerticleKt
curl http://localhost:8080      # → hello
```

You will see `event loop: vert.x-eventloop-thread-0` (or `-1`, `-2`, ...).
Every concurrent request handled by this verticle runs on **the same event
loop thread**. That is not a limitation — it is the secret to predictable
latency, because there is no synchronization overhead and no race conditions
between requests handled by the same verticle instance.

## 0.7  What we will NOT do in this series

- **No annotation-driven DI.** We pass dependencies as constructor args. If
  you want Koin or Guice later, fine — but you will see in chapter 2 that you
  do not need it.
- **No reactive-stream operators (`map`, `flatMap`, `zip` chains).** Kotlin
  coroutines give you sequential code; that's the entire point. We will
  show one `Future`-style example in chapter 3 to highlight the difference.
- **No "framework-mode" Vert.x.** Vert.x has a CLI launcher and a Maven
  plugin. They are fine, but they hide too much for a learning series.
  We start `Vertx.vertx()` ourselves.

## 0.8  Try it

1. Clone or `cd` into `code/`, run `./mvnw -B verify`. Even if you do not
   read further today, you want a green build before chapter 1.
2. Change the `requestHandler` to delay 500 ms with `vertx.setTimer(500) {
   req.response().end("delayed\n") }` and verify your terminal can still
   accept other requests during the delay. (Hint: open two terminals.)
3. Increase the number of event-loop threads with
   `Vertx.vertx(VertxOptions().setEventLoopPoolSize(1))` and observe what
   happens to per-thread distribution. We will explore why in chapter 1.

[← Index](../README.md) · [Next: Chapter 1 — Event loop & reactor pattern →](01-event-loop.md)
