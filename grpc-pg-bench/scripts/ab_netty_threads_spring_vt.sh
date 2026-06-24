#!/usr/bin/env bash
# Controlled same-session A/B: grpc-netty worker I/O threads 1 vs 2.
# Same epoll binary, 2 GB heap; arm flips NETTY_IO_THREADS. Interleaved per (mode,c).
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=16 PG_POOL_MIN=4 LISTEN_PORT=50056
JVM_OPTS=(-Xms2048m -Xmx2048m -XX:+UseZGC -XX:+AlwaysPreTouch)
WARMUP="${WARMUP:-10s}"; DUR="${DUR:-40s}"
OUT=/tmp/svt-threads-ab.csv
echo "mode,c,threads,rps,p99" > "$OUT"

wait_port(){ for _ in $(seq 1 80); do (exec 3<>/dev/tcp/127.0.0.1/50056) 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

run_one(){
  local n="$1" mode="$2" c="$3"
  PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
  if [ "$mode" = read ]; then
    PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$c-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1
  fi
  NETTY_IO_THREADS="$n" taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" >/tmp/thr-srv.log 2>&1 &
  local pid=$!
  if ! wait_port; then echo "$mode,$c,$n,START_FAIL,0" >> "$OUT"; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; return; fi
  taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:50056 -c "$c" -d "$DUR" -warmup "$WARMUP" -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out /tmp/thr.json >/dev/null 2>&1
  local line
  line=$(python3 -c "import json;r=json.load(open('/tmp/thr.json'));print('%.0f,%.3f'%(r['rps'],r['lat_p99_ms']))" 2>/dev/null || echo "ERR,0")
  echo "$mode,$c,$n,$line" >> "$OUT"
  kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
}

echo "THREADS-AB START $(date +%H:%M:%S)  epoll heap=2G warmup=$WARMUP dur=$DUR"
for mode in execute read; do
  for c in 1 8 32 64 128; do
    for n in 1 2; do
      run_one "$n" "$mode" "$c"
      echo "done $mode c=$c threads=$n $(date +%H:%M:%S)"
    done
  done
done
echo "THREADS-AB DONE $(date +%H:%M:%S)"

echo "=== paired comparison (1 vs 2 I/O threads) ==="
python3 - "$OUT" <<'PY'
import csv,sys
d={}
for r in csv.DictReader(open(sys.argv[1])):
    try: d[(r['mode'],int(r['c']),r['threads'])]=(float(r['rps']),float(r['p99']))
    except: pass
for mode in ['execute','read']:
    cs=sorted({k[1] for k in d if k[0]==mode})
    if not cs: continue
    print(f"\n{mode}:   c |   t1 rps |   t2 rps |  d(t1vs t2) | t1 p99 | t2 p99")
    for c in cs:
        a=d.get((mode,c,'1')); b=d.get((mode,c,'2'))
        if a and b:
            dr=100*(a[0]-b[0])/b[0] if b[0] else 0
            print(f"      {c:4d} | {a[0]:7.0f} | {b[0]:7.0f} | {dr:+6.1f}%   | {a[1]:6.2f} | {b[1]:6.2f}")
PY
