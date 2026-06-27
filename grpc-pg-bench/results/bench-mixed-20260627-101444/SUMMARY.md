# spring-vt mixed-workload benchmark — 30 min

`execute` + `read` (GetState through the Redis read-through cache) run in PARALLEL
against one spring-vt server, with the co-hosted REST `/health` pinged every second.
Server pinned to 2 cores (t4g.medium shape); production JVM tune; Redis local, warm.

- duration 30 min, warmup 30 s, execute c=32, read c=32, keyspace 5000 (160k seeded
  workflow_state rows), Redis TTL 300 s, Lettuce shared connection (pool=false).

## Results

| workload | rps | requests (ok) | err | p50 | p99 | p99.9 | max |
|----------|----:|--------------:|----:|----:|----:|------:|----:|
| **execute** (write) | 7,381 | 13,285,441 | **0** | 3.84 | 9.44 | 18.86 | 254.65 ms |
| **read** (GetState→Redis) | 10,232 | 18,417,311 | **0** | 3.05 | 6.29 | 9.15 | 156.04 ms |
| **COMBINED** | **17,613** | **31,702,752** | **0** | — | — | — | — |

- **Redis hit ratio (measured window): 95.5%** (17.83M hits / 0.84M misses). The
  ~4.5% miss rate is TTL-driven: the 160k-key working set fits entirely in Redis, so
  misses are 300 s expiry re-populations, not capacity evictions. A longer
  `REDIS_TTL_SECONDS` would push hit ratio toward ~100%.
- **/health: p50 3.2 ms, p99 10.2 ms, max 283 ms** over 1,779 pings, **0 non-200**.
  The 283 ms max is the single cold-start ping; p99 ~10 ms under full mixed load.

## Reading it

- **The cache decouples reads from write-table growth.** Over the run the write path
  decayed (~8.5k → ~6.2k rps; time-avg 7,381) as `commands` grew past 13M rows and
  index maintenance got heavier — the usual sustained-write behaviour. The read path
  held flat (~10.2k rps, p99 6.3 ms) because Redis serves it, independent of the
  growing table. That divergence is the point of the mixed test.
- **No interference at the 2-core wall.** execute, read, and the /health probe shared
  one 2-core server for 30 min with zero errors and a single-digit-ms health p99 — the
  three surfaces don't starve each other.
- 31.7M requests / 0 errors over 30 min = a robust mixed steady state.
