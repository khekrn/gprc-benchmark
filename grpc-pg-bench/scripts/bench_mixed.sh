#!/usr/bin/env bash
# Mixed-workload benchmark for spring-vt (NOT a soak — a ~60s performance snapshot).
#
# Runs TWO workloads in PARALLEL against one spring-vt server to validate a mixed
# read/write profile:
#   * execute  — autocommit INSERT into commands (write path), its own loadgen
#   * read     — GetState, served THROUGH the Redis read-through cache (read path)
# and, concurrently, an external REST /health probe (the co-hosted Jetty endpoint)
# is pinged every second so the health surface is exercised under load too.
#
# Redis is prepopulated: workflow_state is seeded in PG for the read keyspace, then
# a dedicated warm pass primes Redis before the measured window. The Redis hit ratio
# DURING the measured window is computed from keyspace_hits/misses deltas.
#
# Core layout keeps the server at its 2-core constraint while the client never
# bottlenecks it: server 2,3 | execute-gen 4,5 | read-gen 6,7 | health 0,1.
#
# Output: per-workload rps + latency, COMBINED total processed, Redis hit ratio,
# and /health latency — written to results/bench-mixed-<ts>/.
#
# Knobs (env): DURATION WARMUP EXEC_C READ_C KEYSPACE REDIS_POOL_ENABLED.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi

export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=32 PG_POOL_MIN=4

STACK="${STACK:-spring-vt}"          # spring-vt | go-pgx
DURATION="${DURATION:-60s}"
WARMUP="${WARMUP:-20s}"
EXEC_C="${EXEC_C:-32}"
READ_C="${READ_C:-32}"
KEYSPACE="${KEYSPACE:-5000}"
REDIS_PORT="${REDIS_PORT:-6379}"
export REDIS_POOL_ENABLED="${REDIS_POOL_ENABLED:-false}"

# Production spring-vt JVM tune (matches SPRING_VT_JVM_OPTS).
JVM_OPTS=(-Xms2304m -Xmx2304m -XX:+UseZGC -XX:ConcGCThreads=1
  -XX:MaxDirectMemorySize=768m -XX:+AlwaysPreTouch -XX:+UseCompactObjectHeaders
  -Dio.netty.allocator.type=pooled)

TS="$(date +%Y%m%d-%H%M%S)"
OUT="${OUT:-$ROOT/results/bench-mixed-$STACK-$TS}"; mkdir -p "$OUT"
LG="$ROOT/bin/loadgen"
[ -x "$LG" ] || { echo "build loadgen first"; exit 1; }

# Per-stack: gRPC port, server start command, and whether it has a REST /health
# (only spring-vt co-hosts one; go-pgx is gRPC-only so the health probe is skipped).
HTTP_PORT=8080
case "$STACK" in
  spring-vt)
    GRPC_PORT=50056; HAS_HEALTH=1
    JAR="$ROOT/bin/spring-vt-bench.jar"
    [ -f "$JAR" ] || { echo "build spring-vt first"; exit 1; }
    start_server(){
      REDIS_ENABLED=true REDIS_HOST=127.0.0.1 REDIS_PORT="$REDIS_PORT" REDIS_POOL_ENABLED="$REDIS_POOL_ENABLED" \
        HTTP_PORT="$HTTP_PORT" LISTEN_PORT="$GRPC_PORT" \
        taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$JAR" > "$OUT/server.log" 2>&1 & SRV=$!
    } ;;
  go-pgx)
    GRPC_PORT=50051; HAS_HEALTH=0
    BIN="$ROOT/bin/go-server"
    [ -x "$BIN" ] || { echo "build go-pgx first (scripts/build_go.sh)"; exit 1; }
    start_server(){
      REDIS_ENABLED=true REDIS_HOST=127.0.0.1 REDIS_PORT="$REDIS_PORT" \
        DATABASE_URL="$DATABASE_URL" LISTEN_ADDR="127.0.0.1:$GRPC_PORT" GOMAXPROCS=2 \
        PG_POOL_MAX=32 PG_POOL_MIN=4 \
        taskset -c 2,3 "$BIN" > "$OUT/server.log" 2>&1 & SRV=$!
    } ;;
  *) echo "unknown STACK=$STACK (use spring-vt|go-pgx)"; exit 1 ;;
esac

q(){ PGPASSWORD=sam psql "$DATABASE_URL" -tAc "$1" 2>/dev/null; }
rcli(){ redis-cli -p "$REDIS_PORT" "$@" 2>/dev/null; }
wait_port(){ for _ in $(seq 1 120); do (exec 3<>/dev/tcp/127.0.0.1/"$1") 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

echo "MIXED BENCH START $(date +%H:%M:%S)  stack=$STACK dur=$DURATION warmup=$WARMUP exec_c=$EXEC_C read_c=$READ_C keyspace=$KEYSPACE  out=$OUT"

# --- Redis up? ---
if ! wait_port "$REDIS_PORT"; then
  echo "  starting redis-server on :$REDIS_PORT"
  redis-server --daemonize yes --port "$REDIS_PORT" --save '' --appendonly no >/dev/null 2>&1
  wait_port "$REDIS_PORT" || { echo "  redis failed to start"; exit 1; }
fi
rcli flushall >/dev/null

# --- Seed workflow_state for the read keyspace (wf-0..READ_C-1 x 0..KEYSPACE-1) ---
echo "  [seed] truncate + seed workflow_state ($((READ_C*KEYSPACE)) rows)"
PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
PGPASSWORD=sam psql "$DATABASE_URL" -q -c \
  "INSERT INTO workflow_state(workflow_id,state,version,updated_at) \
   SELECT 'wf-'||w||'-'||k,'seed',1,now() \
   FROM generate_series(0,$((READ_C-1))) w, generate_series(0,$((KEYSPACE-1))) k \
   ON CONFLICT DO NOTHING;" >/dev/null 2>&1
echo "  [seed] workflow_state rows: $(q 'select count(*) from workflow_state')"

# --- Start server (Redis enabled) ---
echo "  [up] starting $STACK (REDIS_ENABLED=true) on :$GRPC_PORT"
start_server
trap 'kill -TERM $SRV 2>/dev/null; sleep 2; kill -KILL $SRV 2>/dev/null' EXIT
if ! wait_port "$GRPC_PORT"; then echo "  server START_FAIL"; tail -20 "$OUT/server.log"; exit 1; fi
[ "$HAS_HEALTH" = 1 ] && { wait_port "$HTTP_PORT" || true; }
grep -iE 'Redis read-through|Lettuce ClientResources|redis read-through cache enabled' "$OUT/server.log" | sed 's/^/    /'

# --- Pre-warm Redis: read pass over the keyspace so the cache is hot before measuring ---
echo "  [warm] priming Redis cache (read pass ${WARMUP})"
taskset -c 6,7 "$LG" -addr 127.0.0.1:"$GRPC_PORT" -c "$READ_C" -d "$WARMUP" -warmup 0s \
  -payload 256 -conns 4 -mode read -keyspace "$KEYSPACE" -out /dev/null >/dev/null 2>&1
echo "  [warm] redis keys: $(rcli dbsize)"

# --- Snapshot Redis stats just before the measured window ---
H0=$(rcli info stats | awk -F: '/keyspace_hits/{print $2+0}'); H0=${H0//[$'\r']/}
M0=$(rcli info stats | awk -F: '/keyspace_misses/{print $2+0}'); M0=${M0//[$'\r']/}

# --- PARALLEL measured window: execute + read + health ---
echo "  [run] parallel execute(c=$EXEC_C) + read(c=$READ_C) + /health for $DURATION  $(date +%H:%M:%S)"
taskset -c 4,5 "$LG" -addr 127.0.0.1:"$GRPC_PORT" -c "$EXEC_C" -d "$DURATION" -warmup "$WARMUP" \
  -payload 256 -conns 4 -mode execute -out "$OUT/execute.json" >/dev/null 2>&1 &
PE=$!
taskset -c 6,7 "$LG" -addr 127.0.0.1:"$GRPC_PORT" -c "$READ_C" -d "$DURATION" -warmup "$WARMUP" \
  -payload 256 -conns 4 -mode read -keyspace "$KEYSPACE" -out "$OUT/read.json" >/dev/null 2>&1 &
PR=$!
# Health probe every 1s for the whole window (only the stack that has a REST /health).
PH=""
if [ "$HAS_HEALTH" = 1 ]; then
  DUR_S=$(python3 -c "import re;s='$DURATION';w='$WARMUP';f=lambda x:int(re.sub('[^0-9]','',x))*(60 if x.strip().endswith('m') else 1);print(f(s)+f(w)+10)")
  DURATION="$DUR_S" PORT="$HTTP_PORT" INTERVAL=1 OUT="$OUT/health.csv" PIN_CPUS=0,1 \
    bash "$ROOT/scripts/health_ping.sh" > "$OUT/health.log" 2>&1 &
  PH=$!
fi

wait $PE; wait $PR
[ -n "$PH" ] && { kill -TERM $PH 2>/dev/null; wait $PH 2>/dev/null || true; }

# --- Redis hit ratio during the measured window (deltas) ---
H1=$(rcli info stats | awk -F: '/keyspace_hits/{print $2+0}'); H1=${H1//[$'\r']/}
M1=$(rcli info stats | awk -F: '/keyspace_misses/{print $2+0}'); M1=${M1//[$'\r']/}

kill -TERM $SRV 2>/dev/null; for _ in $(seq 1 30); do kill -0 $SRV 2>/dev/null || break; sleep 0.5; done
kill -KILL $SRV 2>/dev/null; trap - EXIT

# --- Report ---
echo ""
echo "================== MIXED BENCH RESULTS =================="
python3 - "$OUT" "$H0" "$M0" "$H1" "$M1" <<'PY'
import json, sys
out, h0, m0, h1, m1 = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), int(sys.argv[5])
def load(name):
    try: return json.load(open(f"{out}/{name}.json"))
    except Exception: return None
e, r = load("execute"), load("read")
def row(tag, j):
    if not j: print(f"  {tag:<9} (no data)"); return (0,0)
    print(f"  {tag:<9} rps={j['rps']:>9.0f}  ok={j['total_ok']:>9}  err={j['total_err']:>5}  "
          f"p50={j['lat_p50_ms']:>6.2f}  p99={j['lat_p99_ms']:>7.2f}  p999={j['lat_p999_ms']:>7.2f}  max={j['lat_max_ms']:>7.2f} ms")
    return (j['total_ok'], j['total_err'])
print("-- per workload --")
eo, ee = row("execute", e)
ro, re_ = row("read", r)
print("-- combined --")
tot_ok, tot_err = eo+ro, ee+re_
comb_rps = (e['rps'] if e else 0) + (r['rps'] if r else 0)
print(f"  TOTAL     processed={tot_ok:>9}  errors={tot_err}  combined_rps={comb_rps:>9.0f}")
dh, dm = h1-h0, m1-m0
hr = 100.0*dh/(dh+dm) if (dh+dm)>0 else 0.0
print(f"-- redis (measured window) --")
print(f"  hits={dh}  misses={dm}  hit_ratio={hr:.1f}%")
# health (only if this stack exposed /health)
import os, csv
hpath = f"{out}/health.csv"
if os.path.exists(hpath):
    lat=[]; bad=0
    for i,rw in enumerate(csv.reader(open(hpath))):
        if i==0 or len(rw)<4: continue
        if rw[2]!='200': bad+=1
        lat.append(float(rw[3]))
    if lat:
        lat.sort(); n=len(lat); p=lambda q:lat[min(n-1,int(q*n))]
        print(f"-- /health (every 1s) --")
        print(f"  samples={n}  non200={bad}  p50={p(.5):.1f}  p99={p(.99):.1f}  max={lat[-1]:.1f} ms")
PY
{
  echo "stack=$STACK dur=$DURATION warmup=$WARMUP exec_c=$EXEC_C read_c=$READ_C keyspace=$KEYSPACE pool=$REDIS_POOL_ENABLED"
} > "$OUT/params.txt"
echo "========================================================"
echo "MIXED BENCH DONE $(date +%H:%M:%S)  out=$OUT"
