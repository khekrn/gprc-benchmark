# spring-vt — profiling & optimization methodology

This documents **exactly** how the spring-vt optimization work is profiled and
analyzed, so the results can be independently validated. Goal: maximize absolute
throughput + minimize latency on the **2-core / 4 GB** target, profile-first
(only apply a change if the profile or a controlled A/B benchmark justifies it).

Everything here is reproducible via two committed scripts:
- `scripts/profile_spring_vt.sh` — captures a profile under load.
- `scripts/analyze_profile.py` — summarizes a collapsed profile (transparent; no hidden weighting).

## 1. Environment & tooling

| | |
|---|---|
| Box | AMD Ryzen 5 7535HS, 12 logical cores, Ubuntu (Linux 7.0.0) |
| Server cores | pinned to **2** via `taskset -c 2,3` (simulates the 2-core target) |
| Loadgen cores | `taskset -c 4,5` — separate cores, never steals server CPU |
| JDK | 25.0.3 (Corretto), spring-vt fat jar (`bin/spring-vt-bench.jar`) |
| Profiler | **async-profiler 4.3** (`asprof` CLI) |
| Driver | the shared Go closed-loop loadgen (`bin/loadgen`) — same one the benchmark uses |
| DB | local Postgres; tables `TRUNCATE`d before every run |

### Why `itimer` / `wall` / `alloc` and not `-e cpu`/perf

This box has `/proc/sys/kernel/perf_event_paranoid = 4`, so the kernel denies
`perf_event_open` to non-root. async-profiler's `cpu` engine needs perf_events,
so it would fail. We use the engines that need **no** perf_events:

| Event | What it samples | Answers |
|---|---|---|
| `itimer` | **on-CPU** time (POSIX `setitimer`/SIGPROF, per thread) | *Where do CPU cycles burn?* (the binding constraint on 2 cores) |
| `wall` | **wall-clock** across all threads, incl. **blocked/off-CPU** | *Where do we wait?* (lock/pool/IO waits — e.g. HikariCP) |
| `alloc` | heap **allocation** sites (JVMTI sampling) | *What drives GC pressure?* |

`itimer` answers "what's the CPU bottleneck"; `wall` is what reveals whether
**HikariCP is blocking** (a connection-starved handler shows up parked in
`HikariPool.getConnection` / `ConcurrentBag.borrow`, which `itimer` cannot see
because a blocked thread burns no CPU).

`-XX:+EnableDynamicAgentLoading` is added to the server JVM so async-profiler can
attach at runtime without the JDK dynamic-agent warning.

## 2. Workload profiled

`execute` (single autocommit INSERT) at **c=64** — a saturated-but-not-overloaded
point that maximizes server CPU without collapsing into pure queueing. The same
method is repeated for `read` and at c=128 where relevant.

## 3. Exact commands

### 3.1 One-shot reproducible capture (preferred)

```bash
# CPU (on-CPU) profile of execute @ c=64, 30s window:
scripts/profile_spring_vt.sh itimer execute 64 30
# Wall-clock (shows blocked time — the HikariCP test):
scripts/profile_spring_vt.sh wall   execute 64 30
# Allocation profile:
scripts/profile_spring_vt.sh alloc  execute 64 30

# Param-matrix variant — override the JVM and/or pool via env:
JVM_OPTS="-Xms512m -Xmx1024m -XX:+UseG1GC -XX:+AlwaysPreTouch" PG_POOL_MAX=32 \
  scripts/profile_spring_vt.sh itimer execute 128 30
```

Each run writes to `results/profile-spring-vt-<ts>/`:
`<event>.collapsed` (folded stacks), `<event>.html` (openable flamegraph),
`load.json` (throughput/latency during the capture), `server.log`, `params.txt`.

### 3.2 What the script does, step by step (verbatim core)

```bash
# 1. start server, pinned, with the chosen JVM_OPTS + attach enabled
taskset -c 2,3 java $JVM_OPTS -XX:+EnableDynamicAgentLoading \
  -jar bin/spring-vt-bench.jar &
PID=$!
# 2. drive steady-state load on separate cores
taskset -c 4,5 bin/loadgen -addr 127.0.0.1:50056 -c 64 -d 68s -warmup 0s \
  -payload 256 -conns 4 -mode execute -out load.json &
# 3. let it reach steady state, then sample
sleep 15
asprof -d 30 -e itimer -o collapsed  -f itimer.collapsed $PID   # for analysis
asprof -d 30 -e itimer -o flamegraph -f itimer.html      $PID   # for eyeballing
```

The very first baseline capture (before any change) was run with the shipped
config — `JVM_OPTS="-Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch"`,
`PG_POOL_MAX=16`.

## 4. How the collapsed profile is analyzed

async-profiler `-o collapsed` emits one line per unique stack:
`frameA;frameB;...;leaf <sampleCount>`. The **leaf** is where the sample landed,
so:

- **Self time** = sum sample counts grouped by leaf frame.
- **Subsystem share** = sum sample counts for every stack that *touches* a
  subsystem (matched by substrings: `io.netty`, `protobuf`, `org.postgresql`,
  `com.zaxxer.hikari`/`ConcurrentBag`, ZGC/`Z*`, `VirtualThread`/`Continuation`,
  `com.beam.bench`/`Fnv`, …).

`scripts/analyze_profile.py <file.collapsed>` prints both. The substring table is
in the script header in plain sight — adjust it if a frame is miscategorised. You
can also open `<event>.html` in a browser and cross-check the flamegraph directly
against these numbers.

## 5. The HikariCP "is the pool the bottleneck?" test — two independent methods

1. **Wall-clock profile** (`wall` event): if a large share of wall time sits in
   `HikariPool.getConnection` → `ConcurrentBag.borrow` → `park`, handlers are
   **starved for connections** (pool-bound). If that share is small, the pool is
   not the wall.
2. **Pool-size sweep** (empirical): run the same `execute`/`read` c-sweep at
   `PG_POOL_MAX = 16 / 32 / 64 / 96`. If throughput **rises** with pool size →
   pool-bound; if it's **flat or worse** → CPU/DB-bound (more connections just add
   contention). Sanity ceiling: with ~`q` ms per query and `N` connections, the
   DB-bound limit is ~`N/q` qps (e.g. 16 conns × 0.3 ms ⇒ ~53k qps, well above the
   observed ~15k — a first hint the pool is *not* the limiter, to be confirmed).

Both must agree before we conclude.

## 6. JVM-parameter matrix

Each candidate is A/B-benchmarked with the **full** `run_benchmark.sh` sweep
(`execute` + `read`, c=1..128, 15s/60s) against the ZGC baseline; keep only wins.

- **GC:** ZGC (baseline) vs **G1** vs **Parallel** vs ZGC-generational — on a
  2-core box ZGC's concurrent barriers/threads compete with request work, so a
  throughput collector may win.
- **`-XX:+UnlockExperimentalVMOptions -XX:+UseCompactObjectHeaders`** (JDK 25,
  experimental): 16→8 byte object headers ⇒ better cache density + less alloc
  footprint.
- **Pool size** (from §5).
- Always-on baseline: `-XX:+AlwaysPreTouch`, `-Xms=-Xmx` (no heap resize jitter).

Per-stack JVM opts are passed via the `JVM_OPTS` env (config.sh default is shared;
for spring-vt-only experiments we export `JVM_OPTS=...` before the run).

## 7. Candidate code/config changes (profile-gated)

Applied only if the profile/benchmark supports them:
- **Netty server tuning** in `GrpcServerConfig` — 1 MiB HTTP/2 flow-control
  windows, `TCP_NODELAY`, server keepalive (the Go server & spring-kt-vt have
  these; spring-vt currently sets only the VT executor).
- **Per-RPC allocation cuts** — FNV over the payload without a `getBytes(UTF_8)`
  copy; cheaper receive-timestamp than `Instant.now()`.
- **Netty epoll** native transport (fewer syscalls than NIO on Linux).
- **Disable per-RPC observability** (Micrometer/gRPC observation) if present.

## 8. Validation checklist (how *you* can re-verify any claim here)

1. Re-run a capture: `scripts/profile_spring_vt.sh itimer execute 64 30`.
2. Open `results/profile-spring-vt-<ts>/itimer.html` in a browser — the flamegraph
   is the raw evidence; the subsystem % in §9 must match what you see.
3. Re-run the analysis on the collapsed file:
   `python3 scripts/analyze_profile.py <dir>/itimer.collapsed`.
4. For any "X made it faster" claim, the two run dirs (before/after) are kept under
   `results/`, each with `load.json` (rps + p99) and `params.txt` (exact JVM/pool).

## 9. Results log

> Filled in as each step completes — each row links the run dir so it can be reopened.

### Baseline (ZGC, pool=16, execute c=64) — 2026-06-24
- Throughput during capture: **13,739 rps**, 0 errors (profiler attached costs a little vs the ~14.9k clean peak).
- **CPU (itimer) subsystem breakdown** (5,922 samples):
  | share | subsystem |
  |--:|---|
  | **42.0%** | Netty |
  | **30.5%** | gRPC |
  | 13.4% | other |
  | 9.9% | VT/scheduler |
  | 2.6% | GC (ZGC) |
  | 0.8% | protobuf |
  | **0.1%** | HikariCP |
  | **0.0%** | pgjdbc |
- **Top self frames:** `[vdso]` 34.4% + `__syscall_cancel_arch_end` 16.3% +
  `pthread_cond_signal` 6.3% ⇒ **~57% of on-CPU time is syscalls + cross-thread
  wakeups**, not compute. `Fnv.fnv1a32` 0.4%, `String.<init>` 0.4%.
- **Verdict:** on 2 cores the wall is the **gRPC/Netty transport + the Netty
  event-loop ↔ virtual-thread handoff** (futex/`pthread_cond_signal`, syscalls,
  clock reads via `[vdso]`). **GC is only 2.6%** (so a GC swap is low-value here),
  and **pgjdbc/HikariCP are ~0% CPU** (the pool is *not* a CPU bottleneck — the
  wall-clock profile in §5 will check whether it's a *blocking* bottleneck).
- This reprioritizes the plan: **epoll native transport, HTTP/2 flow-control +
  TCP_NODELAY, fewer clock reads, and reducing the event-loop↔VT handoff** are the
  high-value levers; GC tuning is demoted.
- Artifacts: `/tmp/svt-cpu.collapsed` (re-run via `scripts/profile_spring_vt.sh itimer execute 64 30`).

### Pool-size sweep
- _pending_

### JVM-param matrix
- _pending_

### HikariCP pool-size sweep (2026-06-24, `scripts/ab_pool_spring_vt.sh`) — CORRECTS an earlier claim
Pool 16/32/64 at c=64/128. **Bumping the pool 16→64 raises throughput +30%
(execute c=64: 14.6k→19.0k) / +18% (read c=64), with better p99.** This
**overturns the earlier "pool is not a bottleneck" read**: HikariCP's *code* is
~0% CPU (cheap), but the pool *size* caps how many requests can be in Postgres
at once — at c=64 with 16 connections, ~48 requests park in `getConnection`.
That's a **concurrency limiter, not a CPU one**, invisible to the itimer (on-CPU)
profile — which is exactly why the sweep (or a wall-clock profile) was needed.
**Decision: the final 3-stack run uses pool=32** (applied equally to go/rust/svt) —
captures most of the gain (+22% execute @c64) at half the connections of 64, a more
production-realistic count for a 2-core node.

### JVM-param matrix (2026-06-24, `scripts/ab_jvm_matrix_spring_vt.sh`)
ZGC vs G1 vs Parallel vs ZGC+CompactObjectHeaders, execute+read, c=32/64/128,
interleaved. Differences are mostly within ±5% (confirms GC ≈ 2.6% of CPU — not
the bottleneck), with two clear signals:
- **Parallel GC collapses on the write tail** (execute c=128: 9,635 rps / p99
  37.8 ms vs ZGC 15,384 / 18.7) — stop-the-world pauses. Disqualified.
- **Compact object headers ≈ ZGC** (no measurable gain) — alloc/GC isn't the wall.
- G1 marginally wins execute mid-range (c=32/64, ~+2–4%) but loses the execute
  c=128 tail; Parallel wins read mid-range slightly. No config beats ZGC by enough
  to switch.
**Decision: keep ZGC** — best/most-consistent tails, never collapses; compact
headers and a GC swap are not worth adopting.

### REST API coexistence — Tomcat vs grpc-netty (verified 2026-06-24)
Empirically confirmed (temporarily added `spring-boot-starter-web` + a
`@RestController`, built to `target/`, then reverted): a REST API in a Spring
gRPC app runs on **Tomcat** (`Apache Tomcat/11.0.22`) on its **own HTTP port
(8080)** — a **separate server** from the **grpc-netty** server (port 50056).
Both come up in one JVM. With `spring.threads.virtual.enabled=true`, **Tomcat
also dispatches REST handlers onto virtual threads** (`tomcat-handler-*` runs as
a `VirtualThread`). So gRPC stays on grpc-netty; REST does **not** share that
Netty — it's Tomcat, just also on Loom. (WebFlux would instead give Reactor-Netty
for REST — reactive, still a separate server/port.)

### Applied changes & net effect (2026-06-24, controlled same-session A/Bs)
- **epoll vs NIO** (`scripts/ab_spring_vt.sh`, interleaved per (mode,c)): epoll wins
  **~+3–5%** in the loaded range (c≥8) with better p99 at c=8 and c=128-read
  (19.0→17.6 ms); ±2–3% noise at c=1. **KEPT** as default (NIO via
  `NETTY_TRANSPORT=nio`). NOTE: the earlier *cross-day* table (+43% / −10%) was
  box drift — only the same-session A/B is trustworthy.
- **Micrometer observation ON vs OFF** (`scripts/ab_obs_spring_vt.sh`, 2 GB heap,
  epoll): **no measurable effect** — all deltas within ±2–6% run-to-run noise,
  sign inconsistent. The `ObservationRegistry` is effectively no-op without a
  metrics/tracing backend, so `spring.grpc.server.observation.enabled` is **left
  ON** (it's free here and useful in prod). Hypothesis (observation = prime CPU
  sink) **disproven** — ruled out.
- **2 GB heap** (was 1 GB): adopted for spring-vt tuning runs (ZGC headroom →
  smoother tail); no throughput regression.
- **Netty I/O threads: 1 vs 2** (`scripts/ab_netty_threads_spring_vt.sh`, epoll,
  2 GB): **1 thread wins clearly** — up to **+14%** rps (read c=8) and **+13.9%**
  at execute c=128 with **p99 21.2→14.8 ms (−30%)**. Handlers run on virtual
  threads, so the event loop is pure socket I/O; a single loop removes
  cross-event-loop contention + halves the wakeup churn (the profile's dominant
  cost). **Adopted:** default I/O threads = `max(1, cores/2)` (→ 1 on 2 cores,
  2 on 4 cores), overridable via `NETTY_IO_THREADS`.
- **NET RESULT so far (epoll + 1 I/O thread + 2 GB):** spring-vt peaks
  **execute ~17.7k / read ~20.3k**, i.e. **~matches go-pgx (18.0k / 20.6k)** on
  the 2-core box — the single I/O thread was the missing piece. (40s/10s windows,
  cross-run vs go-pgx — directional, but the jump is real and consistent.) The
  earlier "structural gap" framing was too pessimistic: trimming the *number* of
  event loops directly attacked the handoff cost.
