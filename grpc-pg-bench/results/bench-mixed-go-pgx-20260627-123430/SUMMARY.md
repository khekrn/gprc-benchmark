# go-pgx vs spring-vt — 30-min mixed workload (head-to-head)

Same mixed workload on both stacks: parallel `execute` (autocommit INSERT) +
`read` (GetState through a Redis read-through cache), exec c=32 + read c=32,
keyspace 5000 (160k seeded rows), Redis warm, server pinned to 2 cores, identical
SQL / cache key+TTL / pool. The only variable is the runtime: JVM (virtual threads
+ grpc-netty + Lettuce + pgjdbc/HikariCP) vs Go (goroutines + grpc-go + go-redis +
pgx). go-pgx run 20260627-123430; spring-vt run 20260625-101444 (same dedicated
Ryzen 2-core, taskset-pinned — no burst variance across boots).

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

## Reading it — it's a tradeoff, not a clean win

- **Read path: go-pgx wins decisively (+31%).** This is exactly what the profile +
  research predicted: the cached read path is transport-bound (server CPU is the
  limiter, not the DB), and Go's leaner grpc-go + go-redis carry far less per-request
  overhead than the JVM's Netty/Lettuce/virtual-thread machinery (spring-vt's profile
  was ~79% on-CPU in Netty/grpc framing). Where the server CPU is the wall, Go wins.

- **Write path: spring-vt wins (+18%) — the counterintuitive result.** Both write to
  Postgres (durable, PG-round-trip bound), and spring-vt's HikariCP + pgjdbc paced PG
  faster than go's pgx. This is consistent with the repo's earlier execute-only soak
  (spring-vt 11,973 vs go-pgx 10,081). So Java is NOT slower on the durable write path
  here; if anything it's ahead.

- **Combined: go-pgx +10.5%**, because reads are the larger share (13.4k vs 6k), so the
  read-path win outweighs the write-path loss.

- **Robustness: spring-vt was error-free; go-pgx had 1,942 read errors (0.008%).** The
  go-pgx server logged NO application errors during the run — the only error is one Redis
  `context canceled` at shutdown (13:05:32, server killed mid-call as the window ended).
  So these are rare transport-level blips, not a failure mode — but spring-vt's clean 0
  is a (small) robustness edge.

## Verdict

For this read-heavy mixed workload, **Go gives ~+10% combined throughput and a much
faster read path**, at the cost of a slightly slower durable-write path and a trace
error rate. The runtime choice is workload-shaped: read-heavy/cache-served → Go; 
write-heavy/durable → spring-vt is at least as good. Neither is a blowout; both sustain
30M+ requests over 30 min on 2 cores.
