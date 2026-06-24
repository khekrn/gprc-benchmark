# gRPC + Postgres Benchmark — Go vs Rust vs Spring Boot (virtual threads)

Three functionally identical gRPC services that unmarshal a command, do a tiny
CPU touch (FNV-1a checksum), and `INSERT` it into Postgres. The goal is to pick a
stack for a **next-gen distributed workflow engine**, judged on **sustained
throughput and latency** on a **2-core** box — the shape of a small cloud node
(memory is cheap on AWS; compute is the cost that matters).

The benchmark here is a **30-minute sustained soak** (one stack at a time, on a
fresh boot). That's the number that reflects how an always-on engine actually
behaves under continuous load over time.

> A broader 8-stack exploration is archived in
> [`README-archive-8stacks.md`](README-archive-8stacks.md). This document is the
> focused **3-stack soak**.

## The three stacks

- **go-pgx** — Go + `google.golang.org/grpc` + `jackc/pgx` (pgxpool). The native
  baseline: goroutines, graceful shutdown, keepalive, tuned pool.
- **rust-tokio** — Rust + `tonic` + `tokio-postgres` + `deadpool`. Async/native;
  tonic's HTTP/2 transport tuned like go (1 MiB windows, `tcp_nodelay`, keepalive).
- **spring-vt** — Spring Boot 4.1 gRPC + **virtual threads** + **Spring
  `JdbcClient`** over HikariCP. Blocking code on Loom (no reactive). Config: epoll
  transport, a single Netty I/O thread, ZGC + 2 GB heap, pool 32. (How that config
  was arrived at — profiling + A/Bs — is in [`spring-vt/PROFILING.md`](spring-vt/PROFILING.md).)

A single Go load generator drives all three, so you measure the *servers*.

## Test hardware

| | |
|---|---|
| Box | AMD Ryzen 5 7535HS, 13 GiB RAM, Ubuntu Linux |
| Server | pinned to **2 cores** (`taskset -c 2,3`) |
| Loadgen | pinned to cores 4,5 (never steals server CPU) |
| Postgres | local, v16.14 (gets the remaining cores) |
| Runtimes | Go 1.23+ (GOMAXPROCS=2), Rust (2 worker threads), JDK 25 (Corretto) |
| Pool | **32** DB connections, identical for all three |
| Payload | 256 B · client connections 4 (HTTP/2, multiplexed) |

## Quick start

```bash
./scripts/setup_db.sh           # role + db + schema (one time)
./scripts/build_go.sh           # bin/go-server + bin/loadgen (needs protoc)
./scripts/build_rust.sh         # bin/rust-server (needs cargo + protoc)
./scripts/build_spring_vt.sh    # bin/spring-vt-bench.jar (needs JDK 25)

# The benchmark: 30-min sustained soak, one stack at a time, c=64,
# with cleanup + cooldown between stacks:
bash scripts/soak_3stacks.sh
```

---

## How we benchmark — the soak (in detail)

A **soak test** applies **sustained, continuous load at a fixed concurrency for a
long, fixed duration**, to measure *steady-state* behaviour — how the stack holds
up over time, not its cold-start peak. Specifics:

### Concurrency model — closed-loop (this is key to reading the numbers)

We hold **c = 64 concurrent in-flight requests**. The load generator runs 64
"workers"; each worker sends one `Execute` RPC, **waits for the response, then
immediately sends the next** — so there are always exactly 64 requests in flight.

> We do **not** inject a fixed requests/second rate. The **throughput (rps) is the
> *result*** — it's however many requests the server can complete per second while
> 64 are always pending. So "how many requests per second" = the measured rps in
> the results table below (e.g. spring-vt sustained ≈ **11,973 req/s**).

### Duration & volume

- **30 minutes** of continuous measurement **per stack**,
- preceded by a **30-second warmup** that is discarded (lets the JIT, the
  connection pool, and the Postgres page cache reach steady state before we count),
- ⇒ **≈ 18–21 million requests measured per stack** (30 min × ~10–12k req/s).

### Workload

- **`execute`** — one autocommit `INSERT` into the `commands` table per request,
  plus the FNV-1a checksum CPU touch. Payload 256 B. (Same SQL/contract for all
  three stacks; only the driver differs.)

### Isolation & fairness (per stack)

1. **Fresh OS reboot before each stack** — identical, quiet starting conditions.
2. Server **pinned to 2 cores** (`taskset -c 2,3`); the load generator pinned to
   **cores 4,5** so it never competes with the server; Postgres gets the rest.
3. **One stack at a time** — never two servers running together.
4. **pool = 32** DB connections, identical across stacks.

### Cleanup between stacks (so each starts clean)

1. `TRUNCATE` all tables (`RESTART IDENTITY`).
2. **Wait until no (auto)VACUUM worker is active** (poll `pg_stat_activity`) — so
   leftover vacuum from the prior run can't perturb the next one.
3. **Cool down** until the 1-minute load average drops below 2.0.

### Pass criterion

Each run must report **0 errors** for its full 30 minutes; any error halts the
soak for investigation. (All three passed — 0 errors across ~60M requests total.)

**Reproduce:** `bash scripts/soak_3stacks.sh` (reboot between stacks for parity).

---

## Results — 30-minute sustained soak (`execute`, c=64)

Throughput is **requests completed per second** (closed-loop, c=64); latency is
end-to-end per-request, measured over the full 30-minute window.

| stack | **req/s** | p50 | p90 | p99 | p999 | max | requests | errors |
|---|--:|--:|--:|--:|--:|--:|--:|--:|
| **spring-vt** (VT + JdbcClient) | **11,973** | 4.3 | 9.6 | 12.1 | 38.3 | 527 ms | 21.5 M | **0** |
| **rust-tokio** | 11,081 | 5.3 | 9.8 | **11.4** | 43.2 | 700 ms | 19.9 M | **0** |
| **go-pgx** | 10,081 | 5.7 | 9.8 | 12.7 | 45.7 | **3,513 ms** | 18.1 M | **0** |

Per-second throughput sustained over 30 min: **spring-vt ≈ 11,973 req/s, rust-tokio
≈ 11,081 req/s, go-pgx ≈ 10,081 req/s** — all three within ~18% of each other.

### Why the three are so close — it becomes DB-bound

This is the most important thing to understand about the soak. Over 30 minutes of
continuous `INSERT`s, the `commands` table grows to **~18–21 million rows plus its
index**. Once that index no longer fits in the Postgres page cache, **every insert
pays for index maintenance against a large on-disk structure** — and at that point
**Postgres, not the gRPC runtime, is the bottleneck.** So the three runtimes
converge: they're all waiting on the same database.

What still separates them at sustained steady state:

- **rust-tokio — best tail / most consistent** (p99 11.4 ms, max 700 ms).
- **go-pgx — lowest sustained throughput here**, and it ate a **3.5-second** max
  stall (rode out a Postgres checkpoint poorly; pgx's pool health-checks /
  recycling add steady overhead over a long run).
- **spring-vt — highest sustained throughput** (HikariCP at pool 32, no mid-run
  connection churn, on virtual threads).

---

## Verdict (2-core target, workflow-engine hot path)

Under a **sustained 30-minute write soak on a 2-core box, the stack choice barely
moves throughput** — all three land at **10–12k req/s with zero errors**, because
the workload becomes **Postgres-bound** as the table grows. The runtime is not the
wall at steady state; the database is.

Within that close race:

- **spring-vt (virtual threads + Spring JdbcClient) — the pragmatic pick.** It
  **sustained the highest throughput** (11,973 req/s), with simple blocking code,
  mainstream JVM hiring, and clean ergonomics. For a JVM-centric org this is the
  low-risk choice — stay on virtual threads + `JdbcClient` (not reactive, not
  JPA/Exposed).
- **rust-tokio — pick it for the tail.** Lowest, most predictable latency
  (p99 11.4 ms, max 700 ms) and no multi-second stalls — best if jitter and
  worst-case latency matter most, and the team can carry Rust.
- **go-pgx — solid but the weakest here.** Lowest sustained throughput and a
  3.5-second stall in this run.

**The bigger lever is the database, not the language.** Because sustained write
throughput is Postgres-bound on this shape, the highest-impact work is on the DB
design — partitioning / retention for append tables, or a checkpoint-style state
model (bounded, HOT-updated rows) that keeps the working set cache-resident. Get
that right and any of these three runtimes will serve the engine well.

## Caveats (read before trusting any single number)

- **n = 1 per stack** — one 30-minute run each. The spread (≤18%) is partly
  Postgres/disk, not pure runtime.
- **Non-dedicated box** — expect run-to-run variance; each soak ran on a fresh
  reboot to equalize conditions.
- **DB-bound by design** — these are *sustained* numbers on a single, growing
  table; they measure steady state, which here is dominated by Postgres.
- spring-vt runs its tuned config (pool 32, epoll, 1 I/O thread, 2 GB heap,
  JdbcClient); reproduce via `scripts/soak_3stacks.sh` and `spring-vt/PROFILING.md`.

## Layout

```
proto/command.proto     shared gRPC contract
sql/schema.sql          commands + workflow_state + outbox
go-pgx/                 Go server
rust-tokio/             Rust server (modular src/)
spring-vt/              Spring Boot gRPC + virtual threads + JdbcClient  (+ PROFILING.md)
loadgen/                shared Go load generator
scripts/                build_* , soak_3stacks.sh , run_benchmark.sh
results/                JSON + summary.csv per run
```
