# spring-vt cache-miss + context-switch A/B (perf stat)

Hardware-counter A/B isolating two design choices on the 2-core-pinned spring-vt
(gRPC + co-hosted Jetty REST). 4 cells, each a 40s steady-state `perf stat` window
under c=64 `execute` load, all at matched throughput (~17.6–18.0k rps). Needs
`kernel.perf_event_paranoid<=2`. Harness: `scratchpad/perf_ab2.sh`.

Metrics normalized **per million instructions (PMI)** so cross-cell comparison is
valid independent of small rps differences.

| cell  | config              | rps    | IPC   | cache-miss/s | cacheMiss/Minstr | L1-miss/s | ctx-sw/s | migr/s |
|-------|---------------------|-------:|------:|-------------:|-----------------:|----------:|---------:|-------:|
| base  | compact ON, 1 I/O   | 17,655 | 0.483 | 73.3 M       | 26,855           | 123.3 M   | 18,121   | 432    |
| nohdr | compact OFF, 1 I/O  | 17,901 | 0.479 | 75.3 M       | 27,403           | 128.0 M   | 18,319   | 427    |
| io2   | compact ON, 2 I/O   | 17,950 | 0.454 | 80.4 M       | 29,769           | 124.8 M   | 16,191   | 240    |
| base2 | repeat of base      | 17,864 | 0.477 | 74.0 M       | 26,921           | 125.0 M   | 18,650   | 506    |

**Stability:** base vs base2 (identical config) agree to 0.25% on cacheMiss/Minstr
→ deltas >0.5% are real signal, not box noise. (base/base2 errors 43/1953 are
cold-start, outside the 40s steady window; nohdr/io2 = 0.)

## Findings

1. **Compact object headers (JEP 519): real but modest cache win.**
   −2.0% cache-misses/instruction, −3.1% L1-d-cache-misses/instruction, +1.0% IPC.
   Modest because the path is DB-round-trip bound with a small per-request heap
   footprint — header shrinkage improves locality but there's little memory
   pressure to relieve.

2. **1 vs 2 Netty I/O threads is a CACHE-LOCALITY story, not a context-switch one.**
   Context switches barely moved (slightly *fewer* with 2 threads). The real cost
   of a 2nd event loop on the 2-core box is cache locality: IPC −6% (0.483→0.454)
   and cache-misses +10% (73.3M→80.4M/s), because two loops split across cores 2,3
   bounce connection/buffer state between the cores' caches. rps is identical only
   because Postgres is the wall; 1 I/O thread is the more CPU-efficient choice.
   (Corrects the earlier "fewer cross-thread wakeups / context switches" wording.)

3. **Context-switch rate is workload-governed (~18k/s, ~6–7 per million instr),
   not knob-governed.** Unchanged by headers or I/O-thread count because the driver
   is virtual-thread park/unpark on the blocking JDBC round-trips, not the event
   loops. The lever for fewer ctx switches is fewer DB round-trips, not thread tuning.

## Caveats

- This CPU returns `<not supported>` for LLC-load/LLC-load-miss PMU events;
  `cache-misses` (generic LLC-miss proxy) and L1-dcache events do count.
- `~83%` enable-fractions on each perf event = normal PMU multiplexing (more events
  than hardware counters), already accounted for by perf's scaling.
- A prior contaminated run (`results/perf-cohost-*`, 12s warmup) was discarded:
  cold-start errors + an anomalous nohdr rps made cells non-comparable. This v2
  (30s warmup, cooldown-until-quiet between cells) supersedes it.
