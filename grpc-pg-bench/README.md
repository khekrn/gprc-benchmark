# gRPC + Postgres Benchmark — Go vs Rust vs Spring Boot (virtual threads)

Functionally identical gRPC services that unmarshal a command, do a tiny
CPU touch (FNV-1a checksum), and `INSERT` it into Postgres. The goal is to pick a
stack for a **next-gen distributed workflow engine**, judged on **sustained
throughput and latency** on a **2-core** box — the shape of a small cloud node
(memory is cheap on AWS; compute is the cost that matters).

The core comparison is **three runtimes** (Go, Rust, Spring Boot on virtual
threads). A fourth stack, **spring-data-jdbc**, is spring-vt with only the data
layer swapped (Spring Data JDBC repositories instead of `JdbcClient`), so it
isolates the **cost of that ORM-lite abstraction** on the same hot path.

The benchmark here is a **30-minute sustained soak** (one stack at a time, on a
fresh boot). That's the number that reflects how an always-on engine actually
behaves under continuous load over time.

> A broader 8-stack exploration is archived in
> [`README-archive-8stacks.md`](README-archive-8stacks.md). This document is the
> focused **3-stack soak** (plus data-layer/ORM variants — **spring-data-jdbc**,
> **go-gorm**, **spring-rt** — compared against the raw-driver baselines).

## The stacks

- **go-pgx** — Go + `google.golang.org/grpc` + `jackc/pgx` (pgxpool). The native
  baseline: goroutines, graceful shutdown, keepalive, tuned pool.
- **go-gorm** — go-pgx with the data layer swapped to the **GORM ORM**
  (`gorm.io/gorm`), which uses **jackc/pgx under the hood** (`gorm.io/driver/postgres`
  → pgx stdlib). Same gRPC/transport/SQL/pgx wire as go-pgx; only `Create()`/
  `Transaction()` replace hand-written `QueryRow`. The Go "ORM cost" data point vs
  go-pgx — the mirror of spring-data-jdbc vs spring-vt.
- **rust-tokio** — Rust + `tonic` + `tokio-postgres` + `deadpool`. Async/native;
  tonic's HTTP/2 transport tuned like go (1 MiB windows, `tcp_nodelay`, keepalive).
- **spring-vt** — Spring Boot 4.1 gRPC + **virtual threads** + **Spring
  `JdbcClient`** over HikariCP. Blocking code on Loom (no reactive). Config: epoll
  transport, a single Netty I/O thread, ZGC + 2 GB heap, pool 32. (How that config
  was arrived at — profiling + A/Bs — is in [`spring-vt/PROFILING.md`](spring-vt/PROFILING.md).)
  It also co-hosts a **REST `/health` endpoint on Jetty** in the same JVM — see
  [Co-hosting REST + gRPC](#co-hosting-rest--grpc-in-one-service-spring-vt).
- **spring-data-jdbc** — spring-vt with the data layer swapped to **Spring Data
  JDBC** repositories (entities + `CrudRepository` + a `@Modifying` upsert).
  Everything else identical: same VT model, same HikariCP pool (32), same
  epoll/heap/SQL/contract. The abstraction-cost data point vs `JdbcClient`.
- **spring-rt** — Spring Boot 4.1 gRPC + **Kotlin coroutines** + **Spring Data
  R2DBC** (`CoroutineCrudRepository`) — fully reactive/non-blocking. Tuned with
  the same playbook as spring-vt *adapted for reactive*: epoll + a **direct
  executor** (NOT a virtual-thread executor — reactive stays on the event loop),
  1 MiB windows/keepalive, ZGC + compact object headers (JEP 519). The reactive
  counterpoint to the blocking-on-VT stacks.

A single Go load generator drives them all, so you measure the *servers*.

## Test hardware

| | |
|---|---|
| Box | AMD Ryzen 5 7535HS, 13 GiB RAM, Ubuntu Linux |
| Server | pinned to **2 cores** (`taskset -c 2,3`) |
| Loadgen | pinned to cores 4,5 (never steals server CPU) |
| Postgres | local, v16.14 (gets the remaining cores) |
| Runtimes | Go 1.23+ (GOMAXPROCS=2), Rust (2 worker threads), JDK 25 (Corretto) |
| Pool | **32** DB connections, identical across stacks |
| Payload | 256 B · client connections 4 (HTTP/2, multiplexed) |

## Quick start

```bash
./scripts/setup_db.sh           # role + db + schema (one time)
./scripts/build_go.sh           # bin/go-server + bin/loadgen (needs protoc)
./scripts/build_rust.sh         # bin/rust-server (needs cargo + protoc)
./scripts/build_spring_vt.sh    # bin/spring-vt-bench.jar (needs JDK 25)
./scripts/build_spring_data_jdbc.sh  # bin/spring-data-jdbc-bench.jar (needs JDK 25)

# The benchmark: 30-min sustained soak, one stack at a time, c=64,
# with cleanup + cooldown between stacks (STACKS= picks which to run):
STACKS="go-pgx rust-tokio spring-vt spring-data-jdbc" bash scripts/soak_3stacks.sh
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
  stacks; only the driver / data layer differs.)

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
| **spring-data-jdbc** (VT + Spring Data JDBC) † | 7,373 | 8.5 | 11.5 | 15.1 | 35.6 | 650 ms | 13.3 M | **0** |
| **go-gorm** (GORM ORM over pgx) | 5,767 | 10.1 | 16.6 | 26.0 | 49.7 | 330 ms | 10.4 M | **0** |
| **spring-rt** (coroutines + Spring Data R2DBC) ‡ | ~4,828 | 11 | 24 | 38 | — | 87 ms | _A/B_ | **0** |

Per-second throughput sustained over 30 min: **spring-vt ≈ 11,973 req/s, rust-tokio
≈ 11,081 req/s, go-pgx ≈ 10,081 req/s** — the three core runtimes within ~18% of
each other. The ORM layers trail: **spring-data-jdbc ≈ 7,373** and **go-gorm ≈
5,767 req/s** (the two repository/ORM abstractions), and **spring-rt furthest at
≈ 4,828 req/s** (the reactive double-event-loop tax on 2 cores). All measured the
same DB-bound `execute` path — the gap is the data layer, not the runtime.

> † spring-data-jdbc was measured later, in its own fresh-boot 30-min `execute`
> c=64 soak on the same box and identical config (pool 32, ZGC + 2 GB heap, epoll).
> The run was reproducible (a second fresh-boot soak gave 7,172 req/s, &lt;3% apart),
> 0 errors over 13.3 M requests. Loadavg spiked late in the run from background
> kernel/desktop noise on this non-dedicated box, but it did not perturb the result
> (tails stayed tight: p99 15 ms, max 650 ms).
>
> ‡ spring-rt has **no 30-min soak yet** — this row is a **60-second same-session
> A/B** (c=64, pool 32, tuned: epoll + direct executor + compact headers), 2 reps
> averaged (4,573 & 5,083 req/s), 0 errors. It's the slowest stack here and the gap
> is **structural**: each reactive `save()` is a 3-round-trip transaction, and on 2
> cores the reactive cross-event-loop handoff latency exceeds virtual-thread
> park/unpark — confirmed by profiling (`itimer`/`wall`/`alloc`). Tuning lifted it
> +28% over the un-tuned baseline (~3,757 req/s) but can't close the structural gap.
> A 30-min soak will replace this A/B number.

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

### Spring Data JDBC vs JdbcClient — the abstraction cost

spring-data-jdbc is spring-vt with **one** thing changed: Spring's fluent
`JdbcClient` is replaced by the **Spring Data JDBC repository abstraction**
(entities + `CrudRepository.save()`). Same gRPC contract, same SQL, same HikariCP
pool, same virtual-thread + epoll + heap config. So the gap is purely what the
repository layer costs.

It is large. Over the 30-min soak, **7,373 vs 11,973 req/s — ~38% lower
throughput** than `JdbcClient`. In a back-to-back **60-second same-session A/B on a
small (runtime-bound) table**, where the DB is *not* yet the wall, the gap is even
starker: **7,657 vs 19,264 req/s (~60% lower)**. The soak compresses the gap
because both stacks spend the back half waiting on the same growing database; the
A/B exposes the abstraction's raw CPU + round-trip cost.

Why it's slower:

- **`CrudRepository.save()` is `@Transactional` by default.** Even the "autocommit"
  `Execute` insert becomes **BEGIN + INSERT + COMMIT — three DB round-trips** where
  `JdbcClient`'s autocommit insert is **one**. That alone roughly triples the
  per-insert DB chatter, and round-trips dominate on this hot path.
- **Entity mapping CPU.** Each call pays reflection / `RowMapper` / immutable-record
  reconstruction to turn a row into an entity and back — cheap individually, but it
  bites at 2 cores under sustained load.
- The UPSERT can't even use `save()` (Spring Data JDBC only INSERTs *or* UPDATEs),
  so the conflict path is a native `@Modifying @Query` regardless — you take the
  abstraction's overhead without it doing the hard part for you.

**Takeaway:** on a write-hot, latency-sensitive path at low core count, prefer
`JdbcClient` (spring-vt) over Spring Data JDBC repositories. Spring Data JDBC earns
its keep where mapping richness and developer velocity matter more than peak write
throughput — not on this engine's hot path.

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
- **spring-data-jdbc — an abstraction tax, not a runtime one.** Same virtual-thread
  Spring Boot as spring-vt, but the Spring Data JDBC repository layer cost it ~38%
  throughput (7,373 vs 11,973 req/s) by turning every autocommit insert into a
  3-round-trip transaction. If you go Spring + VT, use `JdbcClient`, not Spring Data
  JDBC repositories, on the write hot path.
- **go-gorm — the same lesson on the Go side, and the steepest tax in the suite.**
  Same goroutines + pgx wire as go-pgx, but the GORM ORM cost it **~43%** (5,767 vs
  10,081 req/s) — GORM's default per-`Create` transaction (BEGIN/COMMIT) plus
  reflection-based mapping. It even trails the JVM ORM (spring-data-jdbc 7,373). On
  a write-hot Go service, reach for `pgx` directly, not GORM.
- **spring-rt — last place, and it's the *model*, not just the abstraction.**
  Reactive (coroutines + Spring Data R2DBC), fully tuned, still lands ≈4,828 req/s
  (A/B). On 2 cores the reactive double-event-loop handoff costs more per op than a
  virtual thread parking on blocking I/O — so reactive is the wrong tool for this
  sub-millisecond-query, low-core-count shape. Profiling confirmed it's I/O-wait /
  scheduling bound; tuning helped +28% but can't change the model's ceiling.

**The bigger lever is the database, not the language.** Because sustained write
throughput is Postgres-bound on this shape, the highest-impact work is on the DB
design — partitioning / retention for append tables, or a checkpoint-style state
model (bounded, HOT-updated rows) that keeps the working set cache-resident. Get
that right and any of these three runtimes will serve the engine well.

## Co-hosting REST + gRPC in one service (spring-vt)

A workflow engine usually needs an HTTP surface too (k8s probes, admin/ops
endpoints) next to its gRPC hot path. So we gave spring-vt a **second network
stack in the same JVM**: a Spring MVC REST server on **Jetty** (Tomcat excluded —
lighter, fewer threads) exposing a plain `GET /health` → `200` liveness endpoint,
co-hosted with the existing grpc-netty server. The question: on a 2-core box, does
running two servers in one process starve either one?

We re-ran the 30-min `execute` soak (c=64, pool=32) with spring-vt now serving
**both** gRPC (:50056) and REST (:8080), while an **external** probe pinged
`/health` every 5 s from a third core pair (a bystander to both server and
loadgen). This run also adopted a production-shaped JVM tune (`-Xmx2304m`, ZGC
`ConcGCThreads=1`, `+UseCompactObjectHeaders`, `MaxDirectMemorySize=768m`, pooled
Netty buffers), so the delta below reflects the second server **and** the JVM
change together.

| metric | gRPC only (baseline) | **+ REST co-hosted** | Δ |
|--------|---------------------:|---------------------:|----:|
| gRPC req/s | 11,171 | **10,934** | **−2.1%** |
| gRPC p99 | 12.01 ms | 11.96 ms | flat |
| gRPC p99.9 | 54.5 ms | **39.7 ms** | −27% |
| gRPC errors | 0 | **0** | — |
| `/health` p50 / p99 / max | — | **4.0 / 11.5 / 24 ms** | — |
| `/health` non-200 under load | — | **0** | — |
| total platform threads | ~33 | **36** | +~3 |

**Co-hosting is cheap here.** Throughput cost was ~2% (and that conflates the JVM
re-tune; the GC change actually *improved* the tail — p99.9 dropped 27%). Under
full gRPC saturation (both cores ~187% busy, 10,934 req/s), the REST `/health`
probe stayed at single-digit-ms p50 with a p99 of 11.5 ms and **zero stalls** —
the two servers don't starve each other. The thread count barely moved: with
virtual threads on, Jetty routes requests through its `VirtualThreadPool`, so it
adds only a `MasterPoller` + acceptor (~3 platform threads), versus Tomcat's
default 200-thread ceiling. The gRPC server staying at ~187% (not pinned 200%)
confirms `execute` remains DB-round-trip bound, not CPU-walled, even with the
second stack present.

*Reproduce:* build spring-vt, then `STACKS=spring-vt bash scripts/soak_3stacks.sh`
(serves both servers) with `bash scripts/health_ping.sh` running alongside.

### Chosen JVM tune (and the cache-miss / context-switch evidence)

The co-host run uses a production-shaped, spring-vt-specific JVM tune
(`SPRING_VT_JVM_OPTS` in `scripts/config.sh`):

```
-Xms2304m -Xmx2304m            # fixed heap; with 768m direct ≈ 3 GB, ~1 GB left for OS/native
-XX:+UseZGC -XX:ConcGCThreads=1 # ZGC capped to 1 concurrent thread — protects the 2 vCPU
-XX:MaxDirectMemorySize=768m   # bound Netty's pooled direct buffers (both servers)
-XX:+AlwaysPreTouch            # front-load page faults so phase 1 isn't taxed by lazy commit
-XX:+UseCompactObjectHeaders   # JEP 519, 8-byte vs 12-byte headers — smaller cache footprint
-Dio.netty.allocator.type=pooled
```

Plus **1 Netty I/O thread** — set in `GrpcServerConfig`, not via a JVM flag.

> **"1 I/O thread" is a Netty knob, not a GC knob — they are different things.**
> This JVM runs several distinct thread pools; the two we tuned to "1" are unrelated:
>
> - **Netty I/O (worker) event-loop thread** — `NETTY_IO_THREADS`, defaults to
>   `cores/2` = **1** on the 2-core pin. `GrpcServerConfig` builds the
>   `MultiThreadIoEventLoopGroup` *worker* group with this size; it does socket I/O
>   + HTTP/2 framing. (A separate **boss** group is always 1, for accepting
>   connections.) This is the thread the A/B's `io2` cell doubled to 2.
> - **ZGC concurrent GC thread** — `-XX:ConcGCThreads=1`, the garbage collector's
>   concurrent worker count, capped to 1 so GC can't grab both vCPUs. This is a
>   *JVM/GC* setting and was **held constant across all four A/B cells** — the A/B
>   never varied GC.
> - **Request handlers** run on **virtual threads** (not a fixed pool), and the
>   co-hosted **Jetty** REST server adds only a `MasterPoller` + acceptor (~3
>   platform threads). Neither is the "I/O thread" referred to here.
>
> So "1 I/O thread vs 2" below means **1 vs 2 Netty worker event loops**, with GC
> (`ConcGCThreads=1`) and everything else identical.

We verified the two cache-relevant choices with a hardware-counter A/B (`perf stat`,
matched ~18k rps, repeat cell agrees to 0.25% so >0.5% deltas are real —
[`results/perf-cohost2-*/FINDINGS.md`](results/perf-cohost2-20260625-224525/FINDINGS.md)):

| setting | vs alternative | cache-misses / instr | IPC | context switches |
|---|---|--:|--:|--:|
| **compact headers ON** (GC/heap) | vs OFF | **−2.0%** (L1 −3.1%) | **+1.0%** | flat |
| **1 Netty I/O thread** | vs 2 | **−10%** (80→73 M/s) | **+6%** (0.45→0.48) | flat |

So the kept config is the one with the **fewest cache misses and best IPC**:
compact headers ON + 1 Netty I/O thread (GC stays at `ConcGCThreads=1` throughout).
Two corrections the data forced: (1) the 1-Netty-I/O-thread advantage is **cache
locality**, not "fewer context switches" — a 2nd event loop bounces connection/buffer
state between the two cores' caches (lower IPC, more misses), while context switches
barely move; (2) the context-switch rate (~18k/s) is **workload-governed** — driven by
virtual-thread park/unpark on the blocking JDBC round-trips, unchanged by either knob.
On this DB-bound path the efficiency gains don't widen rps (Postgres is the wall), but
they're the right defaults for a more CPU-bound or higher-core deployment.

## Caveats (read before trusting any single number)

- **n = 1 per stack** — one 30-minute run each (spring-data-jdbc has n = 2,
  &lt;3% apart). The spread (≤18% among the core three) is partly Postgres/disk,
  not pure runtime.
- **Non-dedicated box** — expect run-to-run variance; each soak ran on a fresh
  reboot to equalize conditions. spring-data-jdbc's runs saw a late-run loadavg
  spike from background OS/desktop activity (not the ACPI/`kacpi` storm seen on an
  earlier boot); it left the measured tails clean, so the number stands.
- **DB-bound by design** — these are *sustained* numbers on a single, growing
  table; they measure steady state, which here is dominated by Postgres.
- spring-vt runs its tuned config (pool 32, epoll, 1 I/O thread, 2 GB heap,
  JdbcClient); reproduce via `scripts/soak_3stacks.sh` and `spring-vt/PROFILING.md`.
- spring-data-jdbc reuses spring-vt's exact config and only swaps the data layer;
  reproduce via `STACKS=spring-data-jdbc bash scripts/soak_3stacks.sh`.

## Layout

```
proto/command.proto     shared gRPC contract
sql/schema.sql          commands + workflow_state + outbox
go-pgx/                 Go server (raw pgx)
go-gorm/                Go server, GORM ORM over pgx (modular: config/db/service/server/...)
rust-tokio/             Rust server (modular src/)
spring-vt/              Spring Boot gRPC + virtual threads + JdbcClient + Jetty REST /health  (+ PROFILING.md)
spring-data-jdbc/       Spring Boot gRPC + virtual threads + Spring Data JDBC repositories
loadgen/                shared Go load generator
scripts/                build_* , soak_3stacks.sh , run_benchmark.sh , health_ping.sh
results/                JSON + summary.csv per run
```
