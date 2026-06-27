#!/usr/bin/env bash
# Sustained SOAK: go-pgx, rust-tokio, spring-vt, spring-data-jdbc — ONE AT A TIME,
# ~30 min each at a fixed concurrency (c=64). Between every stack: TRUNCATE, wait
# for any (auto)VACUUM to finish, and cool down until system load drops, so each
# run starts from a clean, quiet box.
#
#   per stack = MODES x PER_MODE_DUR (default execute @ 30m = 30 min)
#   pool=32, the JVM stacks run epoll + 1 I/O thread + 2 GB heap (ZGC)
#   Override which stack(s) to soak: STACKS="spring-data-jdbc" bash scripts/soak_3stacks.sh
#   (reboot between stacks for parity — run one stack per boot).
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=32 PG_POOL_MIN=4
JVM_OPTS=(-Xms2048m -Xmx2048m -XX:+UseZGC -XX:+AlwaysPreTouch)
# spring-rt (reactive Spring Data R2DBC) adds compact object headers (JEP 519).
SPRING_RT_JVM_OPTS=("${JVM_OPTS[@]}" -XX:+UseCompactObjectHeaders)
# spring-vt now co-hosts REST (Jetty) + gRPC, so it gets a production-shaped tune:
# fixed 2304m heap + 768m direct (≈3 GB, leaves ~1 GB for OS/native), ZGC capped
# to 1 concurrent thread (protect the 2 vCPU), compact headers, pooled Netty bufs.
SPRING_VT_JVM_OPTS=(-Xms2304m -Xmx2304m -XX:+UseZGC -XX:ConcGCThreads=1
  -XX:MaxDirectMemorySize=768m -XX:+AlwaysPreTouch -XX:+UseCompactObjectHeaders
  -Dio.netty.allocator.type=pooled)
C="${C:-64}"
PER_MODE_DUR="${PER_MODE_DUR:-30m}"
WARMUP="${WARMUP:-30s}"
MODES=(execute)
# Default to the new stack for the post-reboot soak; override with STACKS=...
STACKS=(${STACKS:-spring-data-jdbc})
TS="$(date +%Y%m%d-%H%M%S)"
OUT="$ROOT/results/soak-$TS"; mkdir -p "$OUT"
SUMMARY="$OUT/summary.csv"
echo "stack,mode,c,rps,p50_ms,p90_ms,p99_ms,p999_ms,max_ms,total_ok,total_err" > "$SUMMARY"

q(){ PGPASSWORD=sam psql "$DATABASE_URL" -tAc "$1" 2>/dev/null; }
truncate_db(){ PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1; }
seed(){ PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$1-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1; }

wait_no_vacuum(){
  for _ in $(seq 1 120); do                # up to ~10 min
    local n; n=$(q "select count(*) from pg_stat_activity where (query ilike '%vacuum%' or backend_type='autovacuum worker') and pid<>pg_backend_pid()")
    [ "${n:-0}" = "0" ] && return 0
    echo "  [clean] ${n} vacuum worker(s) active — waiting..."; sleep 5
  done
  echo "  [clean] WARN: vacuum still active after timeout"
}
cooldown(){                                # wait for box to go quiet
  local mincool="${1:-30}"; sleep "$mincool"
  for _ in $(seq 1 72); do                 # up to ~6 min
    local la; la=$(awk '{print $1}' /proc/loadavg)
    awk "BEGIN{exit !($la < 2.0)}" && { echo "  [cooldown] loadavg ${la} — ok"; return 0; }
    echo "  [cooldown] loadavg ${la} — waiting..."; sleep 5
  done
}
wait_port(){ local p="$1"; for _ in $(seq 1 120); do (exec 3<>/dev/tcp/127.0.0.1/"$p") 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

SRV_PID=""; PORT=""
start_server(){
  case "$1" in
    go-pgx)     PORT=50051; LISTEN_ADDR=127.0.0.1:$PORT GOMAXPROCS=2 taskset -c 2,3 "$ROOT/bin/go-server"   >"$OUT/$1.server.log" 2>&1 & SRV_PID=$! ;;
    go-gorm)    PORT=50054; LISTEN_ADDR=127.0.0.1:$PORT GOMAXPROCS=2 taskset -c 2,3 "$ROOT/bin/go-gorm-server" >"$OUT/$1.server.log" 2>&1 & SRV_PID=$! ;;
    rust-tokio) PORT=50053; LISTEN_ADDR=127.0.0.1:$PORT RUST_WORKER_THREADS=2 taskset -c 2,3 "$ROOT/bin/rust-server" >"$OUT/$1.server.log" 2>&1 & SRV_PID=$! ;;
    spring-vt)  PORT=50056; HTTP_PORT="${SPRING_VT_HTTP_PORT:-8080}" LISTEN_PORT=$PORT \
                  REDIS_ENABLED="${REDIS_ENABLED:-false}" REDIS_HOST="${REDIS_HOST:-127.0.0.1}" REDIS_PORT="${REDIS_PORT:-6379}" \
                  REDIS_TTL_SECONDS="${REDIS_TTL_SECONDS:-300}" REDIS_POOL_ENABLED="${REDIS_POOL_ENABLED:-false}" \
                  taskset -c 2,3 java "${SPRING_VT_JVM_OPTS[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" >"$OUT/$1.server.log" 2>&1 & SRV_PID=$! ;;
    spring-data-jdbc) PORT=50060; taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$ROOT/bin/spring-data-jdbc-bench.jar" >"$OUT/$1.server.log" 2>&1 & SRV_PID=$! ;;
    spring-rt)  PORT=50058; taskset -c 2,3 java "${SPRING_RT_JVM_OPTS[@]}" -jar "$ROOT/bin/spring-rt-bench.jar" >"$OUT/$1.server.log" 2>&1 & SRV_PID=$! ;;
  esac
}
stop_server(){
  [ -z "$SRV_PID" ] && return
  kill -TERM "$SRV_PID" 2>/dev/null || true
  for _ in $(seq 1 40); do kill -0 "$SRV_PID" 2>/dev/null || break; sleep 0.5; done
  kill -KILL "$SRV_PID" 2>/dev/null || true; wait "$SRV_PID" 2>/dev/null || true
  SRV_PID=""
}
trap stop_server EXIT

echo "SOAK START $(date +%H:%M:%S)  c=$C per_mode=$PER_MODE_DUR warmup=$WARMUP pool=$PG_POOL_MAX  out=$OUT"
for stack in "${STACKS[@]}"; do
  echo "================= $stack  $(date +%H:%M:%S) ================="
  echo "  [pre] truncate + drain vacuum + cooldown"
  truncate_db; wait_no_vacuum; cooldown 20
  start_server "$stack"
  if ! wait_port "$PORT"; then echo "  !! $stack START_FAIL — see $OUT/$stack.server.log — STOPPING."; stop_server; exit 1; fi
  echo "  [up] $stack on :$PORT (pid $SRV_PID)"
  for mode in "${MODES[@]}"; do
    truncate_db
    [ "$mode" = read ] && seed "$C"
    echo "  -- soak $stack/$mode c=$C for $PER_MODE_DUR  $(date +%H:%M:%S)"
    taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:"$PORT" -c "$C" -d "$PER_MODE_DUR" -warmup "$WARMUP" \
      -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out "$OUT/$stack-$mode.json" >/dev/null 2>&1 || true
    python3 -c "import json;r=json.load(open('$OUT/$stack-$mode.json'));print('$stack,$mode,$C,%.0f,%.3f,%.3f,%.3f,%.3f,%.3f,%d,%d'%(r['rps'],r['lat_p50_ms'],r['lat_p90_ms'],r['lat_p99_ms'],r['lat_p999_ms'],r['lat_max_ms'],r['total_ok'],r['total_err']))" >> "$SUMMARY" 2>/dev/null || echo "$stack,$mode,$C,PARSE_FAIL,,,,,,," >> "$SUMMARY"
    # Fail-fast on errors (as requested): stop the whole soak if this run had any.
    ERRN=$(python3 -c "import json;print(json.load(open('$OUT/$stack-$mode.json'))['total_err'])" 2>/dev/null || echo -1)
    if [ "${ERRN:-1}" != "0" ]; then
      echo "  !! $stack/$mode reported total_err=${ERRN} — STOPPING soak as requested (results so far in $SUMMARY)."
      stop_server; exit 2
    fi
    echo "  [ok] $stack/$mode  total_err=0"
  done
  stop_server
  echo "  [post] truncate + drain vacuum + cooldown before next stack"
  truncate_db; wait_no_vacuum; cooldown 30
done
echo "SOAK DONE $(date +%H:%M:%S)"
echo "=== summary ==="; column -s, -t "$SUMMARY" 2>/dev/null || cat "$SUMMARY"
