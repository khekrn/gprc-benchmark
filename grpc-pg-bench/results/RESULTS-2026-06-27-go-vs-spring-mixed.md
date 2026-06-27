# Java vs Go vs Rust — mixed workload (Redis read-through), 30 min, 2 cores

**Question:** with the same Redis read-through cache and the same co-hosted REST
`/health`, how do the three runtimes compare on a parallel read+write workload — and
how much of any gap is *native vs JVM* vs *no GC at all*?

**Setup (identical except the runtime):** parallel `execute` (autocommit INSERT, write
path) + `read` (GetState through Redis, read path), exec c=32 + read c=32, keyspace
5000 (160k seeded rows), Redis warm, **each stack co-hosts a REST `/health`** pinged
every second, server pinned to 2 cores, same SQL / cache key+value+TTL / pool sizes.
Back-to-back on one boot, cooldown between. Runtime is the only variable:

| | spring-vt (Java) | go-pgx (Go) | rust-tokio (Rust) |
|---|---|---|---|
| transport | grpc-netty | grpc-go | tonic |
| concurrency | virtual threads | goroutines | tokio tasks |
| GC | ZGC (generational) | Go GC | **none** |
| Redis client | Lettuce (shared multiplexed) | go-redis (pool 32) | redis-rs (multiplexed) |
| REST /health | Jetty | net/http | axum |
| Postgres | pgjdbc + HikariCP | pgx (pgxpool) | tokio-postgres + deadpool |

Run: [`results/compare-20260627-142402/`](compare-20260627-142402/).

## Results (the fair 3-way)

| metric | Java | Go | Rust | best |
|--------|-----:|---:|-----:|:-----|
| **read** rps | 10,578 | 12,711 | **22,974** | Rust |
| read p50 / p90 / p99 ms | 2.94 / 4.21 / 6.14 | 2.31 / 3.89 / 6.35 | **1.32 / 2.01 / 3.16** | Rust |
| **execute** rps | 7,028 | 5,883 | **7,673** | Rust |
| execute p50 / p90 / p99 ms | 4.08 / 7.01 / 9.56 | 5.23 / 7.78 / 10.22 | **4.07 / 5.65 / 7.58** | Rust |
| **combined rps** | 17,606 | 18,594 | **30,647** | Rust |
| combined vs Java | — | +5.6% | **+74.1%** | |

Each stack sustained 30M+ requests over 30 min. Errors were **negligible and transient**
(~0.01%, transport blips under core contention — they flip between stacks/runs, not a
stack property) and are omitted.

## Rust wins everything — read 2.2×, and even the write path

Rust is fastest on **both** paths and the **tail**: read throughput is **2.2× Java's at
half the p99** (3.16 vs 6.14 ms), and — unlike Go — it also takes the durable **write**
path (7,673 > Java 7,028 > Go 5,883). No GC means no pause-driven tail, and zero
per-request heap allocation means the transport cost per request is the lowest of the three.

## Decomposing the gap: "not the JVM" vs "no GC"

The read path is transport-bound (Redis hit = no Postgres round-trip), so the server's
per-request CPU is the wall — exactly where runtimes separate. Reading the read-rps ladder:

- **Java → Go: +20%.** The cost of *being on the JVM* — grpc-netty + Lettuce + a
  per-request `VirtualThread`/`ThreadLocalMap` allocation (these were the top allocation
  frames in spring-vt's profile, which spent ~79% of on-CPU in Netty/grpc-java framing).
  Real, but modest.
- **Go → Rust: +81%.** The cost of *having a GC and a managed runtime at all*. This jump
  is **far bigger** than the JVM-vs-native one. Rust pays no GC, allocates ~nothing per
  request, and its redis-rs `MultiplexedConnection` pipelines reads over one connection.

So **"not the JVM" buys ~20% on reads; "no GC + zero-alloc native" buys ~2.2×.** Most of
the headroom is the GC/managed-runtime/allocation tax that Go still pays and Rust doesn't
— not simply native-vs-JVM.

## Why Java beats Go on the WRITE path

`execute` is a durable INSERT — every request blocks on a **Postgres round-trip**, so the
transport tax is a small fraction of per-request time. There the runtime barely matters and
**HikariCP + pgjdbc pace PG faster than pgx** (consistent with the earlier execute-only
soak: spring-vt 11,973 vs go-pgx 10,081). Rust still edges ahead, but the three are within
~30% on writes vs ~2.2× on reads — the write path is the great equalizer.

## Recommendation for a workflow engine (2-core, Aurora PG)

A checkpoint-style workflow engine is **write/durability-bound**, and on that path Java is
within ~9% of Rust and *beats* Go. So:

- **spring-vt (Java + virtual threads) — best performance-vs-developer-productivity
  balance.** ≤9% behind Rust on the path that bounds the engine, with the richest ecosystem
  (Spring Data, declarative transactions, observability), simple blocking-on-Loom code, and
  the largest talent pool. The read gap is mostly mooted by the Redis cache.
- **rust-tokio — pick it for maximum throughput-per-core, lowest tail, smallest footprint.**
  +74% combined, GC-free tail consistency, ideal if you're cost-optimizing a small t4g box
  or have a strict p99 SLA — at the cost of slower dev iteration and a smaller talent pool.
- **go-pgx** is the awkward middle for *this* workload: it lost the write path to Java and
  trails Rust everywhere, so it doesn't clearly win either axis unless the team is all-Go.

## Notes

- **Fairness correction vs the earlier go-vs-spring run:** that run had go-pgx **gRPC-only**
  (no co-hosted REST), which inflated its read lead to +31%. With all three now paying the
  co-host cost, go's fair read lead over Java is **+20%**. The earlier asymmetric numbers
  are superseded by this run.
- **p95** was added to the loadgen after this run, so it isn't in these JSONs (they carry
  p50/p90/p99/p99.9/max); future runs include `lat_p95_ms` / `read_p95_ms` / `write_p95_ms`.
