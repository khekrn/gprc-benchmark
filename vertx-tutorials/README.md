# Vert.x 5 + Kotlin Coroutines: From Beginner to Expert

A hands-on, chapter-by-chapter tutorial for mastering Eclipse Vert.x 5 with Kotlin
coroutines. Each chapter is a self-contained markdown file with diagrams, runnable
code, and line-by-line explanations. The companion Maven project under `code/`
implements every concept end-to-end.

## Why this series

Most Vert.x material online is either ancient (Vert.x 3.x) or skips the parts that
actually matter in production: how the event loop really works under Netty, why one
blocking call destroys throughput, how to wire async PostgreSQL/Redis correctly,
how to size pools, how to log without paying a context-switch tax, how to package
a container-aware JVM image. We cover all of that with practical code.

## Technology stack (pinned)

| Layer | Version | Why this version |
|-------|---------|------------------|
| Java compile target | **21 LTS** | Long-term support, virtual threads GA, container support stable |
| Java runtime (image) | **25** | Latest non-LTS; ZGC generational default, scoped values, structured concurrency preview |
| Kotlin | **2.3.21** | Latest stable, K2 compiler GA, improved coroutines codegen |
| kotlinx.coroutines | **1.10.2** | Compatible with Kotlin 2.3 |
| Vert.x | **5.0.11** | Vert.x 5 GA line: native `Future`, modular gRPC, removed deprecated APIs |
| vertx-grpc | **5.0.11** | Native Vert.x gRPC server/client (not grpc-java compat layer) |
| vertx-pg-client | **5.0.11** | Reactive, non-blocking PostgreSQL driver |
| vertx-redis-client | **5.0.11** | Reactive Redis 7.x client |
| Netty | **4.2.x** | Bundled with Vert.x 5 |
| Protobuf | **4.29.x** | Used by vertx-grpc |
| Build tool | **Maven 3.9.x** | Per your preference |
| Container base | **eclipse-temurin:25-jre-noble** | Container-aware ergonomics |

If you read this in the future, version numbers will have moved. Treat them as a
known-good combination at time of writing.

## Chapter map

The chapters build on each other. If you are new to Vert.x, read in order. If you
are coming from Spring/Netty, you can skim 0-2 and start at chapter 3.

| # | Chapter | Focus |
|---|---------|-------|
| 0 | [Introduction & setup](chapters/00-introduction.md) | Why Vert.x, prerequisites, install everything, first verticle |
| 1 | [Event loop & reactor pattern](chapters/01-event-loop.md) | The single most important chapter. How Netty + Vert.x dispatch I/O. |
| 2 | [Verticles & deployment](chapters/02-verticles.md) | Standard, Worker, Coroutine verticles. Deployment options. |
| 3 | [Kotlin coroutines integration](chapters/03-coroutines.md) | `Dispatchers.Vertx`, `.coAwait()`, `CoroutineVerticle`, structured concurrency. |
| 4 | [Event bus](chapters/04-event-bus.md) | Point-to-point, pub/sub, codecs, clustered mode. |
| 5 | [Avoiding blocking](chapters/05-avoiding-blocking.md) | Blocked-thread checker, `executeBlocking`, virtual threads, async-profiler. |
| 6 | [Logging done right](chapters/06-logging.md) | SLF4J + Logback, JSON logs, MDC across coroutines, async appender. |
| 7 | [REST API with Vert.x Web](chapters/07-rest-api.md) | Router, validation, OpenAPI 3.1, problem+json, content negotiation. |
| 8 | [PostgreSQL with vertx-pg-client](chapters/08-postgresql.md) | Pool config, prepared statements, transactions, pipelining, row mapping. |
| 9 | [Redis integration](chapters/09-redis.md) | Standalone/cluster/sentinel, pipelining, pub/sub, caching patterns. |
| 10 | [gRPC unary service](chapters/10-grpc.md) | Protobuf best practices, Maven codegen, vertx-grpc server + client. |
| 11 | [Configuration](chapters/11-configuration.md) | `vertx-config`, env, file, secret stores, hot reload. |
| 12 | [Testing](chapters/12-testing.md) | `vertx-junit5`, `runTest`, Testcontainers for PG/Redis. |
| 13 | [Docker & JVM tuning](chapters/13-docker-jvm.md) | Multi-stage build, jlink, distroless, container-aware flags, ZGC. |
| 14 | [Performance tuning Vert.x](chapters/14-performance.md) | Event loop sizing, native transports (epoll/io_uring), HTTP tuning. |
| 15 | [Netty deep dive](chapters/15-netty.md) | Channel pipeline, ByteBuf, custom handlers, native transports. |
| 16 | [Build a DynamoDB async driver](chapters/16-dynamodb-driver.md) | Greenfield async driver: Netty bootstrap, SigV4, pool, coroutines. |
| 17 | [Production best practices](chapters/17-production.md) | Metrics, tracing, health checks, graceful shutdown, circuit breakers. |

## Companion code

Everything in `code/` is one Maven multi-module project. After cloning:

```bash
cd code
./mvnw -B verify           # build & test everything
./mvnw -pl full-app exec:java   # run the demo full-app
```

Each chapter points to the specific files it exercises. Files are kept small and
focused so you can `Cmd+Click` from the markdown into the real code.

## How to read this series

1. Read the chapter top-to-bottom.
2. Open the referenced source file alongside it. The code is the source of truth.
3. Run the example.  Each chapter ends with a "Try it" section.
4. Do the exercise.  Each chapter ends with 2-3 stretch exercises.
5. Move on.

## A note on style

We avoid the temptation to "explain everything to everyone." We assume you can
read Kotlin and have written a TCP socket once. We do explain Vert.x-specific
mechanics in detail because that is exactly where most tutorials hand-wave.

Diagrams are ASCII (deliberately) so they render anywhere — terminal, GitHub,
your editor, your PR review. Mermaid is used only where ASCII gets unreadable.

Let's begin: [Chapter 0 — Introduction & setup →](chapters/00-introduction.md)
