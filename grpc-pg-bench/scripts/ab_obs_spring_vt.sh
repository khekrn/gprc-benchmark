#!/usr/bin/env bash
# Controlled same-session A/B: Spring gRPC per-RPC observation ON vs OFF.
# Same epoll binary, 2 GB heap; the OFF arm adds
#   -Dspring.grpc.server.observation.enabled=false
# Interleaved per (mode,c) so box drift hits both arms equally.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=16 PG_POOL_MIN=4 LISTEN_PORT=50056
JVM_OPTS=(-Xms2048m -Xmx2048m -XX:+UseZGC -XX:+AlwaysPreTouch)   # 2 GB heap
WARMUP="${WARMUP:-10s}"; DUR="${DUR:-40s}"
OUT=/tmp/svt-obs-ab.csv
echo "mode,c,arm,rps,p99" > "$OUT"

wait_port(){ for _ in $(seq 1 80); do (exec 3<>/dev/tcp/127.0.0.1/50056) 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

run_one(){
  local arm="$1" mode="$2" c="$3"
  local obs=()
  [ "$arm" = off ] && obs=(-Dspring.grpc.server.observation.enabled=false)
  PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
  if [ "$mode" = read ]; then
    PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$c-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1
  fi
  taskset -c 2,3 java "${JVM_OPTS[@]}" "${obs[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" >/tmp/obs-srv.log 2>&1 &
  local pid=$!
  if ! wait_port; then echo "$mode,$c,$arm,START_FAIL,0" >> "$OUT"; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; return; fi
  taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:50056 -c "$c" -d "$DUR" -warmup "$WARMUP" -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out /tmp/obs.json >/dev/null 2>&1
  local line
  line=$(python3 -c "import json;r=json.load(open('/tmp/obs.json'));print('%.0f,%.3f'%(r['rps'],r['lat_p99_ms']))" 2>/dev/null || echo "ERR,0")
  echo "$mode,$c,$arm,$line" >> "$OUT"
  kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
}

echo "OBS-AB START $(date +%H:%M:%S)  heap=2G transport=epoll warmup=$WARMUP dur=$DUR"
for mode in execute read; do
  for c in 1 8 32 64 128; do
    for arm in on off; do
      run_one "$arm" "$mode" "$c"
      echo "done $mode c=$c obs=$arm $(date +%H:%M:%S)"
    done
  done
done
echo "OBS-AB DONE $(date +%H:%M:%S)"

echo "=== paired comparison (observation ON vs OFF) ==="
python3 - "$OUT" <<'PY'
import csv,sys
d={}
for r in csv.DictReader(open(sys.argv[1])):
    try: d[(r['mode'],int(r['c']),r['arm'])]=(float(r['rps']),float(r['p99']))
    except: pass
for mode in ['execute','read']:
    cs=sorted({k[1] for k in d if k[0]==mode})
    if not cs: continue
    print(f"\n{mode}:   c |   on rps |  off rps |  d rps  |  on p99 | off p99")
    for c in cs:
        a=d.get((mode,c,'on')); b=d.get((mode,c,'off'))
        if a and b:
            dr=100*(b[0]-a[0])/a[0] if a[0] else 0
            print(f"      {c:4d} | {a[0]:7.0f} | {b[0]:7.0f} | {dr:+5.1f}% | {a[1]:6.2f} | {b[1]:6.2f}")
PY
