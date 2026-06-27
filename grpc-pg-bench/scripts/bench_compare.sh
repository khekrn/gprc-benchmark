#!/usr/bin/env bash
# Head-to-head: run the SAME mixed workload (parallel execute + read/Redis +
# co-hosted REST /health) on spring-vt, go-pgx and rust-tokio back-to-back and
# print a side-by-side comparison. Every stack gets the identical Redis
# read-through cache, SQL, concurrency, pool, co-hosted /health, and 2-core pin —
# so the delta isolates the RUNTIME (JVM/virtual-threads/Netty/Lettuce vs
# Go/goroutines/grpc-go/go-redis vs Rust/tokio/tonic/redis-rs), not the design.
#
# Each stack runs via scripts/bench_mixed.sh (truncates + flushes + seeds + warms
# its own fresh state), with a cooldown-until-quiet between them. Run once on a
# fresh boot. The /health probe applies only to spring-vt (go-pgx is gRPC-only).
#
# Knobs (env): DURATION WARMUP EXEC_C READ_C KEYSPACE STACKS ORDER.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

DURATION="${DURATION:-10m}"
WARMUP="${WARMUP:-30s}"
EXEC_C="${EXEC_C:-32}"
READ_C="${READ_C:-32}"
KEYSPACE="${KEYSPACE:-5000}"
STACKS="${STACKS:-spring-vt go-pgx rust-tokio}"

TS="$(date +%Y%m%d-%H%M%S)"
BASE="$ROOT/results/compare-$TS"; mkdir -p "$BASE"

cooldown(){ sleep "${1:-20}"; for _ in $(seq 1 45); do local la; la=$(awk '{print $1}' /proc/loadavg); awk "BEGIN{exit !($la<1.5)}" && { echo "  [cooldown] loadavg $la ok"; return 0; }; echo "  [cooldown] loadavg $la — waiting"; sleep 4; done; }

echo "COMPARE START $(date +%H:%M:%S)  stacks=[$STACKS]  dur=$DURATION warmup=$WARMUP exec_c=$EXEC_C read_c=$READ_C keyspace=$KEYSPACE  out=$BASE"
for s in $STACKS; do
  echo ""
  echo "=================== $s  $(date +%H:%M:%S) ==================="
  OUT="$BASE/$s" STACK="$s" DURATION="$DURATION" WARMUP="$WARMUP" \
    EXEC_C="$EXEC_C" READ_C="$READ_C" KEYSPACE="$KEYSPACE" \
    bash "$ROOT/scripts/bench_mixed.sh" 2>&1 | sed 's/^/  /' || echo "  !! $s FAILED (see $BASE/$s/server.log)"
  echo "  [post] cooldown before next stack"
  cooldown 20
done

echo ""
echo "==================== COMPARISON ===================="
python3 - "$BASE" $STACKS <<'PY'
import json, sys
base = sys.argv[1]; stacks = sys.argv[2:]
def load(s, n):
    try: return json.load(open(f"{base}/{s}/{n}.json"))
    except Exception: return None
W = 16
print(f"{'metric':<20}" + "".join(f"{s:>{W}}" for s in stacks))
def line(label, fn):
    print(f"{label:<20}" + "".join(f"{fn(s):>{W}}" for s in stacks))
for wl in ['execute', 'read']:
    print(f"-- {wl} --")
    line("  rps",      lambda s: (f"{load(s,wl)['rps']:.0f}"        if load(s,wl) else "-"))
    line("  p50 ms",   lambda s: (f"{load(s,wl)['lat_p50_ms']:.2f}" if load(s,wl) else "-"))
    line("  p99 ms",   lambda s: (f"{load(s,wl)['lat_p99_ms']:.2f}" if load(s,wl) else "-"))
    line("  p99.9 ms", lambda s: (f"{load(s,wl)['lat_p999_ms']:.2f}"if load(s,wl) else "-"))
    line("  max ms",   lambda s: (f"{load(s,wl)['lat_max_ms']:.2f}" if load(s,wl) else "-"))
    line("  errors",   lambda s: (f"{load(s,wl)['total_err']}"      if load(s,wl) else "-"))
print("-- combined --")
def comb_rps(s):
    e,r = load(s,'execute'), load(s,'read'); return (e['rps']+r['rps']) if (e and r) else 0
def comb_ok(s):
    e,r = load(s,'execute'), load(s,'read'); return (e['total_ok']+r['total_ok']) if (e and r) else 0
line("  combined rps",  lambda s: f"{comb_rps(s):.0f}")
line("  total processed",lambda s: f"{comb_ok(s)}")
# winner note on combined rps
vals = sorted([(s, comb_rps(s)) for s in stacks if comb_rps(s) > 0], key=lambda x: -x[1])
if len(vals) == 2:
    (hi, hv), (lo, lv) = vals
    pct = 100.0 * (hv - lv) / lv if lv else 0.0
    print(f"\n  => {hi} combined rps is {pct:+.1f}% vs {lo} ({hv:.0f} vs {lv:.0f})")
PY
echo "COMPARE DONE $(date +%H:%M:%S)  out=$BASE"
