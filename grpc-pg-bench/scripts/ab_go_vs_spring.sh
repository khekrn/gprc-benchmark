#!/usr/bin/env bash
# Same-session efficiency comparison: go-pgx vs optimized spring-vt.
#   go : bin/go-server        (GOMAXPROCS=2, pgxpool 4/16)             port 50051
#   svt: spring-vt-bench.jar   (epoll + 1 I/O thread + 2 GB, raw JDBC)  port 50056
# Both server-pinned to 2 cores (2,3), client to 4,5. Interleaved per (mode,c).
#
# Focus: CPU / latency / throughput (memory deliberately ignored — compute is
# the cost that matters). We sample the server's CPU (utime+stime from
# /proc/PID/stat) over an 8s steady-state window mid-measure, and report:
#   rps, p99, server CPU% (out of 200% on 2 cores), and rps_per_core
#   (= rps / CPU-cores-used) — the throughput-per-compute efficiency number.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=16 PG_POOL_MIN=4
JVM_OPTS=(-Xms2048m -Xmx2048m -XX:+UseZGC -XX:+AlwaysPreTouch)
WARMUP="${WARMUP:-12s}"; DUR="${DUR:-45s}"
HZ=$(getconf CLK_TCK)
OUT=/tmp/go-vs-svt.csv
echo "mode,c,stack,rps,p99,cpu_pct,rps_per_core" > "$OUT"

wait_port(){ local p="$1"; for _ in $(seq 1 90); do (exec 3<>/dev/tcp/127.0.0.1/"$p") 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }
# utime+stime (ticks) for a pid, robust to comm containing spaces/parens.
cpu_ticks(){ awk '{s=$0; sub(/^.*\) /,"",s); split(s,a," "); print a[12]+a[13]}' /proc/"$1"/stat 2>/dev/null || echo 0; }

run_one(){
  local stack="$1" mode="$2" c="$3" port pid
  PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
  if [ "$mode" = read ]; then
    PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$c-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1
  fi
  if [ "$stack" = go ]; then
    port=50051
    LISTEN_ADDR=127.0.0.1:$port GOMAXPROCS=2 taskset -c 2,3 "$ROOT/bin/go-server" >/tmp/govs-srv.log 2>&1 &
  else
    port=50056
    taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" >/tmp/govs-srv.log 2>&1 &
  fi
  pid=$!
  if ! wait_port "$port"; then echo "$mode,$c,$stack,START_FAIL,0,0,0" >> "$OUT"; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; return; fi

  # loadgen in background so we can sample server CPU during steady state
  taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:"$port" -c "$c" -d "$DUR" -warmup "$WARMUP" -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out /tmp/govs.json >/dev/null 2>&1 &
  local lg=$!
  sleep 18                      # past warmup, into the measured window
  local t0; t0=$(cpu_ticks "$pid")
  sleep 8                       # 8s steady-state CPU sample
  local t1; t1=$(cpu_ticks "$pid")
  wait "$lg" 2>/dev/null || true
  local cpu_pct rps p99 eff
  cpu_pct=$(python3 -c "print('%.0f'%(($t1-$t0)/$HZ/8*100))" 2>/dev/null || echo 0)
  read rps p99 < <(python3 -c "import json;r=json.load(open('/tmp/govs.json'));print('%.0f %.3f'%(r['rps'],r['lat_p99_ms']))" 2>/dev/null || echo "0 0")
  eff=$(python3 -c "print('%.0f'%($rps/(($cpu_pct/100) if $cpu_pct>0 else 1)))" 2>/dev/null || echo 0)
  echo "$mode,$c,$stack,$rps,$p99,$cpu_pct,$eff" >> "$OUT"
  kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
}

echo "GO-vs-SVT START $(date +%H:%M:%S)  2-core, CPU-focused, warmup=$WARMUP dur=$DUR"
for mode in execute exectx read; do
  for c in 8 32 64 128; do
    for stack in go svt; do
      run_one "$stack" "$mode" "$c"
      echo "done $mode c=$c $stack $(date +%H:%M:%S)"
    done
  done
done
echo "GO-vs-SVT DONE $(date +%H:%M:%S)"

echo "=== go-pgx vs spring-vt: throughput / latency / CPU efficiency ==="
python3 - "$OUT" <<'PY'
import csv,sys
d={}
for r in csv.DictReader(open(sys.argv[1])):
    try: d[(r['mode'],int(r['c']),r['stack'])]=(float(r['rps']),float(r['p99']),float(r['cpu_pct']),float(r['rps_per_core']))
    except: pass
for mode in ['execute','exectx','read']:
    cs=sorted({k[1] for k in d if k[0]==mode})
    if not cs: continue
    print(f"\n{mode}:  c | go rps | svt rps | go p99 | svt p99 | go CPU% | svt CPU% | go/core | svt/core | eff svt/go")
    for c in cs:
        g=d.get((mode,c,'go')); s=d.get((mode,c,'svt'))
        if g and s:
            r=(s[3]/g[3]) if g[3] else 0
            print(f"     {c:4d} | {g[0]:6.0f} | {s[0]:7.0f} | {g[1]:6.2f} | {s[1]:7.2f} | {g[2]:6.0f}  | {s[2]:7.0f}  | {g[3]:6.0f}  | {s[3]:7.0f}  | {r:5.2f}x")
PY
