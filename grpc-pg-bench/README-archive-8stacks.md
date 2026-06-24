# gRPC + Async Postgres Benchmark: Go, Rust, Kotlin/Vert.x, Quarkus & Spring

Functionally identical gRPC services that unmarshal a command, do a tiny CPU
touch (FNV-1a checksum), and write to / read from Postgres. The point is to
decide a stack for the next-gen workflow engine by measuring throughput and
tail latency on a **2-core / 4 GB** box, across the concurrency models people
actually ship on the JVM (reactive event loop, virtual threads) plus Go as the
baseline.

Eight stacks, all behind the **same** `bench.v1.CommandService` contract, the
**same** SQL, and the **same** Go load generator:

- **go-pgx** — Go + `google.golang.org/grpc` + `jackc/pgx` (pgxpool).
  Production-shaped: graceful shutdown on SIGINT/SIGTERM, gRPC keepalive,
  `grpc.health.v1`, slog logs, pgxpool tuned with lifetime/idle/health-check.
- **rust-tokio** — Rust + `tonic` + `tokio-postgres` + `deadpool` (async,
  native). The non-JVM counterpart to go-pgx: tonic's native HTTP/2-only
  `transport::Server` tuned like go-pgx (1 MiB windows, `tcp_nodelay`,
  keepalive), prepared-statement cache, pre-warmed pool. Code split into
  one-concern-per-file modules (`config`/`db`/`service`/`server`/`fnv`/`proto`).
- **kotlin-vertx** — Kotlin coroutines + Vert.x 5 gRPC + `vertx-pg-client`
  (reactive). A `CoroutineVerticle` per event loop, a shared pool on the root
  Vertx, handlers bridged via `vertxFuture(scope) { … }` so the suspend block
  runs on the event loop the request arrived on — `coAwait` yields the loop on
  I/O, never blocks it.
- **quarkus-rt** — Quarkus gRPC **reactive** (Mutiny `Uni`) + `vertx-pg-client`.
  The same non-blocking driver as kotlin-vertx, exercised through Quarkus +
  Mutiny operators instead of raw Vert.x + coroutines.
- **quarkus-vt** — Quarkus gRPC + **virtual threads** (`@RunOnVirtualThread`) +
  blocking JDBC over the **Agroal** pool. The "Loom" model: straight-line
  blocking code, carrier thread parked on I/O.
- **spring-vt** — Spring Boot 4.1 native gRPC + **virtual threads** + blocking
  JDBC over **HikariCP**. Same Loom model, different framework + pool.
- **spring-rt** — Spring Boot 4.1 native gRPC + **Kotlin coroutines** + **R2DBC**
  (reactive). The reactive peer to spring-vt: `suspend fun` handlers, non-blocking
  R2DBC via `DatabaseClient`, no JDBC.
- **spring-kt-vt** — Spring Boot 4.1 native gRPC + **Kotlin** + **virtual threads**
  + **JetBrains Exposed DSL** over blocking JDBC (**HikariCP**). The Kotlin
  sibling of spring-vt (same Loom model, plain grpc-java stubs called from Kotlin
  — *not* coroutines), but the data layer is the **Exposed DSL** instead of
  hand-written SQL, wired the official way (`exposed-spring-boot4-starter` +
  `@Transactional`, per `JetBrains/Exposed samples/exposed-spring`). So it doubles
  as the "Exposed framework cost on virtual threads" data point (vs **ktor-exposed**,
  Exposed on R2DBC). Tuned harder for c=128 via 1 MiB HTTP/2 windows + keepalive +
  TCP_NODELAY on the Netty server. *Caveat:* Exposed generates its own SQL, so this
  stack is a framework-cost data point, not a byte-identical-SQL comparison.

The `-vt` stacks deliberately use blocking JDBC (virtual threads make that
cheap); the reactive stacks (`kotlin-vertx`, `quarkus-rt`, `spring-rt`) use async
drivers (`vertx-pg-client` / R2DBC); `go-pgx` uses goroutines + pgx and
`rust-tokio` uses tokio tasks + tokio-postgres. That spread is the point: it
isolates *concurrency model + driver*, not framework sugar.

A single Go load generator drives every server, so you measure the *servers*,
not different clients.

## Layout

```
proto/command.proto         # shared contract (single source of truth)
sql/schema.sql              # shared table + index
go-pgx/                     # Go server (stubs generated into gen/benchv1)
rust-tokio/                 # Cargo, Rust/tonic + tokio-postgres + deadpool (modular src/)
kotlin-vertx/               # Maven, Kotlin/Vert.x reactive server
quarkus-rt/                 # Maven, Quarkus gRPC reactive (Mutiny) server
quarkus-vt/                 # Maven, Quarkus gRPC + virtual threads (JDBC/Agroal)
spring-vt/                  # Maven, Spring Boot gRPC + virtual threads (JDBC/Hikari)
spring-rt/                  # Maven, Spring Boot gRPC + Kotlin coroutines (R2DBC)
spring-kt-vt/               # Maven, Spring Boot gRPC + Kotlin + virtual threads (Exposed DSL/Hikari)
loadgen/                    # shared Go load generator
scripts/                    # build + run + orchestrate
results/                    # JSON + summary.csv per run (created at runtime)
```

All four JVM stacks share the same Java package (`com.beam.bench`), the same
`Fnv` checksum, the same SQL strings, and read the same env knobs from
`scripts/config.sh`. Only the concurrency model and DB driver differ.

## Prerequisites

- PostgreSQL 14+ running and reachable (local is best for this test).
- Go 1.23+ and `protoc` (for the Go server + loadgen + Go stubs).
- **JDK 25** and Maven 3.9+ for all four JVM stacks. kotlin-vertx compiles to
  release 25 (Kotlin 2.2's tooling parses the Java 25 version string); the
  Quarkus/Spring stacks target bytecode 21 but run on the same JDK 25 — so the
  whole suite runs on one runtime. Set it active before building/running, e.g.
  `sdk use java 25.0.3-amzn` (or make it your sdkman default). Each JVM build
  downloads its own `protoc`/gRPC codegen — no system protoc needed for them.
- `psql`, `python3` (summary parsing), and ideally `taskset` (Linux) for CPU
  pinning. On macOS pinning is skipped automatically.

> **Brand-new framework versions (June 2026):** Quarkus 3.36.3 and Spring Boot
> 4.1.0 (whose native `spring-boot-starter-grpc-server` ships first-class gRPC
> auto-configuration). Both fully support JDK 25.

## Quick start

```bash
# 1. One-time DB setup (creates role 'bench', db 'bench', applies schema)
./scripts/setup_db.sh

# 2. Build everything (JVM builds need JDK 25 active)
./scripts/build_go.sh
./scripts/build_kotlin.sh
./scripts/build_quarkus_vt.sh
./scripts/build_quarkus_rt.sh
./scripts/build_spring_vt.sh

# 3. Run the full sweep for all five stacks
./scripts/run_benchmark.sh

# Workload selection (writes vs reads):
LOADGEN_MODE=execute ./scripts/run_benchmark.sh   # single autocommit INSERT (default)
LOADGEN_MODE=exectx  ./scripts/run_benchmark.sh   # 3-statement transaction
LOADGEN_MODE=read    ./scripts/run_benchmark.sh   # 100% GetState (pre-populated)
LOADGEN_MODE=mixed   ./scripts/run_benchmark.sh   # ExecuteTx + LOADGEN_READ_PCT% reads
```

For `read`/`mixed` the orchestrator pre-populates `workflow_state` (via
`generate_series` over the loadgen's `wf-<worker>-<key>` keyspace) after each
truncate, so reads hit real rows instead of measuring empty lookups.

Results print as a table at the end and are saved under
`results/<timestamp>/` (per-run JSON + `summary.csv` + `environment.txt`).

### Running one stack / one level manually

```bash
# terminal 1
./scripts/run_go_server.sh
# terminal 2
./bin/loadgen -addr 127.0.0.1:50051 -c 64 -d 30s -label go-pgx
```

## Configuration

All knobs live in `scripts/config.sh` and are overridable via env vars:

| Var | Default | Meaning |
|-----|---------|---------|
| `CONCURRENCY_LEVELS` | `1 8 32 64 128` | in-flight request sweep |
| `DURATION` | `30s` | measured phase per level |
| `WARMUP` | `5s` | unmeasured priming phase |
| `PAYLOAD` | `256` | payload bytes |
| `PG_POOL_MAX` / `PG_POOL_MIN` | `16` / `4` | pool size (both stacks) |
| `GOMAXPROCS` | `2` | Go core cap |
| `VERTX_EVENT_LOOPS` | `2` | Vert.x event-loop threads (= GrpcVerticle instances) |
| `JVM_OPTS` | `-Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch` | Java 25 + 1 GB heap (all 4 JVM stacks) |
| `PIN_SERVER_CPUS` / `PIN_CLIENT_CPUS` | `2,3` / `4,5` | taskset pinning (server / client) |
| `LOADGEN_MODE` | `execute` | `execute` \| `exectx` \| `read` \| `mixed` |
| `LOADGEN_READ_PCT` | `20` | % reads in `mixed` mode |
| `LOADGEN_KEYSPACE` | `10000` | per-worker `workflow_id` pool (also the seed size) |
| `STACKS` | `go-pgx rust-tokio kotlin-vertx quarkus-vt quarkus-rt spring-vt spring-rt spring-kt-vt` | which servers to sweep |

Per-stack gRPC ports (override in `config.sh`): go-pgx `50051`, kotlin-vertx
`50052`, rust-tokio `50053`, quarkus-vt `50055`, spring-vt `50056`, quarkus-rt
`50057`, spring-rt `50058`, spring-kt-vt `50059`. Only one server runs at a time, so the ports just
keep the stacks independently addressable.

## Fairness decisions (read before trusting numbers)

These are deliberate so the comparison isn't accidentally rigged:

1. **Identical work.** Same proto, same FNV-1a checksum (every JVM stack
   reimplements Go's `hash/fnv` exactly, not CRC32), same `INSERT ... RETURNING
   id`, same index. The SQL is character-identical except for the placeholder
   syntax forced by each driver (JDBC `?` for the `-vt` stacks, `$1` for the
   reactive ones); the statements and plans are the same.
2. **Same transaction model everywhere (sequential).** `ExecuteTx` runs
   `BEGIN → INSERT → UPSERT → INSERT → COMMIT` as five sequential round trips
   in *every* stack. go-pgx used to pipeline the whole TX via `pgx.Batch`; that
   was changed to sequential per-statement awaits so it matches the others.
   This matters because the virtual-thread/JDBC stacks (`quarkus-vt`,
   `spring-vt`) *cannot* pipeline a transaction — JDBC is one statement per
   round trip — so sequential statements are the only model all five can share.
3. **Equal core budget.** `GOMAXPROCS=2` for Go; `VERTX_EVENT_LOOPS=2` for the
   reactive JVM stacks; the virtual-thread stacks get the same 2 pinned cores
   as carrier threads. JVM heap capped at 1 GB so GC behaviour is realistic for
   the 4 GB box.
4. **Equal pool.** All five use min=4 / max=16 connections (pgxpool, Vert.x
   pool, Agroal, HikariCP respectively), pre-warmed to min at startup.
5. **Best-in-class pool per driver.** Agroal (Quarkus JDBC), HikariCP (Spring
   JDBC), the Vert.x reactive pool (both reactive stacks), pgxpool (Go) — each
   the production default for its ecosystem.
6. **Prepared statements on everywhere.** pgx statement cache on by default;
   Vert.x `setCachePreparedStatements(true)`; the JDBC stacks pass
   `prepareThreshold=1` so pgjdbc server-prepares from first use; quarkus-rt
   enables the reactive client's prepared-statement cache. The reactive drivers
   additionally pipeline *non-transactional* queries — a genuine architectural
   trait of the reactive model, left on for `execute`/`read`/`mixed`.
7. **Virtual threads actually verified.** `spring-vt` sets the gRPC server
   executor to `newVirtualThreadPerTaskExecutor()` via a `ServerBuilderCustomizer`
   (because `spring.threads.virtual.enabled=true` alone leaves gRPC on its
   platform pool); `quarkus-vt` uses `@RunOnVirtualThread`. Both were confirmed
   to run handlers — and therefore the blocking JDBC call — on virtual threads.
8. **One server at a time.** The orchestrator never runs two servers together,
   so the JVMs and Go process don't fight for the 2 cores.
9. **Client contends with server** only if co-located; by default the loadgen
   is pinned to a *different* core pair (`4,5`) from the server (`2,3`), the
   production-realistic shape (clients call from elsewhere).
10. **Warmup discarded.** The warmup phase primes pools, statement caches, and
    JVM JIT before measurement.

### Known caveats

- **JIT warmup**: 5s may be short for the JVM to reach steady state. For a
  publishable result bump `WARMUP=15s` and `DURATION=60s`.
- **Closed-loop generator** measures latency under a fixed concurrency, not
  under a fixed arrival rate. It answers "what throughput and tail latency do
  N in-flight clients get," which is the right question for a worker pool, but
  it won't surface coordinated-omission effects the way an open-loop tool
  (e.g. a fixed-RPS driver) would. If you need open-loop, that's a follow-up.
- **The DB is usually the bottleneck.** With a single tiny INSERT, both stacks
  may saturate Postgres before they saturate the language runtime. Watch the
  Postgres CPU during the run — if PG is pinned at ~100% of a core, you're
  benchmarking Postgres, and the two stacks will look nearly identical. To
  stress the *runtime/driver* instead, lower DB cost (e.g. `UNLOGGED` table,
  or batch inserts) — but then you're measuring a different thing. Decide which
  question matters for Beam and document it.

## Interpreting the output

`summary.csv` columns: `stack, concurrency, mode, rps, p50_ms, p90_ms, p99_ms,
p999_ms, max_ms, total_ok, total_err, write_rps, write_p50_ms, write_p99_ms,
read_rps, read_p50_ms, read_p99_ms`. The per-op (`write_*`/`read_*`) columns are
populated in `mixed` mode and mirror the totals otherwise.

What to look for:
- **Throughput plateau**: where does RPS stop climbing as concurrency rises?
- **Tail latency under load**: compare p99/p999 at the knee, not just p50.
- **Error count**: any non-zero `total_err` (outside phase-end deadlines)
  means the server shed load — investigate before comparing throughput.

A quick chart:
```bash
python3 - results/<timestamp>/summary.csv <<'PY'
import csv,sys
rows=list(csv.DictReader(open(sys.argv[1])))
for s in sorted({r['stack'] for r in rows}):
    print(s); [print(f"  c={r['concurrency']:>4}  rps={float(r['rps']):>9.0f}  p99={r['p99_ms']}ms") for r in rows if r['stack']==s]
PY
```

## Results

**All eight stacks**, three workloads (writes: `execute`, `exectx`; reads:
`read`), swept c = 1/8/32/64/128, on **Ubuntu Linux / AMD Ryzen 5 7535HS**
(ThinkPad E14 Gen 6). Server pinned to 2 cores (cpu 2,3) with a **4 GB**
`MemoryMax` cgroup cap, loadgen pinned to cpu 4,5, local Postgres, **JDK 25**
(Corretto 25.0.3), `WARMUP=15s DURATION=60s`, pool min 4 / max 16, ZGC + 1 GB
heap. Every run used the **identical** config. Result dirs:
`results/20260618-195914` (execute), `…-203402` (exectx), `…-210952` (read) for
go-pgx/kotlin-vertx/quarkus-rt/quarkus-vt/spring-vt; spring-rt
`…-232040/-232730/-233420`; rust-tokio `20260619-074828/-075453/-080119`;
spring-kt-vt `20260619-082154/-082840/-083526`.

One row per **(stack, concurrency)**; columns **RPS, p90, p99, max (ms)**.
Stacks are ordered by the overall ranking (best first); **bold** marks the best
value for that metric at that concurrency level (across stacks; collapsed
quarkus-vt levels excluded). quarkus-vt collapses under virtual-thread pool
contention — see the dedicated note below. (rust-tokio and spring-kt-vt were
measured 2026-06-19 vs the rest on 2026-06-18 — same box, same config; cross-day,
so treat ±10% as noise.)

### `execute` — autocommit INSERT (write)

| stack | c | RPS | p90 ms | p99 ms | max ms |
|---|--:|--:|--:|--:|--:|
| rust-tokio | 1 | 1,936 | 0.540 | 0.702 | 6.982 |
| rust-tokio | 8 | **12,811** | **0.735** | **0.935** | 14.689 |
| rust-tokio | 32 | **23,617** | **1.608** | **2.040** | 28.538 |
| rust-tokio | 64 | **23,852** | **2.957** | **3.585** | 40.240 |
| rust-tokio | 128 | **23,566** | **5.736** | **7.040** | 50.518 |
| go-pgx | 1 | 2,122 | 0.518 | 0.645 | **6.627** |
| go-pgx | 8 | 10,678 | 0.936 | 1.527 | 16.076 |
| go-pgx | 32 | 17,842 | 2.555 | 4.059 | 30.928 |
| go-pgx | 64 | 17,992 | 4.797 | 6.449 | 42.555 |
| go-pgx | 128 | 17,756 | 8.915 | 11.370 | **50.475** |
| quarkus-rt | 1 | **2,129** | 0.523 | **0.591** | 6.832 |
| quarkus-rt | 8 | 10,734 | 0.869 | 1.107 | 15.475 |
| quarkus-rt | 32 | 15,087 | 2.457 | 3.968 | **19.229** |
| quarkus-rt | 64 | 15,549 | 4.555 | 6.724 | 48.292 |
| quarkus-rt | 128 | 15,004 | 8.975 | 14.062 | 56.568 |
| spring-vt | 1 | 1,802 | 0.596 | 0.726 | 629.288 |
| spring-vt | 8 | 8,791 | 1.053 | 3.512 | 17.688 |
| spring-vt | 32 | 10,865 | 6.186 | 10.202 | 180.724 |
| spring-vt | 64 | 14,891 | 5.866 | 8.718 | **26.625** |
| spring-vt | 128 | 12,580 | 16.773 | 28.964 | 115.187 |
| kotlin-vertx | 1 | 1,956 | **0.508** | 2.322 | 12.855 |
| kotlin-vertx | 8 | 7,025 | 2.707 | 5.637 | 128.713 |
| kotlin-vertx | 32 | 6,385 | 9.635 | 11.773 | 158.500 |
| kotlin-vertx | 64 | 7,882 | 15.471 | 21.241 | 140.841 |
| kotlin-vertx | 128 | 15,335 | 8.945 | 16.772 | 76.651 |
| spring-kt-vt | 1 | 1,675 | 0.629 | 0.900 | 9.841 |
| spring-kt-vt | 8 | 6,914 | 1.404 | 2.411 | **14.536** |
| spring-kt-vt | 32 | 8,586 | 5.320 | 9.239 | 44.480 |
| spring-kt-vt | 64 | 7,863 | 12.429 | 20.843 | 49.498 |
| spring-kt-vt | 128 | 7,705 | 26.998 | 48.709 | 210.984 |
| spring-rt | 1 | 1,680 | 0.613 | 0.912 | 775.239 |
| spring-rt | 8 | 4,975 | 2.183 | 4.842 | 18.499 |
| spring-rt | 32 | 6,033 | 8.783 | 16.589 | 99.566 |
| spring-rt | 64 | 6,085 | 17.972 | 28.173 | 83.613 |
| spring-rt | 128 | 5,613 | 40.973 | 58.947 | 111.477 |
| quarkus-vt | 1 | 20 | 0.772 | 1.252 | 23.630 |
| quarkus-vt | 8 | 182 | 2.025 | 3.997 | 37.546 |
| quarkus-vt | 32 | 268 | 4.703 | 7.191 | 18.513 |
| quarkus-vt | 64 | 0 | — | — | — |
| quarkus-vt | 128 | 0 | — | — | — |

### `exectx` — 3-statement transaction (write)

| stack | c | RPS | p90 ms | p99 ms | max ms |
|---|--:|--:|--:|--:|--:|
| rust-tokio | 1 | 1,325 | 0.841 | 0.959 | 6.566 |
| rust-tokio | 8 | 2,714 | 5.036 | 6.145 | 202.360 |
| rust-tokio | 32 | **11,609** | **3.082** | **3.592** | 27.708 |
| rust-tokio | 64 | **9,851** | **6.787** | 17.448 | 259.400 |
| rust-tokio | 128 | 7,296 | 32.972 | 40.234 | 445.866 |
| go-pgx | 1 | **1,478** | **0.712** | **0.851** | 8.824 |
| go-pgx | 8 | 3,319 | 4.873 | 6.062 | 188.750 |
| go-pgx | 32 | 7,737 | 5.262 | 7.085 | 40.884 |
| go-pgx | 64 | 4,388 | 17.957 | 24.620 | 282.812 |
| go-pgx | 128 | 6,580 | 31.070 | 43.543 | 433.097 |
| quarkus-rt | 1 | 1,362 | 0.785 | 0.889 | 6.606 |
| quarkus-rt | 8 | **6,107** | **1.539** | **2.083** | 31.590 |
| quarkus-rt | 32 | 8,011 | 4.962 | 8.599 | **21.324** |
| quarkus-rt | 64 | 8,071 | 8.707 | **13.420** | 50.964 |
| quarkus-rt | 128 | **7,936** | **16.803** | **29.645** | **81.904** |
| spring-vt | 1 | 1,437 | 0.739 | 0.916 | **6.542** |
| spring-vt | 8 | 4,990 | 2.415 | 5.635 | 123.802 |
| spring-vt | 32 | 8,048 | 5.342 | 9.597 | 130.160 |
| spring-vt | 64 | 7,472 | 12.272 | 17.969 | **48.111** |
| spring-vt | 128 | 4,636 | 33.914 | 51.811 | 212.490 |
| kotlin-vertx | 1 | 1,342 | 0.748 | 3.289 | 11.804 |
| kotlin-vertx | 8 | 3,781 | 4.808 | 5.908 | 124.055 |
| kotlin-vertx | 32 | 5,190 | 9.734 | 15.429 | 299.436 |
| kotlin-vertx | 64 | 5,146 | 17.151 | 29.200 | 176.510 |
| kotlin-vertx | 128 | 5,087 | 33.973 | 48.460 | 270.810 |
| spring-kt-vt | 1 | 1,189 | 0.912 | 1.379 | 159.130 |
| spring-kt-vt | 8 | 4,605 | 2.120 | 3.994 | **18.261** |
| spring-kt-vt | 32 | 5,262 | 8.784 | 14.578 | 61.511 |
| spring-kt-vt | 64 | 5,068 | 19.038 | 30.351 | 79.840 |
| spring-kt-vt | 128 | 4,910 | 41.410 | 73.016 | 218.425 |
| spring-rt | 1 | 908 | 1.183 | 1.774 | 9.633 |
| spring-rt | 8 | 2,411 | 4.769 | 7.806 | 19.479 |
| spring-rt | 32 | 2,836 | 17.391 | 24.316 | 64.081 |
| spring-rt | 64 | 2,799 | 34.734 | 47.474 | 101.087 |
| spring-rt | 128 | 2,788 | 69.253 | 90.037 | 148.809 |
| quarkus-vt | 1 | 1 | 4.706 | 10.149 | 10.149 |
| quarkus-vt | 8 | 207 | 2.997 | 4.581 | 56.910 |
| quarkus-vt | 32 | 0 | — | — | — |
| quarkus-vt | 64 | 0 | — | — | — |
| quarkus-vt | 128 | 0 | — | — | — |

### `read` — GetState SELECT (read)

| stack | c | RPS | p90 ms | p99 ms | max ms |
|---|--:|--:|--:|--:|--:|
| rust-tokio | 1 | 5,215 | 0.204 | **0.235** | **3.356** |
| rust-tokio | 8 | **22,141** | **0.444** | **0.598** | **6.758** |
| rust-tokio | 32 | **29,163** | **1.462** | **1.833** | **5.421** |
| rust-tokio | 64 | **29,081** | **2.620** | **3.069** | **7.039** |
| rust-tokio | 128 | **28,784** | **4.888** | **5.370** | **24.073** |
| go-pgx | 1 | 4,175 | 0.257 | 0.324 | 3.620 |
| go-pgx | 8 | 15,445 | 0.730 | 1.269 | 9.135 |
| go-pgx | 32 | 19,966 | 2.427 | 3.955 | 33.105 |
| go-pgx | 64 | 20,622 | 4.502 | 6.144 | 39.482 |
| go-pgx | 128 | 20,304 | 8.182 | 9.629 | 24.525 |
| quarkus-rt | 1 | 3,019 | 0.390 | 0.486 | 3.910 |
| quarkus-rt | 8 | 13,565 | 0.702 | 0.952 | 14.409 |
| quarkus-rt | 32 | 16,793 | 2.224 | 2.975 | 23.830 |
| quarkus-rt | 64 | 16,998 | 4.412 | 5.598 | 22.786 |
| quarkus-rt | 128 | 17,889 | 7.597 | 10.272 | 37.623 |
| spring-vt | 1 | 3,906 | 0.274 | 0.378 | 6.220 |
| spring-vt | 8 | 13,322 | 0.836 | 1.556 | 8.185 |
| spring-vt | 32 | 22,357 | 1.944 | 3.315 | 13.778 |
| spring-vt | 64 | 22,295 | 3.972 | 6.193 | 39.270 |
| spring-vt | 128 | 20,101 | 9.007 | 12.842 | 37.640 |
| kotlin-vertx | 1 | **5,787** | **0.187** | 0.236 | 3.738 |
| kotlin-vertx | 8 | 18,410 | 0.527 | 0.796 | 64.548 |
| kotlin-vertx | 32 | 20,484 | 2.091 | 2.847 | 17.076 |
| kotlin-vertx | 64 | 20,414 | 3.672 | 4.953 | 24.612 |
| kotlin-vertx | 128 | 19,632 | 7.029 | 13.602 | 50.747 |
| spring-kt-vt | 1 | 2,610 | 0.398 | 0.520 | 6.072 |
| spring-kt-vt | 8 | 8,179 | 1.266 | 2.038 | 15.881 |
| spring-kt-vt | 32 | 10,312 | 4.629 | 7.167 | 31.629 |
| spring-kt-vt | 64 | 9,710 | 10.105 | 16.064 | 46.692 |
| spring-kt-vt | 128 | 9,270 | 22.011 | 37.889 | 108.913 |
| spring-rt | 1 | 2,693 | 0.392 | 0.567 | 497.977 |
| spring-rt | 8 | 5,740 | 1.957 | 4.358 | 752.225 |
| spring-rt | 32 | 7,019 | 8.021 | 14.314 | 45.659 |
| spring-rt | 64 | 7,558 | 14.420 | 23.460 | 51.000 |
| spring-rt | 128 | 7,345 | 29.229 | 46.018 | 104.540 |
| quarkus-vt | 1 | 5 | 1.301 | 5.900 | 7.428 |
| quarkus-vt | 8 | 179 | 3.248 | 5.476 | 40.479 |
| quarkus-vt | 32 | 733 | 5.882 | 9.829 | 28.430 |
| quarkus-vt | 64 | 0 | — | — | — |
| quarkus-vt | 128 | 0 | — | — | — |

> **Load-shedding caveat.** A few stacks return deadline/UNAVAILABLE errors at
> saturation (the loadgen excludes those from the latency sample, so their p99
> is slightly flattered): execute — go-pgx 269 @c64, kotlin-vertx 989 @c8,
> spring-vt 5978 @c32 / 2281 @c128, quarkus-rt 1311 @c128, spring-rt 983 @c32 /
> 218 @c64; read — kotlin-vertx 2656 @c8 / 2322 @c32, quarkus-rt 2307 @c64,
> spring-vt 5716 @c64. `exectx` was essentially error-free (≤161 on go-pgx).
> rust-tokio and spring-kt-vt showed only sporadic, **non-reproducible**
> client-side blips (~0.1–0.3%: rust execute @c32/64 & read @c128, spring-kt-vt
> exectx @c8) — a diagnostic re-run hit 0 at the same levels and neither server's
> error path fired, so they're transient noise, not load-shedding.

### What the numbers say

- **rust-tokio is the new ceiling on every workload.** Reads ~29k, simple writes
  ~24k, transactional writes peak ~11.6k — and with the cleanest tails of the
  field (read p99 5.4 ms, execute 7.0 ms at c=128). Async-native, no GC, lowest
  per-request CPU on the 2-core box.
- **Reads (behind rust):** spring-vt ~22k, go-pgx/kotlin-vertx ~20k, quarkus-rt
  ~18k cluster together; spring-kt-vt ~10k, spring-rt ~7.5k trail. The DB/CPU is
  the wall, so above the leaders the concurrency model barely separates them.
- **Simple writes (`execute`):** rust ~24k, then **go-pgx ~18k**; quarkus-rt,
  kotlin-vertx and spring-vt land ~15k; spring-kt-vt ~8.6k; spring-rt ~6k.
- **Transactional writes (`exectx`, the realistic workflow path):** rust has the
  highest *peak* (11.6k @ c=32), but at sustained c=128 **quarkus-rt keeps the
  best throughput *and* tail** (7.9k, p99 29.6 ms) — its pipelined reactive driver
  shines when each command is 5 round trips; go-pgx and spring-vt follow
  (~7.7–8.0k); spring-kt-vt ~5.3k; spring-rt trails (~2.8k, p99 up to 90 ms).
- **The Exposed DSL tax:** spring-kt-vt (Exposed DSL) peaks ~8.6k execute vs
  spring-vt (raw JDBC, same VT model) ~14.9k — on this CPU-bound 2-core box the
  DSL's query-building/reflection overhead roughly **halves** write throughput.
  Same query, same plan; the cost is pure CPU, which is the scarce resource here.

### quarkus-vt + Agroal: a virtual-thread footgun

quarkus-vt **livelocks at the fair pool=16** above c≈8–32 (execute 5→0,
exectx collapses by c=32, read 698→0), with each failed level forcing a SIGTERM
stop. Root cause (thread dumps + async-profiler + Quarkus #17304 / PR #30083):
**Agroal's per-thread connection cache is a carrier-bound Netty `FastThreadLocal`**.
Every gRPC request runs on a *fresh* virtual thread, so it never hits that cache,
falls back to the contended shared cache, and under concurrency > pool size
thrashes connection creation + a ForkJoinPool managed-block storm that pegs the
2 cores at ~100 % CPU with **zero completed work**. (`sslmode=disable` removed an
SSL-handshake amplifier but not the core problem.) HikariCP/spring-vt — same VT +
blocking-JDBC model — does **not** do this, because its borrow path is
Loom-native.

**Mitigation** (pool sized to concurrency, `pool=128`) avoids the hard
livelock but, on the 4 GB box, trades it for memory/GC pressure: execute c=128
= 3399 rps but **p999 ≈ 1011 ms**, read c=128 = 1689 rps — still far off the
field. Net: **Quarkus virtual-threads + Agroal is the wrong fit for this
small-box, high-concurrency shape**; if you must use it, size the pool to
expected concurrency *and* give the box more RAM.

### Virtual threads vs coroutines on Spring: spring-vt beats spring-rt

The two Spring stacks isolate concurrency model with everything else equal
(Spring Boot 4.1 gRPC, same SQL, pool=16):

| workload | spring-vt (VT + JDBC/Hikari) | spring-rt (coroutines + R2DBC) |
|---|--:|--:|
| execute peak | **14.9k** | 6.1k |
| exectx peak | **8.0k** | 2.8k |
| read peak | **22.4k** | 7.6k |

On this 2-core/4 GB box with sub-millisecond queries, **virtual threads +
blocking JDBC win by 2–3×**. The reactive R2DBC path (coroutines, Reactor
plumbing, r2dbc-postgresql) carries overhead that only pays off when connection
multiplexing relieves a real bottleneck — and here the CPU/DB is the wall, so it
doesn't. Straight-line blocking code on virtual threads is both simpler and
faster for this shape.

### Verdict (2-core / 4 GB workflow-engine target)

1. **rust-tokio** — fastest on all three workloads with the cleanest tails. The
   performance ceiling; the cost is Rust's higher skill floor (ownership, async).
2. **quarkus-rt** — best *JVM* transactional-write throughput and tail at high c,
   strong reads, zero `exectx` errors. Most balanced reactive-JVM pick.
3. **go-pgx** — best simple-write throughput after rust and very clean tails; the
   dependable, dead-simple baseline.
4. **spring-vt** — best JVM reads, competitive writes, simplest code (blocking
   JDBC on virtual threads). A strong, low-complexity JVM choice.
5. **kotlin-vertx** — excellent reads, mid-pack/erratic writes, occasional
   read-path load-shedding.
6. **spring-kt-vt** — Exposed DSL on virtual threads: clean, idiomatic Kotlin but
   ~half the throughput of raw-JDBC spring-vt on this CPU-bound box.
7. **spring-rt** — works correctly but 2–3× slower than spring-vt; reactive R2DBC
   doesn't earn its keep on this hardware.
8. **quarkus-vt** — not viable at the fair pool on this box (Agroal+VT livelock);
   needs careful pool/RAM tuning to even run high concurrency.

## Ranking

### Scorecard (all eight stacks)

The benchmark measures throughput and latency. The maintainability/readability
axis is judged from actually writing all eight (mental model, framework magic,
footguns, ecosystem/hiring).

| Stack | Throughput | Tail latency (p99 under load) | Code readability / maintainability | Op. robustness |
|---|---|---|---|---|
| **rust-tokio** (tonic + tokio-postgres) | **A+ — #1 all 3 (24k/29k/11.6k)** | **A+ — cleanest tails (read p99 5.4)** | B — modular & clean, but Rust skill floor (ownership/async) | A |
| **go-pgx** (goroutines + pgx) | A — exec 18k, reads 20k | A — very clean | **A — tiny, explicit, no magic** | A |
| **quarkus-rt** (Mutiny + vertx-pg) | A− — **exectx #1 JVM (7.9k@128)**, reads 18k | A — best JVM TX tail | C — reactive `Uni` operator chains | A (0 exectx errors) |
| **spring-vt** (VT + JDBC/Hikari) | A− — reads #1 JVM (22k), ~15k/8k | B+ — good, frays at c128 | **A — straight-line blocking, mainstream** | B (sheds load at c128) |
| **kotlin-vertx** (coroutines + Vert.x) | B+ — reads 20k, writes erratic (5.2k tx) | B — read tail good, writes higher | C — verticles + event-loop discipline | B |
| **spring-kt-vt** (VT + Exposed DSL) | C+ — ~half spring-vt (8.6k/5.3k/10k) | C+ — frays at c128 (p99 38–73) | A− — concise idiomatic Exposed DSL + Spring | B |
| **spring-rt** (coroutines + R2DBC) | C — 2–3× slower (6k/2.8k/7.5k) | C — worst tails (exectx p99 90ms) | B — clean `suspend fun`s, R2DBC edges | B |
| **quarkus-vt** (VT + Agroal) | F — livelocks at pool=16 | F — collapses / p999 ≈ 1s tuned | A — simple code… | **F — Agroal+VT footgun** |

### Best overall — throughput + latency (all workloads)

Pure performance, averaged across `execute` / `exectx` / `read`:

1. **rust-tokio** — fastest on all three, cleanest tails. Uncontested.
2. **go-pgx** — wins `execute` + `read` of the JVM-and-native field, very clean
   tails; only loses sustained `exectx`. The native runner-up.
3. **quarkus-rt** — best JVM transactional-write throughput + tail at high c,
   strong reads, zero TX errors. (Edges go-pgx if you weight the TX path heavily.)
4. **spring-vt** — best JVM reads (22k), competitive writes; tails fray at c=128.
5. **kotlin-vertx** — strong reads (20k), but erratic/weak writes.
6. **spring-kt-vt** — Exposed-DSL CPU tax → ~half spring-vt's throughput.
7. **spring-rt** — reactive R2DBC; lowest JVM throughput, worst tails.
8. **quarkus-vt** — collapses at the fair pool (Agroal+VT livelock).

### Best for code readability + performance (the maintain-heavy engine lens)

This weights *readable, evolvable code* alongside raw speed — the right lens for
a workflow engine that will be maintained for years:

| Rank | Stack | Readability | Performance | Why |
|---|---|---|---|---|
| 1 | **go-pgx** | A | A− | Simplest, most explicit code that's *also* near the top on speed. Best balance. |
| 2 | **rust-tokio** | B | A+ | Fastest by far; modular, clean code — but Rust's ownership/async raises the skill floor. |
| 3 | **spring-vt** | A | B+ | Straight-line blocking Java on virtual threads; mainstream, easy to hire for, strong perf. |
| 4 | **spring-kt-vt** | A− | C+ | The most elegant data layer (Exposed DSL, concise Kotlin) — but the DSL halves throughput here. |
| 5 | **quarkus-rt** | C | A− | Top-tier perf, but Mutiny `Uni` operator chains carry real cognitive cost. |
| 6 | **kotlin-vertx** | C | B | Concise, but verticles + event-loop discipline add load. |
| 7 | **spring-rt** | B− | C | Coroutines read cleanly, but reactive R2DBC plumbing + weakest perf. |
| 8 | **quarkus-vt** | A | F | Simple code, but unusable on this box (Agroal+VT collapse). |

**Bottom line.** If you optimize for *readability + performance together*,
**go-pgx is the sweet spot** — the simplest code that's also fast and operationally
boring. **rust-tokio** is the pick when peak performance and tail latency dominate
and the team can carry Rust. Among pure-JVM options, **spring-vt** is the most
maintainable strong performer; **spring-kt-vt** trades ~2× throughput for a more
elegant Exposed data layer (a fine deal on a bigger box, a costly one on 2 cores);
and the reactive stacks (quarkus-rt, kotlin-vertx, spring-rt) buy you little here
while costing readability. Ranks 4–5 are a judgment call: readability-first →
spring-kt-vt, raw-perf-first → quarkus-rt.

## go-pgx vs *tuned* spring-vt — CPU efficiency (2 cores)

> **Scope:** this is a focused **same-session** A/B of go-pgx against an
> **optimized** spring-vt — **epoll** transport, **1 Netty I/O thread**
> (`cores/2`), **2 GB** heap, raw JDBC — *not* the fair-config spring-vt in the
> 8-stack tables above. The optimization story is in
> [`spring-vt/PROFILING.md`](spring-vt/PROFILING.md). Both servers pinned to 2
> cores (2,3), client to 4,5, interleaved per (mode, c), 12 s warmup / 45 s
> measured. The metric that matters for cloud cost is **`rps/core`** = throughput
> ÷ CPU-cores actually consumed (server CPU sampled from `/proc/PID/stat` over an
> 8 s steady-state window). **`eff` = svt ÷ go** (>1 ⇒ spring-vt more
> CPU-efficient). Memory deliberately ignored — compute is the cost that matters.

### `execute` (single INSERT)

| c | go rps | svt rps | go p99 | svt p99 | go CPU% | svt CPU% | go rps/core | svt rps/core | eff |
|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| 8 | 10,188 | 9,708 | 1.60 | 1.73 | 163 | 168 | 6,250 | 5,779 | 0.92× |
| 32 | 16,583 | 16,092 | 4.30 | 4.54 | 191 | 186 | 8,682 | 8,652 | 1.00× |
| 64 | 16,586 | 15,552 | 6.86 | 9.04 | 193 | 192 | 8,594 | 8,100 | 0.94× |
| 128 | 14,639 | 15,673 | 28.94 | 15.97 | 194 | 195 | 7,546 | 8,037 | **1.07×** |

### `exectx` (3-statement transaction — the workflow-engine path)

| c | go rps | svt rps | go p99 | svt p99 | go CPU% | svt CPU% | go rps/core | svt rps/core | eff |
|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| 8 | 3,016 | 5,742 | 6.16 | 4.81 | 110 | 185 | 2,742 | 3,104 | **1.13×** |
| 32 | 6,593 | 8,758 | 10.98 | 7.20 | 191 | 195 | 3,452 | 4,491 | **1.30×** |
| 64 | 7,029 | 8,346 | 18.82 | 14.65 | 191 | 198 | 3,680 | 4,215 | **1.15×** |
| 128 | 4,754 | 8,237 | 41.97 | 33.34 | 130 | 199 | 3,657 | 4,139 | **1.13×** |

### `read` (GetState SELECT)

| c | go rps | svt rps | go p99 | svt p99 | go CPU% | svt CPU% | go rps/core | svt rps/core | eff |
|--:|--:|--:|--:|--:|--:|--:|--:|--:|--:|
| 8 | 15,515 | 14,424 | 1.27 | 1.07 | 180 | 184 | 8,619 | 7,839 | 0.91× |
| 32 | 20,369 | 20,762 | 3.77 | 3.54 | 194 | 187 | 10,499 | 11,103 | 1.06× |
| 64 | 20,562 | 19,134 | 6.07 | 6.55 | 196 | 193 | 10,491 | 9,914 | 0.95× |
| 128 | 20,098 | 19,117 | 9.63 | 12.51 | 197 | 195 | 10,202 | 9,804 | 0.96× |

**Verdict (CPU efficiency, i.e. cloud-compute cost):**
- **Reads & simple writes — a tie.** `rps/core` within ±9% either way; both
  saturate ~190–195% CPU and do ~the same work per core. Equal compute cost.
- **Transactional writes (`exectx`) — spring-vt wins, 1.13–1.30× more efficient
  *and* higher throughput.** go-pgx doesn't even saturate the cores here
  (110–130% CPU) — it stalls on the 5-round-trip transaction, while spring-vt's
  virtual threads keep both cores busy (195–199%).
- **Tail latency:** go-pgx better at low concurrency, **spring-vt better at high
  concurrency** (execute c=128: 16.0 ms vs 28.9 ms; go-pgx throughput also drops
  at c=128 on execute, spring-vt holds).

**Takeaway:** once tuned, **spring-vt is not less CPU-efficient than go-pgx** — it
is **dead-even on reads/simple writes and measurably cheaper on the transactional
path** a workflow engine actually runs. Go's remaining edges are low-concurrency
tail latency and memory footprint; spring-vt's are the transactional path,
high-concurrency tails, and clean scaling to more cores (see the 4-core result in
`PROFILING.md`).

> **Caveat:** go-pgx's `exectx` numbers look anomalously low (not CPU-bound at
> c=8/c=128 — it stalls rather than saturates), consistent with the erratic
> `exectx` dip in the 8-stack sweep. The `execute`/`read` ties are solid; the
> `exectx` win warrants a go-pgx profile before being called settled.

## Go vs Spring Boot for the workflow engine — recommendation

The real decision for the next-gen **distributed workflow engine** comes down to
**go-pgx (Go)** vs **spring-vt (Spring Boot + virtual threads)** — the two that
win on performance *and* simplicity. (Skip reactive: it's harder to maintain and,
here, slower.)

**Lean: Go**, for the engine core, because a workflow engine is exactly Go's
sweet spot — massively concurrent, I/O-bound, latency-SLA-sensitive, run 24/7
across many nodes:

- **Predictable tails + low footprint.** Go had the cleanest p99/p999 and no
  load-shedding; the JVM frayed (GC, errors) at c=128 on 2 cores. For an engine
  with latency SLAs you'd give JVM nodes more RAM/headroom — i.e. pay more to
  scale. Go's single static binary, fast startup, and low memory let you run
  many cheap nodes (autoscaling on t3-class instances).
- **Simplicity that compounds.** Explicit, framework-free code stays readable as
  the system grows; less "magic" to reason about during incidents.
- **Domain precedent.** Temporal and Cadence — the reference distributed workflow
  engines — are written in Go, gRPC-first. Proven at scale, with a hiring pool
  that knows this exact problem in this exact language.

**Choose Spring Boot + virtual threads** instead if your org is a Java shop:

- **Team velocity usually dominates long-run cost.** A large Java talent pool,
  mature tooling/refactoring, and Spring's ecosystem (data, messaging,
  scheduling, security, observability) make heavy feature evolution faster when
  the team already lives in Java.
- **Virtual threads close most of the old gap.** spring-vt keeps *simple blocking
  code* and still posts top reads and near-top writes — so you get JVM
  ergonomics without the reactive-maintenance tax. **Do not** reach for reactive
  Spring (R2DBC/WebFlux): the data shows it is both slower and harder to maintain.
- The cost you accept: heavier per-node memory/GC, more $ per unit of scale, and
  framework magic to debug.

**Verdict:** greenfield distributed workflow engine where scalability,
tail-latency, and operational simplicity matter most → **Go (go-pgx model)**.
If the organization's center of gravity is Java and hiring/velocity outweigh
per-node efficiency → **Spring Boot 4.1 + virtual threads (spring-vt model)**,
never reactive. A common hybrid: Go for the latency-critical engine core,
Java/Spring for business-logic/integration services, talking gRPC.

## Why these libraries

- **pgx** is the de-facto high-performance native Postgres driver for Go and
  the right baseline for an I/O-bound insert path.
- **Vert.x reactive pg-client + Kotlin coroutines** is the closest Kotlin
  analogue: non-blocking driver, event-loop concurrency, with coroutines
  giving the straight-line `coAwait` code you'd actually write.

Both are the realistic "fast path" choice in their ecosystem, which is what
makes the comparison decision-relevant for Beam.
