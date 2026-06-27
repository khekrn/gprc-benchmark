#!/usr/bin/env bash
# Profile spring-vt under the MIXED workload (parallel execute + read/Redis + health),
# capturing async-profiler (itimer/wall/alloc) AND perf stat (cache/ctx/IPC) at steady
# state. The point: see where the mixed read+write path spends CPU, blocks, allocates,
# and how cache-efficient it is — to inform best-config/tradeoff decisions.
#
# async-profiler itimer/wall/alloc need no perf_events; perf stat needs
# perf_event_paranoid<=2 (skipped with a note if not).
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=32 PG_POOL_MIN=4

EXEC_C="${EXEC_C:-32}"; READ_C="${READ_C:-32}"; KEYSPACE="${KEYSPACE:-5000}"
PSECS="${PSECS:-40}"                 # per-capture seconds
REDIS_PORT="${REDIS_PORT:-6379}"
export REDIS_POOL_ENABLED="${REDIS_POOL_ENABLED:-false}"
GRPC_PORT=50056; HTTP_PORT=8080
JVM_OPTS=(-Xms2304m -Xmx2304m -XX:+UseZGC -XX:ConcGCThreads=1 -XX:MaxDirectMemorySize=768m
  -XX:+AlwaysPreTouch -XX:+UseCompactObjectHeaders -Dio.netty.allocator.type=pooled
  -XX:+EnableDynamicAgentLoading)
PERF_EVENTS="instructions,cycles,cache-references,cache-misses,context-switches,cpu-migrations,L1-dcache-loads,L1-dcache-load-misses"

TS="$(date +%Y%m%d-%H%M%S)"; OUT="$ROOT/results/profile-mixed-$TS"; mkdir -p "$OUT"
LG="$ROOT/bin/loadgen"
rcli(){ redis-cli -p "$REDIS_PORT" "$@" 2>/dev/null; }
wait_port(){ for _ in $(seq 1 120); do (exec 3<>/dev/tcp/127.0.0.1/"$1") 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

echo "PROFILE-MIXED START $(date +%H:%M:%S)  exec_c=$EXEC_C read_c=$READ_C psecs=$PSECS  out=$OUT"
wait_port "$REDIS_PORT" || { redis-server --daemonize yes --port "$REDIS_PORT" --save '' --appendonly no >/dev/null 2>&1; wait_port "$REDIS_PORT"; }
rcli flushall >/dev/null
PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
PGPASSWORD=sam psql "$DATABASE_URL" -q -c \
  "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() \
   FROM generate_series(0,$((READ_C-1))) w, generate_series(0,$((KEYSPACE-1))) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1

REDIS_ENABLED=true REDIS_HOST=127.0.0.1 REDIS_PORT="$REDIS_PORT" REDIS_POOL_ENABLED="$REDIS_POOL_ENABLED" \
  HTTP_PORT="$HTTP_PORT" LISTEN_PORT="$GRPC_PORT" \
  taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" > "$OUT/server.log" 2>&1 &
PID=$!
trap 'kill -TERM $PID 2>/dev/null; sleep 2; kill -KILL $PID 2>/dev/null' EXIT
wait_port "$GRPC_PORT" || { echo "START_FAIL"; tail "$OUT/server.log"; exit 1; }
wait_port "$HTTP_PORT" || true
# warm redis
taskset -c 6,7 "$LG" -addr 127.0.0.1:"$GRPC_PORT" -c "$READ_C" -d 20s -warmup 0s -payload 256 -conns 4 -mode read -keyspace "$KEYSPACE" -out /dev/null >/dev/null 2>&1

# Long load window to cover steady-state + 4 captures (~ 15 + 4*PSECS + buffer).
LOAD=$(( 20 + 4*PSECS + 25 ))
taskset -c 4,5 "$LG" -addr 127.0.0.1:"$GRPC_PORT" -c "$EXEC_C" -d "${LOAD}s" -warmup 0s -payload 256 -conns 4 -mode execute -out "$OUT/execute.json" >/dev/null 2>&1 &
PE=$!
taskset -c 6,7 "$LG" -addr 127.0.0.1:"$GRPC_PORT" -c "$READ_C" -d "${LOAD}s" -warmup 0s -payload 256 -conns 4 -mode read -keyspace "$KEYSPACE" -out "$OUT/read.json" >/dev/null 2>&1 &
PR=$!
DURATION="$LOAD" PORT="$HTTP_PORT" INTERVAL=1 OUT="$OUT/health.csv" PIN_CPUS=0,1 bash "$ROOT/scripts/health_ping.sh" >/dev/null 2>&1 &
PH=$!

sleep 18  # reach steady state
for EV in itimer wall alloc; do
  echo ">> async-profiler $EV (${PSECS}s) $(date +%H:%M:%S)"
  asprof -d "$PSECS" -e "$EV" -o collapsed -f "$OUT/$EV.collapsed" "$PID" 2>&1 | tail -1
done
# perf stat (PMU) — last window
PARANOID=$(cat /proc/sys/kernel/perf_event_paranoid 2>/dev/null || echo 9)
if [ "${PARANOID:-9}" -le 2 ]; then
  echo ">> perf stat (${PSECS}s) $(date +%H:%M:%S)"
  perf stat -e "$PERF_EVENTS" -p "$PID" -- sleep "$PSECS" > "$OUT/perf.txt" 2>&1 || true
else
  echo ">> perf SKIPPED (perf_event_paranoid=$PARANOID > 2)"; echo "skipped: paranoid=$PARANOID" > "$OUT/perf.txt"
fi

# Redis hit ratio over the whole run
H=$(rcli info stats | awk -F: '/keyspace_hits/{print $2+0}'); M=$(rcli info stats | awk -F: '/keyspace_misses/{print $2+0}')
kill -TERM $PH 2>/dev/null; wait $PH 2>/dev/null || true
wait $PE 2>/dev/null || true; wait $PR 2>/dev/null || true
kill -TERM $PID 2>/dev/null; for _ in $(seq 1 30); do kill -0 $PID 2>/dev/null || break; sleep 0.5; done; kill -KILL $PID 2>/dev/null; trap - EXIT

echo ""
echo "=== throughput during profiling ==="
python3 -c "import json;
e=json.load(open('$OUT/execute.json'));r=json.load(open('$OUT/read.json'))
print('  execute rps=%.0f p99=%.2f err=%d'%(e['rps'],e['lat_p99_ms'],e['total_err']))
print('  read    rps=%.0f p99=%.2f err=%d'%(r['rps'],r['lat_p99_ms'],r['total_err']))" 2>/dev/null
python3 -c "print('  redis hit_ratio=%.1f%%'%(100*$H/($H+$M) if $H+$M else 0))"
echo ""
for EV in itimer wall alloc; do
  echo "=== TOP frames: $EV ==="
  python3 "$ROOT/scripts/analyze_profile.py" "$OUT/$EV.collapsed" 22 2>/dev/null || echo "  (analyze failed)"
  echo ""
done
echo "=== perf ==="
grep -E 'instructions|cycles|cache-misses|cache-references|context-switches|cpu-migrations|L1-dcache|insn per' "$OUT/perf.txt" 2>/dev/null || cat "$OUT/perf.txt"
echo ""
echo "PROFILE-MIXED DONE $(date +%H:%M:%S)  out=$OUT"
