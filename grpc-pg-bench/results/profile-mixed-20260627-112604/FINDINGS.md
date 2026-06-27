# spring-vt mixed-workload profile (async-profiler + perf)

Profiled spring-vt under the PARALLEL execute + read(GetState→Redis) + /health load
(exec c=32, read c=32, Redis warm), server pinned to 2 cores, production JVM tune.
During capture: execute 9,200 rps, read 9,094 rps, 0 errors, Redis hit ratio 94.3%.
(Higher execute rps than the 30-min run because the table is small/fresh here.)
Raw `.collapsed` stacks are gitignored; this file is the synthesis.

## Where the time/CPU/alloc goes

**On-CPU (itimer, 7,450 samples) — CPU burns in the transport, not the business logic:**
- Netty 54.9% + gRPC 24.0% = **~79% of on-CPU in HTTP/2 transport + framing**.
- Leaf frames: `[vdso]` 32% + `__syscall_cancel_*` 15% = **~48% in syscall/vdso** (socket
  I/O, time), `pthread_cond_signal` 3.9% (VT park/unpark wakeups). VT/scheduler 12%.
- **GC just 2.2%**, protobuf 1.1%, **HikariCP 0.0%** — neither GC nor the DB pool is a
  CPU cost. The JDBC/Redis client libraries do not appear in the on-CPU top frames.

**Off-CPU / wall (31,238 samples) — the system is I/O-round-trip bound:**
- `__syscall_cancel_start/end` = **~96% of wall time blocked in syscalls** — virtual
  threads parked waiting on PG / Redis / socket round-trips. This is the design working:
  blocking calls park the carrier rather than pinning a platform thread.

**Allocation (alloc, 20,393 samples) — transport + Loom machinery, not data access:**
- Netty 60.4% + gRPC 33.5% + protobuf 5.3% dominate: `Object[]`, `byte[]` buffers,
  HTTP/2 `AsciiString` headers, Netty promises, `DirectByteBuffer`.
- `VirtualThread` 5.4% + `ThreadLocalMap` entries ~6% = the one-VT-per-request cost.
- `PgResultSet` only 0.8%; no Lettuce/Redis allocation in the top frames.

**perf stat (40s, mixed load):**
| metric | value |
|---|---|
| IPC | 0.440 |
| cache-miss rate | 22.5% |
| cache-misses / Minstr | 30,860 |
| L1 d-cache miss rate | 12.4% |
| context switches | 26,486 /s (9.1 / Minstr) |
| cpu-migrations | 629 /s |

## Conclusions — best config & tradeoffs

1. **The bottleneck is I/O round-trips + transport, not CPU or the DB/Redis clients.**
   Wall is 96% syscall-blocked; on-CPU is 79% Netty/gRPC. Business logic (JDBC, Lettuce)
   is negligible in CPU and alloc. So throughput is gated by round-trip concurrency and
   transport efficiency — adding CPU for "faster queries" would do nothing.

2. **Redis is doing its job cheaply and the shared connection is the right default.**
   Lettuce/Redis never appears in on-CPU or alloc top frames; the read path's cost is the
   network round-trip (in wall), served at 94% hit so it replaces a *PG* round-trip with a
   faster *Redis* one and offloads PG. No serialization bottleneck on the single shared
   connection at c=32 (read p99 7.2ms). A commons-pool2 pool (`REDIS_POOL_ENABLED=true`)
   would add connection overhead for no visible gain here — keep shared.

3. **Context switches scale with round-trips (workload-governed).** Mixed = ~26k/s vs
   ~18k/s for execute-only, because each read adds a Redis round-trip → more VT
   park/unpark. Not a knob to tune away; the lever is *fewer round-trips* (cache hits
   already remove the PG read for ~94% of GetStates).

4. **GC and the HikariCP pool are not bottlenecks.** GC 2.2% on-CPU / 0.1% wall (ZGC +
   ConcGCThreads=1 + compact headers absorbing a transport-dominated alloc rate);
   HikariCP 0% on-CPU (connections aren't contended — the round-trip *wait* is the cost,
   not acquiring a connection). pool=32 is ample; it could even shrink without harm.

5. **1 Netty I/O thread remains right.** With ~79% of on-CPU in the transport on 2 cores,
   transport cache-locality matters — consistent with the earlier finding that a 2nd I/O
   loop lowers IPC. IPC here is 0.44 (memory/I/O-stall bound), as expected.

**Net:** the current config (compact headers, 1 I/O thread, ZGC/ConcGCThreads=1, shared
Redis connection, pool=32) is well matched to a transport- and I/O-bound profile. For more
throughput the only real lever is fewer round-trips per request; for robustness the cache
trades a bounded staleness window (TTL) + a Redis dependency (covered by degrade-to-PG)
for read-path stability under write-table growth.
