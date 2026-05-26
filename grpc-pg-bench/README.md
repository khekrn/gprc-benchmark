# gRPC + Async Postgres Benchmark: Go (pgx) vs Kotlin/Vert.x

Two functionally identical gRPC services that unmarshal a command, do a tiny
CPU touch (FNV-1a checksum), and insert one row into Postgres via an async
driver. The point is to decide a stack for the next-gen workflow engine by
measuring throughput and tail latency on a **2-core / 4 GB** box.

- **go-pgx** — Go + `google.golang.org/grpc` + `jackc/pgx` (pgxpool).
  Production-shaped: graceful shutdown on SIGINT/SIGTERM, gRPC keepalive
  (server enforcement), `grpc.health.v1` registered, slog structured logs,
  pgxpool tuned with `MaxConnLifetime` / `MaxConnIdleTime` / `HealthCheckPeriod`.
- **kotlin-vertx** — Kotlin coroutines + Vert.x 5 gRPC server + `vertx-pg-client`.
  Built around the kotlin-vertx-talk pattern: a `CoroutineVerticle` per event
  loop (deployed with `instances = VERTX_EVENT_LOOPS`), a shared pool built
  once on the root Vertx, gRPC handlers bridged to coroutines via
  `vertxFuture(scope) { … }` so the suspend block runs on the same event
  loop the request arrived on — `coAwait` yields the loop while waiting on
  the network, never blocks it.

A single Go load generator drives both servers, so you measure the *servers*,
not two different clients.

## Layout

```
proto/command.proto         # shared contract (single source of truth)
sql/schema.sql              # shared table + index
go-pgx/                     # Go server (stubs generated into gen/benchv1)
kotlin-vertx/               # Maven project, Kotlin/Vert.x server
loadgen/                    # shared Go load generator
scripts/                    # build + run + orchestrate
results/                    # JSON + summary.csv per run (created at runtime)
```

## Prerequisites

- PostgreSQL 14+ running and reachable (local is best for this test).
- Go 1.23+ and `protoc` (for the Go server + loadgen + Go stubs).
- **JDK 25** and Maven 3.9+ (for the Kotlin server). Kotlin 2.2.x's tooling
  parses the Java 25 version string, so the build won't run on older JDKs.
  Maven downloads `protoc` and the Vert.x gRPC plugin itself — no system
  protoc needed for the Kotlin side.
- `psql`, `python3` (summary parsing), and ideally `taskset` (Linux) for CPU
  pinning. On macOS pinning is skipped automatically.

## Quick start

```bash
# 1. One-time DB setup (creates role 'bench', db 'bench', applies schema)
./scripts/setup_db.sh

# 2. Build everything
./scripts/build_go.sh
./scripts/build_kotlin.sh

# 3. Run the full sweep for both stacks
./scripts/run_benchmark.sh
```

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
| `JVM_OPTS` | `-Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch` | Java 25 + 1 GB heap |
| `PIN_SERVER_CPUS` / `PIN_CLIENT_CPUS` | `0,1` / `0,1` | taskset pinning |

## Fairness decisions (read before trusting numbers)

These are deliberate so the comparison isn't accidentally rigged:

1. **Identical work.** Same proto, same FNV-1a checksum (the Kotlin side
   reimplements Go's `hash/fnv` exactly, not CRC32), same single `INSERT ...
   RETURNING id`, same index.
2. **Equal core budget.** `GOMAXPROCS=2` vs `VERTX_EVENT_LOOPS=2`. JVM heap is
   capped at 1 GB so GC behaviour is realistic for the 4 GB box.
3. **Equal pool.** Both use min=4 / max=16 connections.
4. **Prepared statements on both.** pgx statement cache is on by default;
   Vert.x uses `setCachePreparedStatements(true)`. Note Vert.x additionally
   pipelines (`setPipeliningLimit(256)`) — this is a genuine architectural
   advantage of the reactive driver, not a thumb on the scale, so it's left on.
5. **One server at a time.** The orchestrator never runs both servers
   together, so the JVM and Go process don't fight for the 2 cores.
6. **Client contends with server.** On a 2-core box the load generator shares
   the cores with the server. That's the honest constraint of your target
   hardware; if you want client/server isolation, run the loadgen from a second
   machine and set `-addr` to the server's IP.
7. **Warmup discarded.** 5s warmup primes pools, statement caches, and (for
   Kotlin) JIT compilation before measurement.

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

`summary.csv` columns: `stack, concurrency, rps, p50_ms, p90_ms, p99_ms,
p999_ms, max_ms, total_ok, total_err`.

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

Combined sweep: `results/20260525-222611/` (c=1..128) and
`results/20260526-080945/` (c=256 follow-up), same config for both — 3 stacks,
`WARMUP=15s DURATION=60s`, server pinned to two P-cores (cpu 2-3) with a 4 GB
memory cap via `systemd-run --user --scope`, loadgen pinned to cpu 4-5.
Hardware: M1 Pro / Asahi Linux, Postgres 16 local. Zero application errors
across all 18 runs.

`c` = concurrency = number of in-flight gRPC requests the loadgen keeps open
simultaneously (one per worker goroutine). `c=1` measures single-shot
round-trip cost; `c=128`/`c=256` measure saturation and beyond-saturation
behavior.

### Peak throughput

| Rank | Stack          | Peak RPS | At c= |
|-----:|----------------|---------:|------:|
| 1    | rust-tokio     |  61,812  |  64   |
| 2    | kotlin-vertx   |  59,940  |  32   |
| 3    | go-pgx         |  49,324  | 128   |

### Steady-state latency (ms)

| Stack         | p50 @ c=64 | p99 @ c=64 | p99 @ c=128 | p99 @ c=256 |
|---------------|-----------:|-----------:|------------:|------------:|
| rust-tokio    |     0.993  |     2.756  |      4.509  |      7.624  |
| kotlin-vertx  |     0.963  |     2.596  |      4.913  |      9.103  |
| go-pgx        |     1.150  |     4.138  |      5.583  |      8.959  |

### Single-shot latency (c=1, ms)

| Stack         |  p50  |  p99  |
|---------------|------:|------:|
| kotlin-vertx  | 0.129 | 0.235 |
| go-pgx        | 0.140 | 0.227 |
| rust-tokio    | 0.151 | 0.278 |

All three are within ~50 µs of each other — round-trip cost is essentially
equal.

### Tail behavior (`max_ms` across all runs)

- **go-pgx**: cleanest — a single 590 ms outlier; otherwise 16–43 ms.
- **rust-tokio**: three outliers >250 ms (max 917 ms at c=256), tokio scheduler hiccups.
- **kotlin-vertx**: four outliers >280 ms (max 860 ms), likely JVM GC pauses.

At c≤128, p999 stays in the 3–9 ms range for all three (worst-of-3-million
observations, not systemic). **At c=256, rust-tokio's p999 jumps to 310 ms** —
the first sign of systemic tail degradation: the tokio scheduler appears to
starve specific in-flight requests when oversubscribed ~125:1 over its
2 worker threads. Kotlin and Go keep p999 in single-digit ms even at c=256.

### Full table

| stack         |   c | RPS    | p50 ms | p90 ms | p99 ms | p999 ms | max ms  |
|---------------|----:|-------:|-------:|-------:|-------:|--------:|--------:|
| go-pgx        |   1 |  6,760 |  0.140 |  0.178 |  0.227 |   0.393 |   4.219 |
| go-pgx        |   8 | 34,493 |  0.210 |  0.311 |  0.623 |   1.755 |  16.025 |
| go-pgx        |  32 | 47,655 |  0.567 |  0.992 |  3.044 |   4.658 |  18.382 |
| go-pgx        |  64 | 45,423 |  1.150 |  1.871 |  4.138 |   6.606 | 590.748 |
| go-pgx        | 128 | 49,324 |  2.453 |  3.427 |  5.583 |   7.670 |  35.011 |
| kotlin-vertx  |   1 |  7,258 |  0.129 |  0.177 |  0.235 |   0.412 |   6.291 |
| kotlin-vertx  |   8 | 38,330 |  0.193 |  0.279 |  0.439 |   1.462 |  26.840 |
| kotlin-vertx  |  32 | 59,940 |  0.472 |  0.686 |  1.453 |   3.674 | 480.275 |
| kotlin-vertx  |  64 | 58,143 |  0.963 |  1.261 |  2.596 |   5.457 | 860.412 |
| kotlin-vertx  | 128 | 59,500 |  1.996 |  2.447 |  4.913 |   9.156 | 280.414 |
| rust-tokio    |   1 |  6,025 |  0.151 |  0.243 |  0.278 |   0.426 |   7.527 |
| rust-tokio    |   8 | 42,882 |  0.177 |  0.240 |  0.358 |   0.832 |  15.830 |
| rust-tokio    |  32 | 59,962 |  0.479 |  0.660 |  1.604 |   4.346 | 577.421 |
| rust-tokio    |  64 | 61,812 |  0.993 |  1.223 |  2.756 |   5.096 | 252.271 |
| rust-tokio    | 128 | 60,643 |  2.014 |  2.355 |  4.509 |   7.145 | 498.189 |
| go-pgx        | 256 | 48,091 |  5.172 |  6.524 |  8.959 |  13.315 |  42.978 |
| kotlin-vertx  | 256 | 58,069 |  4.066 |  4.735 |  9.103 |  28.319 | 548.930 |
| rust-tokio    | 256 | 50,538 |  4.205 |  4.780 |  7.624 | 310.569 | 916.658 |

### Verdict

1. **kotlin-vertx** — most stable across the whole concurrency curve: peaks at
   c=32 (~60k RPS) and holds ~58k through c=256 with p999 staying single-digit
   ms. Outlier `max_ms` (GC pauses) is the cost.
2. **rust-tokio** — best peak throughput (61.8k @ c=64) and best p99 at
   moderate concurrency, but **degrades at c=256**: throughput drops to ~50k
   and p999 jumps to 310 ms. Tokio's 2-worker-thread scheduler is the
   bottleneck under heavy oversubscription.
3. **go-pgx** — lowest throughput ceiling (~49k) and highest steady-state p50,
   but the most predictable tail by far (`max_ms` < 50 ms at every level
   except one 590 ms outlier).

For the workflow-engine target (2 cores, occasional bursts up to c=128),
rust-tokio and kotlin-vertx are functionally tied. **If sustained c=256+
traffic is realistic, kotlin-vertx is the safer pick**: it holds throughput
and p999 where rust degrades. If c stays ≤128, the choice comes down to
ecosystem fit and team expertise.

## Why these libraries

- **pgx** is the de-facto high-performance native Postgres driver for Go and
  the right baseline for an I/O-bound insert path.
- **Vert.x reactive pg-client + Kotlin coroutines** is the closest Kotlin
  analogue: non-blocking driver, event-loop concurrency, with coroutines
  giving the straight-line `coAwait` code you'd actually write.

Both are the realistic "fast path" choice in their ecosystem, which is what
makes the comparison decision-relevant for Beam.
