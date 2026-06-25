# grpc-pg-bench — Claude context

Single-purpose repo: benchmark **seven gRPC + Postgres stacks** on the
gRPC-unmarshal → tiny CPU touch → Postgres write/read path, to decide a stack
for a next-gen workflow engine. Target hardware: **2 cores / 4 GB RAM, Ubuntu
Linux**. All seven sit behind the same `bench.v1.CommandService`, the same SQL,
and the same Go loadgen:

- **go-pgx** — Go + grpc-go + jackc/pgx (baseline).
- **rust-tokio** — Rust + tonic + tokio-postgres + deadpool (async, native).
- **kotlin-vertx** — Kotlin coroutines + Vert.x 5 gRPC + vertx-pg-client (reactive).
- **spring-vt** — Spring Boot 4.1 gRPC + virtual threads + HikariCP JDBC (Spring `JdbcClient`).
- **spring-rt** — Spring Boot 4.1 gRPC + Kotlin coroutines + **Spring Data R2DBC** (reactive; epoll + direct executor + compact headers).
- **spring-kt-vt** — Spring Boot 4.1 gRPC + Kotlin + virtual threads + **JetBrains Exposed DSL** over HikariCP JDBC (tuned for c=128).
- **spring-data-jdbc** — Spring Boot 4.1 gRPC + virtual threads + **Spring Data JDBC** repositories over HikariCP (the Java twin of spring-vt; only the data layer differs).

The three `-vt` stacks use blocking JDBC on virtual threads; spring-kt-vt is the
Kotlin twin of the Java spring-vt (same Loom model, not coroutines) but drives
the **Exposed DSL** instead of hand-written SQL — so it doubles as the
"Exposed framework cost on virtual threads" data point. The `-rt` reactive stacks
use async drivers (vertx-pg-client for kotlin-vertx, R2DBC for spring-rt); go-pgx
uses goroutines + pgx; rust-tokio uses tokio tasks + tokio-postgres. The spread
isolates concurrency-model + driver, not framework sugar.

The full architecture, fairness decisions, and result interpretation live in
`README.md`. This file is for things that aren't obvious from the code and
that future-you will need to remember when running on Linux.

## Layout (one-line each)

```
proto/command.proto            # shared contract (Execute / ExecuteTx / GetState)
sql/schema.sql                 # commands + workflow_state + outbox tables
go-pgx/main.go                 # Go server (graceful stop, keepalive, health)
rust-tokio/                    # Cargo — Rust/tonic + tokio-postgres + deadpool (edition 2024)
  src/                              # main/config/proto/fnv/db/service/server (one concern per file)
kotlin-vertx/                  # Maven — Kotlin/Vert.x reactive (Java 25, Kotlin 2.2.x)
  src/main/kotlin/com/beam/bench/   # Main/MainVerticle/GrpcVerticle/CommandServiceImpl/Db/Config/Fnv
spring-vt/                     # Maven — Spring Boot 4.1 gRPC + virtual threads + HikariCP JDBC + co-hosted Jetty REST /health
  src/main/java/com/beam/bench/     # CommandServiceImpl (StreamObserver), Db (JDBC), GrpcServerConfig (VT executor), HealthController (REST), Fnv
spring-rt/                     # Maven — Spring Boot 4.1 gRPC + Kotlin coroutines + Spring Data R2DBC (reactive; epoll + direct executor)
  src/main/kotlin/com/beam/bench/   # CommandServiceImpl (CoroutineImplBase suspend), Db (@Component, CoroutineCrudRepositories), Command/WorkflowState/OutboxEvent (@Table) + Repositories (@Modifying upsert), GrpcServerConfig (epoll + directExecutor, NO VT executor), Fnv, SpringRtApplication
spring-kt-vt/                   # Maven — Spring Boot 4.1 gRPC + Kotlin + virtual threads + Exposed DSL/JDBC (tuned for c=128)
  src/main/kotlin/com/beam/bench/   # CommandServiceImpl (Java ImplBase/StreamObserver from Kotlin), Db (@Transactional Exposed DSL), GrpcServerConfig (VT executor + Netty tuning), Fnv, SpringKtVtApplication (@ImportAutoConfiguration ExposedAutoConfiguration)
spring-data-jdbc/              # Maven — Spring Boot 4.1 gRPC + virtual threads + Spring Data JDBC repositories over HikariCP
  src/main/java/com/beam/bench/     # CommandServiceImpl (StreamObserver, copied from spring-vt), Db (@Component, repos), Command/WorkflowState/OutboxEvent (@Table records), CommandRepository/WorkflowStateRepository (@Modifying upsert)/OutboxRepository, GrpcServerConfig (copied), Fnv, SpringDataJdbcApplication
  (each JVM module has src/main/proto/command.proto + application.properties/logback)
loadgen/main.go                # closed-loop gRPC driver: execute|exectx|read|mixed
scripts/
  config.sh                    # all env knobs + per-stack ports live here
  setup_db.sh                  # creates role `bench` + db `bench` + schema
  build_go.sh                  # protoc + go build (writes bin/go-server, bin/loadgen)
  build_rust.sh                # cargo build --release -> bin/rust-server (needs protoc)
  build_kotlin.sh              # mvn package -> bin/kotlin-vertx-bench.jar
  build_spring_vt.sh           # mvn package -> bin/spring-vt-bench.jar
  build_spring_rt.sh           # mvn package -> bin/spring-rt-bench.jar
  build_spring_kt_vt.sh        # mvn package -> bin/spring-kt-vt-bench.jar
  build_spring_data_jdbc.sh    # mvn package -> bin/spring-data-jdbc-bench.jar
  run_*_server.sh              # standalone server runners
  run_benchmark.sh             # orchestrator (one server at a time)
results/<ts>/                  # per-run JSON + summary.csv + environment.txt
```

## Prerequisites (Ubuntu)

```bash
# Java 25 — Corretto or Temurin, both work
# Example (Corretto via Amazon's repo, or sdkman):
sdk install java 25.0.1-amzn
sdk use java 25.0.1-amzn

# Go 1.23+
sudo apt install -y golang-go || (download from go.dev/dl)
# protoc (the Go build script uses it; the Kotlin build downloads its own):
sudo apt install -y protobuf-compiler

# Postgres 14+ — local install is best for this test
sudo apt install -y postgresql postgresql-client

# Maven 3.9+
sudo apt install -y maven

# taskset (in util-linux, already on every Ubuntu install) — UNLIKE macOS, this
# actually pins to cores on Linux. The orchestrator detects it automatically.
which taskset
```

## Hard version requirements (why)

- **JDK 25 runtime for the whole suite.** kotlin-vertx compiles to release 25
  (Kotlin 2.2.x's tooling is the first that parses the Java 25 version string;
  `build_kotlin.sh` hard-fails below 25). The Spring modules target
  bytecode 21 but run fine on 25 — so set JDK 25 active (`sdk use java
  25.0.3-amzn`, now the sdkman default here) and *all* JVM stacks run on one
  runtime. Maven itself must run under 25 or kotlin-vertx fails with
  `error: release version 25 not supported`.
- **Kotlin 2.2.21** (pinned in `kotlin-vertx/pom.xml`). 2.1.x can't read JDK 25.
- **Vert.x 5.1.1** (bumped from 5.0.5). `vertxFuture` still takes `scope` as a
  *regular* parameter (`vertxFuture(scope) { … }`, not `scope.vertxFuture { … }`)
  — the signature did NOT change across 5.0→5.1, so no call-site edits were
  needed. If you upgrade further, re-verify `VertxCoroutineKt.vertxFuture`'s
  metadata (it was an extension receiver in 4.x). On 5.1.1 you'll see benign
  `IllegalStateException: stream is failed: CANCELLED` ERROR logs at phase end
  (client closing in-flight streams) — 0 actual RPC errors; harmless noise.
- **Spring Boot 4.1.0** (spring-vt). Uses the *native* Boot gRPC starter
  `spring-boot-starter-grpc-server` (Boot 4.1 took ownership from the spring-grpc
  project; Spring gRPC 1.1.0, grpc-java 1.80.0). The `spring-boot-starter-parent`
  pre-configures `io.github.ascopes:protobuf-maven-plugin`, so the pom needs only
  `<goal>generate</goal>` with no `<configuration>` (matches the official
  `samples/grpc-server` pom). grpc-java 1.80.0 / protobuf-java 4.34.2 are managed
  by the Boot BOM (properties `${grpc-java.version}` / `${protobuf-java.version}`).
- **spring-rt** (Spring Boot 4.1 + Kotlin coroutines + **Spring Data R2DBC**,
  port **50058**). Fully reactive/non-blocking. Coroutine gRPC stubs via
  grpc-kotlin (`grpc-kotlin.version` 1.5.0, Boot-managed); the ascopes plugin runs
  `protoc-gen-grpc-kotlin` as a **`jvm-maven`** plugin (classifier `jdk8`, type
  `jar`) alongside the `binary-maven` `protoc-gen-grpc-java` — mirrors the official
  `samples/grpc-server-kotlin` pom. Service extends
  `CommandServiceGrpcKt.CommandServiceCoroutineImplBase` (`suspend fun`s). **Data
  layer is Spring Data R2DBC** (was raw `DatabaseClient`): `Command`/`WorkflowState`
  /`OutboxEvent` `@Table` data classes, `CoroutineCrudRepository`s (suspend — fully
  non-blocking), a `@Modifying @Query` reactive UPSERT (`save()` can't UPSERT), and
  a `@Transactional` suspend `executeTx` (Boot's reactive `R2dbcTransactionManager`
  backs `@Transactional` on suspend funs). `r2dbc-postgresql` driver, R2DBC pool.
  Like spring-data-jdbc, `save()` is transactional → the `execute` insert is a
  3-round-trip transaction. **Tuned with spring-vt's playbook ADAPTED for reactive**
  (new `GrpcServerConfig`): epoll + a **direct executor** — NOT the VT executor
  spring-vt uses; reactive handlers must stay on the event loop, a VT-per-task
  executor would add a pointless hop — plus 1 MiB windows/TCP_NODELAY/keepalive,
  I/O threads = cores/2. `GRPC_TUNED=off` env bypasses the customizer (same-build
  A/B). JVM gets `SPRING_RT_JVM_OPTS` (shared opts + `-XX:+UseCompactObjectHeaders`,
  JEP 519 — spring-rt-only, so cross-stack comparisons carry that caveat). Tuning =
  **+28%** over un-tuned (~3,757 → ~4,828 rps); still the slowest stack —
  STRUCTURAL: reactive cross-event-loop handoff on 2 cores costs more than
  virtual-thread park/unpark (profiled via async-profiler itimer/wall/alloc:
  I/O-wait + handoff bound, not CPU/alloc bound — a `@Query` autocommit insert
  measured *equal*, confirming it). Needs the epoll netty deps in the pom
  (`netty-transport-classes-epoll` compile + native classifier runtime), same as
  spring-vt. Non-blocking end to end — no JDBC, no `Dispatchers.IO`.
- **spring-kt-vt** (Spring Boot 4.1 + Kotlin + virtual threads + **Exposed DSL**
  over HikariCP JDBC, port **50059**). The Kotlin sibling of spring-vt — blocking,
  on virtual threads, NOT coroutines/R2DBC — but the data layer is the JetBrains
  **Exposed DSL** instead of hand-written SQL, so it's also the "Exposed framework
  cost on virtual threads" data point. **Exposed↔Spring wiring follows the official
  `JetBrains/Exposed samples/exposed-spring` (Boot 4):** dependency
  `org.jetbrains.exposed:exposed-spring-boot4-starter` + `exposed-jdbc` (v**1.3.0**,
  not Boot-managed, pinned in the pom), the app class is
  `@ImportAutoConfiguration(ExposedAutoConfiguration::class)`, and `Db` is a
  `@Component @Transactional` bean whose methods call the Exposed DSL **directly** —
  NO manual `Database.connect()`, NO `transaction { }` block. The starter's
  `SpringTransactionManager` opens the transaction and hands Exposed a connection
  from Boot's HikariCP pool (so HikariCP is still the pool; Exposed is only the
  query layer). `spring.exposed.generate-ddl=false` keeps it off the schema
  (setup_db.sh owns DDL). Imports are the Exposed 1.x `v1` package paths
  (`org.jetbrains.exposed.v1.jdbc.*`, `org.jetbrains.exposed.v1.core.*`); the
  version increment in the upsert uses the top-level `plus` builder
  (`WorkflowState.version + 1L`) + `insertValue(...)`. The
  gRPC codegen is plain grpc-java (`<goal>generate</goal>` only, no grpc-kotlin),
  so the stubs are `CommandServiceGrpc.CommandServiceImplBase` (StreamObserver)
  extended from Kotlin. Same VT-executor requirement as spring-vt:
  `GrpcServerConfig` provides the `ServerBuilderCustomizer<NettyServerBuilder>`
  calling `builder.executor(Executors.newVirtualThreadPerTaskExecutor())`,
  otherwise handlers (and the blocking Exposed/JDBC) stay on grpc-java's default
  *platform* pool. Tuned harder for c=128 on that same builder: 1 MiB HTTP/2
  flow-control windows, `maxConcurrentCallsPerConnection(Int.MAX_VALUE)`, server
  keepalive (30s/10s, permit-without-calls), `withChildOption(TCP_NODELAY, true)`.
  Pool stays min=4/max=16 (PG_POOL_MIN/MAX env) for fairness; speed comes from the
  VT executor + Netty knobs, not an oversized pool. The Kotlin `spring` compiler
  plugin (allopen) opens the `@Transactional` class so Spring can proxy it.
- **spring-data-jdbc** (Spring Boot 4.1 + virtual threads + **Spring Data JDBC**
  repositories over HikariCP, port **50060**). The Java twin of spring-vt — same
  Loom model (blocking on virtual threads, NOT reactive), same gRPC contract,
  same SQL, same HikariCP pool, same `GrpcServerConfig`/`Fnv`/`CommandServiceImpl`
  (copied verbatim). The ONLY difference is the data layer: spring-vt drives the
  fluent `JdbcClient`, this drives the Spring Data JDBC repository abstraction.
  Dependency is `spring-boot-starter-data-jdbc` (pulls in `spring-boot-starter-jdbc`
  → HikariCP transitively, so HikariCP is still the pool — only the query layer
  differs). Entities are immutable `record`s mapped with `@Table`/`@Id`/`@Column`
  (Spring Data JDBC's `DefaultNamingStrategy` does NOT snake_case, so every
  camelCase field that maps to a snake_case column needs an explicit `@Column` —
  `workflowId`→`workflow_id` etc; `received_at`/`created_at`/`dispatched` are left
  unmapped so the DB defaults fill them). `Command.save()` on a null `@Id` issues
  the INSERT + fetches the generated key (the repository equivalent of
  `INSERT … RETURNING id`). **The UPSERT cannot go through `save()`** — Spring
  Data JDBC's `save()` only INSERTs *or* UPDATEs (and a non-null String `@Id` on
  `workflow_state` would force an UPDATE), so the conflict-aware insert lives in
  `WorkflowStateRepository.upsert` as a native `@Modifying @Query` (byte-identical
  SQL to spring-vt's UPSERT, named `:wid` params). **Key fairness/perf note:**
  `CrudRepository.save()` is `@Transactional` by default, so even the "autocommit"
  `execute` path costs BEGIN + INSERT + COMMIT (3 DB round-trips) vs spring-vt's
  single autocommit round-trip — that, plus entity-mapping CPU, is why it runs
  materially slower on a small/runtime-bound table (~7.6k vs ~19k rps in a 60s
  same-session A/B at pool=32). `executeTx` is `@Transactional` on the `Db`
  `@Component` (Spring CGLIB-proxies it; methods must be public). Same VT-executor
  requirement as spring-vt (the copied `GrpcServerConfig` sets
  `builder.executor(newVirtualThreadPerTaskExecutor())` + epoll + 1 I/O thread).
  `spring.sql.init.mode=never` keeps Boot off the schema (setup_db.sh owns DDL).
- **rust-tokio** (Rust + tonic + tokio-postgres + deadpool, port **50053**,
  edition 2024). Async/native, the non-JVM counterpart to go-pgx. Split into
  one-concern-per-file modules under `src/` (`main`, `config`, `proto`, `fnv`,
  `db`, `service`, `server`) rather than a single `main.rs`. Uses tonic's native
  `transport::Server` (HTTP/2-only, no axum/hyper hand-rolled loop) tuned to match
  go-pgx (1 MiB windows, `tcp_nodelay`, keepalive 30s/10s). FNV-1a is a hand-rolled
  **32-bit** `fnv1a_32` (the `fnv` crate's hasher is 64-bit — truncating it would
  NOT match Go/Kotlin; unit-tested against reference vectors). `tonic`/`prost`/
  `tonic-health` 0.12/0.13; deadpool-postgres 0.14 with `RecyclingMethod::Fast`.
  `build.rs` (tonic-build) needs `protoc` on PATH (same as go-pgx). Worker threads
  = `RUST_WORKER_THREADS` (default 2, matches GOMAXPROCS).
- **go-pgx**: grpc-go 1.81.1, jackc/pgx v5.10.0, protobuf 1.36.11 (bumped to
  latest).
- **scram-client comes in transitively** from vertx-pg-client. Don't pin it
  explicitly (the artifact was renamed at v3; vertx-pg-client pulls a 3.x
  transitively).

## Build + run (Ubuntu, end-to-end)

```bash
./scripts/setup_db.sh           # one-time DB + schema
./scripts/build_go.sh           # produces bin/go-server + bin/loadgen
./scripts/build_rust.sh         # produces bin/rust-server  (needs cargo + protoc)
./scripts/build_kotlin.sh       # produces bin/kotlin-vertx-bench.jar  (needs JDK 25)
./scripts/build_spring_vt.sh    # produces bin/spring-vt-bench.jar
./scripts/build_spring_rt.sh    # produces bin/spring-rt-bench.jar
./scripts/build_spring_kt_vt.sh # produces bin/spring-kt-vt-bench.jar  (needs JDK 25)
./scripts/build_spring_data_jdbc.sh # produces bin/spring-data-jdbc-bench.jar  (needs JDK 25)
./scripts/run_benchmark.sh      # full sweep, all 7 stacks, over CONCURRENCY_LEVELS
```

Workload selection via `LOADGEN_MODE`: `execute` (autocommit INSERT, default),
`exectx` (3-statement TX), `read` (100% GetState — orchestrator pre-populates
`workflow_state` first), `mixed` (ExecuteTx + `LOADGEN_READ_PCT`% reads).

Override credentials / DB without editing files:

```bash
export PG_USER=postgres PG_PASSWORD=sam PG_DB=proddb
export DATABASE_URL="postgres://${PG_USER}:${PG_PASSWORD}@${PG_HOST:-127.0.0.1}:${PG_PORT:-5432}/${PG_DB}?sslmode=disable"
./scripts/run_benchmark.sh
```

For a publishable run (per README's own advice):

```bash
WARMUP=15s DURATION=60s ./scripts/run_benchmark.sh
```

## Linux-vs-macOS deltas worth remembering

1. **`taskset` is real on Linux.** Both servers and the loadgen *will* be pinned
   to `PIN_SERVER_CPUS` / `PIN_CLIENT_CPUS` (default `0,1` each). On the macOS
   smoke-test runs we did, pinning was a no-op — Linux numbers can differ
   substantially because the runtime constraint is finally enforced.
2. **No Netty DNS resolver warning.** The `i.n.r.d.DnsServerAddressStreamProviders`
   line you saw on macOS won't appear on Linux. If you *do* see it on Linux,
   something is wrong with the JDK / Netty native libs.
3. **GC and `+AlwaysPreTouch`.** JVM_OPTS uses ZGC + `AlwaysPreTouch`. On Linux
   the pre-touch is meaningful (forces page allocation up front); on macOS it
   was mostly harmless overhead.
4. **Postgres CPU watching matters more.** With taskset actually pinning the
   *servers* to cores 0–1, Postgres has cores 2+ to itself, so the bottleneck
   may shift from "DB is the wall at c=32" (what we saw on Mac) to runtime
   differences becoming visible. Run `top` or `htop` in another terminal and
   watch what's actually saturating.

## Architectural decisions that aren't obvious from the code

- **Kotlin: one shared Pool, N CoroutineVerticles.** The Pool is built once on
  the root `Vertx` in `MainVerticle.start()` and shared across `eventLoops`
  instances of `GrpcVerticle`. vertx-pg-client multiplexes connections across
  event loops internally; per-verticle pools would burn connections for no win.
- **Kotlin: `vertxFuture(scope) { … }` bridge keeps work on the event loop.**
  The verticle's `CoroutineScope` has the verticle's event-loop dispatcher, so
  the suspend block (including `coAwait` on prepared queries) runs on the same
  thread the request arrived on. Never switch to `Dispatchers.IO`/`Default` for
  the DB call — it would hop off the event loop and force a re-dispatch back.
- **Kotlin: pool warmup mirrors pgx's MinConns.** `Db.warmup(min)` acquires and
  releases `min` connections at startup so the Vert.x pool starts at the same
  size as the pgx pool. Without it, the first ~5s of traffic grows the pool
  lazily, which would skew the warmup phase against Kotlin.
- **Go: `run() error` instead of `log.Fatal` everywhere.** Lets `defer
  pool.Close()` actually fire on the fatal path. Graceful stop is bounded to
  15s before SIGKILL fallback so a wedged drain can't hang the orchestrator.
- **Go: gRPC health service registered.** Not used by the loadgen, but it
  reflects what you'd actually ship.
- **ExecuteTx is sequential in EVERY stack (5 round trips).** `BEGIN → INSERT →
  UPSERT → INSERT → COMMIT`, each statement awaited before the next. go-pgx
  *used* to pipeline the whole TX via `pgx.Batch` (1 round trip); that was
  removed so it matches the others. Reason: the JDBC/virtual-thread stacks
  (spring-vt, spring-kt-vt) physically cannot pipeline a transaction (JDBC is one
  statement per round trip), so sequential statements are the only TX model all
  stacks can share. Non-transactional queries (`execute`/`read`) still let the
  reactive drivers pipeline — that's a real trait of the reactive model, left on.
- **spring-vt: the VT executor must be set explicitly.** `spring.threads.
  virtual.enabled=true` switches Spring's own task executors to virtual threads
  but leaves the gRPC server on grpc-java's default *platform* pool
  (`grpc-default-executor`). `GrpcServerConfig` provides a
  `ServerBuilderCustomizer<NettyServerBuilder>` that calls
  `builder.executor(Executors.newVirtualThreadPerTaskExecutor())` — verified via
  `jcmd Thread.print` that handlers (and thus the blocking JDBC) run on virtual
  threads.
- **spring-vt co-hosts a REST `/health` endpoint on Jetty (the only stack that
  runs two network stacks in one JVM).** `spring-boot-starter-web` with Tomcat
  *excluded* + `spring-boot-starter-jetty` adds a Spring MVC servlet container
  alongside grpc-netty; `HealthController` serves a plain `200 "UP"` liveness (no
  DB, no Actuator — Actuator's DataSource check would borrow a HikariCP conn per
  ping and muddy the co-host signal). Jetty binds `server.port` = `${HTTP_PORT:8080}`;
  gRPC stays on `spring.grpc.server.port`. With virtual threads on, Jetty routes
  requests via its `VirtualThreadPool`, so it adds only ~3 platform threads
  (MasterPoller + acceptor), NOT a `qtp` worker pool — that's why we picked Jetty
  over Tomcat (whose default is max-threads=200). **Netty I/O (worker) threads = 1
  is set by `NETTY_IO_THREADS=1` in `application.properties`, read by
  `GrpcServerConfig` via `@Value("${NETTY_IO_THREADS:0}")`** (0 => auto cores/2).
  Spring orders the OS environment ABOVE `application.properties`, so the perf A/B
  harness still forces 2 with an env var of the same name — verified: file-only →
  `io threads=1`, env `NETTY_IO_THREADS=2` → `io threads=2`. This made the count
  explicit (was `cores/2`, which is 1 only under the taskset pin; bare on the
  12-core box it was 6). NOTE the surviving inert keys in `application.properties`,
  kept for documentation: (1) `spring.grpc.server.netty.boss-threads/worker-threads`
  are dead — `GrpcServerConfig`'s `ServerBuilderCustomizer` builds the event-loop
  groups directly (boss always 1; worker = the NETTY_IO_THREADS value) and overrides
  property-based Netty config; (2) `io.netty.allocator.type=pooled` is a Netty
  *system* property that only works as a `-D` JVM arg (set in `SPRING_VT_JVM_OPTS`),
  not from `application.properties`. **spring-vt has its own `SPRING_VT_JVM_OPTS`**
  (config.sh + the soak array): `-Xms/-Xmx2304m`, ZGC `ConcGCThreads=1` (protect
  the 2 vCPU), `+UseCompactObjectHeaders`, `MaxDirectMemorySize=768m`,
  `-Dio.netty.allocator.type=pooled` — a *different* JVM tune than the shared
  `JVM_OPTS` the original 11,973-rps baseline ran under, so the co-host number
  conflates the second server + the JVM change (≤2% total; the GC change improved
  the tail). The external probe is `scripts/health_ping.sh` (curl `/health` every
  5s, pinned to free cores, latency CSV + p50/p99/max summary); run it *alongside*
  the soak. Co-host result: −2.1% gRPC rps, p99 flat, p99.9 −27%, 0 errors, and
  `/health` p99 11.5 ms with 0 stalls under full gRPC saturation. README has the
  table under "Co-hosting REST + gRPC in one service".
- **Cache-miss + ctx-switch perf-stat A/B (spring-vt, `results/perf-cohost2-*`,
  needs `perf_event_paranoid<=2`; re-run via `scratchpad/perf_ab2.sh`-style 4-cell
  harness — base/nohdr/io2/base2 at matched ~18k rps, base↔base2 agree to 0.25% so
  >0.5% deltas are real).** Three takeaways: (1) **compact headers (JEP 519) are a
  real but modest cache win** — −2.0% cache-misses/instruction, −3.1% L1-misses/instr,
  +1.0% IPC (modest because the path is DB-round-trip bound, small per-request heap
  footprint). (2) **1-vs-2 I/O threads is a cache-locality story, NOT a context-switch
  one** — see GrpcServerConfig's comment; ctx switches stayed ~flat, IPC dropped 6%
  and cache-misses rose 10% with a 2nd loop. (3) **ctx-switch rate (~18k/s, ~6-7 per
  million instr) is workload-governed, not knob-governed** — driven by virtual-thread
  park/unpark on the blocking JDBC round-trips, unchanged by headers or I/O-thread
  count; the lever for fewer ctx switches is fewer DB round-trips, not thread tuning.
  Caveat: this CPU returns `<not supported>` for LLC-load/miss PMU events; `cache-misses`
  (generic LLC-miss proxy) and L1-dcache events do count. Context switches are also
  readable without the PMU via `pidstat -w -t` (sum threads) — but perf's
  `context-switches` is the thread-inclusive counter we used.
- **Raw SQL everywhere; only the placeholder syntax differs.** The reactive
  stacks use `$1`-style (vertx-pg-client); the JDBC stacks use `?` (pgjdbc).
  Statements/plans are identical. JDBC stacks pass `prepareThreshold=1` so
  pgjdbc server-prepares from first use (pgx/Vert.x cache by default).
- **Best-in-class pool per driver, all min=4/max=16, pre-warmed.** HikariCP
  (`minimum-idle`), the Vert.x pool (kotlin-vertx warms by holding `PG_POOL_MIN`
  connections at startup), pgxpool (`MinConns`), R2DBC pool, deadpool.

## Known gotchas (already fixed, in case they regress)

1. **Orchestrator orphan-server bug** (fixed in `scripts/config.sh` +
   `scripts/run_benchmark.sh`). The wrapper used to be a shell function called
   via `&`. That meant `$!` was the subshell PID, not the server's — `kill $!`
   killed the subshell and the actual server got reparented to init, kept
   holding the port. *Every subsequent `start_server` silently failed with
   `bind: address already in use` and the loadgen quietly hit the c=1
   server.* Symptoms: results that look fine but the *only* server log lines
   that remain are from the last failed start, and `ps -ef | grep go-server`
   shows orphans after the run. Fix uses a prefix array
   (`SERVER_PIN=(taskset -c …)` or `()`) instead of a function so `$!` is the
   real PID. **If you see orphan servers after a run, the array expansion
   probably regressed under `set -u`** — must use
   `${SERVER_PIN[@]+"${SERVER_PIN[@]}"}`, not `"${SERVER_PIN[@]}"`.

2. **Startup-failure deadlock in Kotlin** (fixed in `Main.kt`). Calling
   `exitProcess` from `.onFailure { … }` runs it on the event loop;
   `System.exit` then triggers the shutdown hook, which tries to
   `vertx.close().get()` — but the event loop is blocked inside `System.exit`.
   Hand exit off to a separate thread. Symptom: 9–10s of `BlockedThreadChecker`
   "Thread blocked" warnings when DB ping fails.

3. **Maven `compile-custom` goal**. The protobuf-maven-plugin's `compile-custom`
   goal requires a top-level `<pluginId>`, which the `<protocPlugins>` block
   doesn't provide. Use *only* `<goal>compile</goal>` — that goal reads
   `<protocPlugins>` and runs the Vert.x gRPC plugin alongside the built-in
   Java generator.

## Validation checklist for the Ubuntu run

After `./scripts/run_benchmark.sh` completes:

```bash
# 1. No orphans (all stacks). Tip: `jps -l | grep bench` is cleaner for the JVM
#    ones and avoids pkill self-match (a pkill -f pattern that also appears in
#    your command line will SIGTERM your own shell — kill by PID from jps). The
#    native servers (go-server, rust-server) aren't JVMs, so grep for them too.
ps -ef | grep -E '(go-server|rust-server|kotlin-vertx-bench|quarkus-run.jar|spring-vt-bench|spring-rt-bench|spring-kt-vt-bench|spring-data-jdbc-bench)' | grep -v grep \
  && echo "BAD: orphans" || echo "OK"

# 2. Ports free
ss -ltn | grep -E ':5005[1-9]' && echo "BAD: port held" || echo "OK: ports free"

# 3. Each server had one clean lifecycle per concurrency level
RUN="results/$(ls -t results/ | head -1)"
grep -c 'go-pgx server listening' "${RUN}/go-pgx.server.log"            # = N levels
grep -c 'rust-tokio server listening' "${RUN}/rust-tokio.server.log"    # = N levels
grep -c 'kotlin-vertx server up'  "${RUN}/kotlin-vertx.server.log"      # = N levels
grep -c 'quarkus-vt server up'    "${RUN}/quarkus-vt.server.log"        # = N levels
grep -c 'quarkus-rt server up'    "${RUN}/quarkus-rt.server.log"        # = N levels
grep -c 'gRPC Server started'     "${RUN}/spring-vt.server.log"         # = N levels
grep -c 'gRPC Server started'     "${RUN}/spring-rt.server.log"         # = N levels
grep -c 'gRPC Server started'     "${RUN}/spring-kt-vt.server.log"      # = N levels
grep -c 'gRPC Server started'     "${RUN}/spring-data-jdbc.server.log"  # = N levels

# 4. ZERO blocked-event-loop warnings on the REACTIVE stacks (kotlin-vertx,
#    quarkus-rt). If non-zero, something blocks the event loop and the reactive
#    comparison is invalid. (The -vt stacks block on purpose — on virtual
#    threads — so this check doesn't apply to them.)
grep -c BlockedThreadChecker "${RUN}/kotlin-vertx.server.log"          # MUST be 0
grep -ci 'blocked'            "${RUN}/quarkus-rt.server.log"           # MUST be 0

# 5. Loadgen reported zero errors at every level (total_err is field 11 now
#    that 'mode' is column 3 in summary.csv).
awk -F, 'NR>1 && $11!="0" {print "BAD: errors at",$1,"c="$2,"mode="$3,$11}' "${RUN}/summary.csv"

# 6. Sanity: Postgres CPU. If it's pinned at 100% of a core, the bench is
#    measuring PG, not either runtime. Decide which question matters.
top -bn1 | grep -E '^[[:space:]]*[0-9]+.*postgres' | head -5
```

If #4 is non-zero on Linux, *stop and investigate before trusting numbers* —
that means we accidentally blocked an event loop and the whole "Vert.x reactive
advantage" comparison is invalid for that run.

## Tweaking the workload

All knobs are in `scripts/config.sh`. The ones you'll actually change:

| Var | Default | Notes |
|-----|---------|-------|
| `STACKS` | `go-pgx rust-tokio kotlin-vertx spring-vt spring-rt spring-kt-vt spring-data-jdbc` | which servers to sweep |
| `CONCURRENCY_LEVELS` | `1 8 32 64 128` | the sweep |
| `LOADGEN_MODE` | `execute` | `execute` \| `exectx` \| `read` \| `mixed` |
| `LOADGEN_READ_PCT` / `LOADGEN_KEYSPACE` | `20` / `10000` | mixed read %; keyspace = read-seed size |
| `WARMUP` / `DURATION` | `5s` / `30s` | bump for publishable numbers (15s/60s) |
| `PG_POOL_MIN/MAX` | `4` / `16` | match across all five stacks |
| `PIN_SERVER_CPUS` / `PIN_CLIENT_CPUS` | `2,3` / `4,5` | server vs client on separate cores |
| `JVM_OPTS` | `-Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch` | Java 25, all 5 JVM stacks |

Per-stack gRPC ports: go-pgx `50051`, kotlin-vertx `50052`, rust-tokio `50053`,
quarkus-vt `50055`, spring-vt `50056`, quarkus-rt `50057`, spring-rt `50058`,
spring-kt-vt `50059`, spring-data-jdbc `50060` (overridable in `config.sh`).

To stress *runtime/driver* and not Postgres: switch `commands` to `UNLOGGED`
in `sql/schema.sql`, drop the index, or move PG to a different box. Document
in the run notes whichever you did — it changes what the numbers mean.
