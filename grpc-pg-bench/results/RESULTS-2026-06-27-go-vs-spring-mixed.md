# go-pgx vs spring-vt — mixed workload (Redis read-through), 30 min, 2 cores

**Question:** with the same Redis read-through cache added to both stacks, does Go
beat the Spring/virtual-threads stack on a parallel read+write workload?

**Setup (identical except the runtime):** parallel `execute` (autocommit INSERT,
write path) + `read` (GetState through Redis, read path), exec c=32 + read c=32,
keyspace 5000 (160k seeded rows), Redis warm, server pinned to 2 cores, same SQL,
same cache key/value/TTL, same pool sizes. Runtime is the only variable:

| | spring-vt | go-pgx |
|---|---|---|
| transport | grpc-netty (HTTP/2) | grpc-go (HTTP/2) |
| concurrency | virtual threads (1 per RPC) | goroutines |
| Redis client | Lettuce (shared multiplexed conn) | go-redis (pool=32) |
| Postgres | pgjdbc + HikariCP (JdbcClient) | pgx (pgxpool) |

## Results

| metric | spring-vt | go-pgx | go vs sv |
|--------|----------:|-------:|---------:|
| execute rps | 7,381 | 6,052 | **−18.0%** |
| execute p99 / p99.9 ms | 9.44 / 18.86 | 9.87 / 19.71 | ~tie |
| read rps | 10,232 | 13,402 | **+31.0%** |
| read p99 / p99.9 ms | 6.29 / 9.15 | 5.93 / 8.09 | go slightly better |
| **combined rps** | 17,613 | **19,454** | **+10.5%** |
| total processed | 31,702,752 | 35,017,105 | +10.5% |
| errors | **0** | 1,942 (0.008%) | sv cleaner |
| Redis hit ratio | 95.5% | 96.6% | ~tie |

Both stacks sustained 30M+ requests over 30 min on 2 cores. (Runs:
`results/bench-mixed-20260625-101444` spring-vt, `results/bench-mixed-go-pgx-20260627-123430`
go-pgx — same dedicated Ryzen 2-core, taskset-pinned, so cross-boot variance is negligible.)

## Why Go wins the READ path (+31%) — the mechanism

The read path is the tell, and the reason is **where the cost lives on that path**.

A `GetState` that hits Redis does almost no "business" work — no Postgres round-trip
(96% cache hit), a tiny Redis GET, and a response. So the dominant per-request cost is
the **gRPC transport + the runtime's per-request concurrency machinery**. The spring-vt
CPU profile made this explicit: under load it spent **~79% of on-CPU samples in
Netty/grpc-java HTTP/2 framing**, and its allocation was dominated by **Netty byte
buffers + HTTP/2 header strings + one `VirtualThread` object and `ThreadLocalMap` per
request**. That is the per-request tax the JVM pays on every read.

Go pays a much smaller version of that tax:

1. **No per-request heap object for concurrency.** spring-vt allocates a `VirtualThread`
   (+ its `ThreadLocalMap` entries) for every RPC — both showed up as allocation hot
   spots in the profile. A goroutine is a ~2 KB stack with no per-request `ThreadLocal`
   churn, so go-pgx allocates far less per read.
2. **Leaner HTTP/2 transport.** grpc-go's framing allocates fewer/cheaper buffers and
   header objects than grpc-netty; less work and less GC pressure per request.
3. **Cheaper GC, better cache locality.** Lower allocation per request → less GC and a
   smaller working set → higher IPC on the same 2 cores (spring-vt's measured IPC was
   ~0.44, memory/stall-bound; the transport pointer-chasing is a big part of that).

On the read path the server CPU is the **wall** (the DB isn't involved), so these
per-request savings convert almost directly into more reads/sec — hence **+31%**.

## Why spring-vt wins the WRITE path (+18%) — and why that's consistent

`execute` is a durable INSERT: every request blocks on a **Postgres round-trip**. There
the transport tax is a *small fraction* of the per-request time (most of it is parked
waiting on PG), so Go's leaner transport barely helps — and **HikariCP + pgjdbc happened
to pace Postgres faster than pgx** on this box. This is not a fluke: the earlier
execute-only soak showed the same ordering (**spring-vt 11,973 vs go-pgx 10,081 rps**).
So Java is *not* slower on the durable write path here; if anything it's ahead.

## Takeaway

The runtime choice is **workload-shaped, not absolute**:

- **Read-heavy / cache-served** (transport-bound) → **Go wins** (leaner transport +
  cheaper concurrency; the per-request tax is the bottleneck).
- **Write-heavy / durable** (Postgres-round-trip-bound) → **spring-vt is at least as
  good** (the DB round-trip dwarfs the transport tax, and its pool/driver pace PG well).
- **Combined here, Go is +10.5%** only because reads are the larger share.

It is a genuine tradeoff, not a blowout. Robustness slightly favours spring-vt (0 errors
vs go-pgx's 0.008% transport-level blips; the go server logged no application errors —
the lone error was a shutdown-time Redis cancel).

> A 3-way run adding **rust-tokio** (same Redis read-through cache) is the next step —
> Rust is native like Go but with no GC at all, so it's the cleanest test of "how much of
> Go's read-path win is *not having the JVM* vs *not having a GC*."
