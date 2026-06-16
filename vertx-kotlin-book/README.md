# Vert.x 5 + Kotlin Coroutines + Non-blocking PostgreSQL

A practical, build-it-as-you-read book on Eclipse Vert.x 5 with Kotlin
coroutines, a non-blocking PostgreSQL driver, and production-grade REST + gRPC
services. Every chapter ends with code you can run and most chapters end
with two or three short exercises.

This book is the result of dissatisfaction with the previous tutorial in
`vertx-tutorials/`. The chapters here go deeper into the internals (event
loop, Netty, coroutine continuations), build the same demo app end-to-end
across all chapters, and cover every gRPC streaming style — unary,
server streaming, client streaming, bidirectional.

## Who is this for

- You can read Kotlin and have written a small backend service before.
- You have heard of "the reactor pattern" but want to *understand* it, not
  just use it.
- You are building a service that has to be fast under real traffic — not
  a hobby project that talks to a JSON file.

## What you will build

A single Maven module, `code/full-app`, that grows chapter by chapter into
a production-style **users service**:

- **REST API** over Vert.x Web with NDJSON streaming.
- **PostgreSQL** access through `vertx-pg-client`: pipelining, prepared
  statements, transactions, server-side cursors, `LISTEN/NOTIFY`.
- **gRPC** server (`vertx-grpc`) exposing the same domain via unary, server
  streaming, client streaming, and bidirectional streaming RPCs.
- **Observability**: SLF4J + MDC propagated across coroutines, Micrometer
  metrics, health checks, structured shutdown.
- **Tests**: JUnit 5 + Testcontainers + `runTest`.

## Stack (pinned)

| Layer                | Version                       | Why this version                                                 |
|----------------------|-------------------------------|------------------------------------------------------------------|
| JDK (compile + run)  | **25 LTS**                    | Generational ZGC by default, stable virtual threads, scoped values |
| Kotlin               | **2.3.21**                    | K2 GA, latest compiler                                           |
| kotlinx.coroutines   | **1.10.2**                    | Compatible with Kotlin 2.3                                       |
| Vert.x core          | **5.0.11**                    | Vert.x 5 GA: typed `Future`, modular gRPC                        |
| vertx-pg-client      | **5.0.11**                    | Reactive, non-blocking PostgreSQL                                |
| vertx-grpc-server    | **5.0.11**                    | Native Vert.x gRPC, not a grpc-java compat layer                 |
| Netty                | **4.2.0.Final**               | Bundled with Vert.x 5                                            |
| Protobuf             | **4.29.3**                    | Used by `vertx-grpc-protoc-plugin2`                              |
| Build                | **Maven 3.9.x**               | Multi-module ready                                               |
| Container base       | **eclipse-temurin:25-jre-noble** | Container-aware                                              |

## Reading order

The chapters are designed to be read in order. Each one introduces a piece
of the demo app, motivates *why* it exists, then drops you into the
relevant `code/full-app/src/...` file.

### Part 1 — Foundations
| # | Chapter |
|---|---------|
| 00 | [Introduction & why Vert.x in the virtual thread era](chapters/00-introduction.md) |
| 01 | [The event loop & Netty internals](chapters/01-event-loop.md) |
| 02 | [Verticles & deployment models](chapters/02-verticles.md) |
| 03 | [Futures, Promises, and the Vert.x async type](chapters/03-futures.md) |

### Part 2 — Kotlin coroutines, deep
| # | Chapter |
|---|---------|
| 04 | [Coroutines internals: continuations & state machines](chapters/04-coroutines-internals.md) |
| 05 | [Coroutines + Vert.x: killing the callback](chapters/05-coroutines-vertx.md) |
| 06 | [Structured concurrency, channels, flows](chapters/06-structured-concurrency.md) |

### Part 3 — REST & PostgreSQL basics
| # | Chapter |
|---|---------|
| 07 | [Config, structured logging, MDC across coroutines](chapters/07-config-logging.md) |
| 08 | [REST API with Vert.x Web + coroutines](chapters/08-rest-api.md) |
| 09 | [PostgreSQL with vertx-pg-client — pool, queries, mapping](chapters/09-postgresql-basics.md) |

### Part 4 — Advanced PostgreSQL
| # | Chapter |
|---|---------|
| 10 | [Transactions, row streaming, LISTEN/NOTIFY, pipelining](chapters/10-postgresql-advanced.md) |
| 11 | [Repository patterns, migrations, domain modelling](chapters/11-repository-patterns.md) |

### Part 5 — gRPC
| # | Chapter |
|---|---------|
| 12 | [gRPC fundamentals & unary RPC](chapters/12-grpc-unary.md) |
| 13 | [Server-streaming RPC](chapters/13-grpc-server-streaming.md) |
| 14 | [Client-streaming & bidirectional streaming](chapters/14-grpc-bidi.md) |
| 15 | [Interceptors, deadlines, cancellation, status codes](chapters/15-grpc-interceptors.md) |

### Part 6 — Production
| # | Chapter |
|---|---------|
| 16 | [Observability: metrics, tracing, health](chapters/16-observability.md) |
| 17 | [Performance & tuning for high traffic](chapters/17-performance.md) |
| 18 | [Testing strategies](chapters/18-testing.md) |
| 19 | [Vert.x vs Virtual Threads: when does the reactive stack still win?](chapters/19-vertx-vs-virtual-threads.md) |

## Running the code

```bash
cd code
docker compose up -d postgres        # local Postgres 17
mvn -B verify                        # build & test all modules
mvn -pl full-app exec:java           # run the demo full-app
```

REST test:

```bash
curl -s -XPOST localhost:8080/api/users \
  -H 'content-type: application/json' \
  -d '{"email":"a@x.io","fullName":"Alice"}'
curl -sN localhost:8080/api/users          # NDJSON stream
```

gRPC test (grpcurl is the easiest client):

```bash
grpcurl -plaintext -d '{"id":1}' localhost:9090 com.example.app.grpc.Users/GetUser
grpcurl -plaintext -d '{}'      localhost:9090 com.example.app.grpc.Users/ListUsers
```

## Conventions

- ASCII diagrams in line. Mermaid only where ASCII gets unreadable.
- Source is the source of truth. Every chapter cross-links to the file.
- Comments in code are sparse, on purpose. The book is the long-form
  documentation; the code is the short-form.
- We avoid annotation-driven DI. Dependencies are passed as constructor
  arguments. You can read every wire from `Main.kt` downwards.

[Start with Chapter 0 →](chapters/00-introduction.md)
