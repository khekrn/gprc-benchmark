#!/usr/bin/env bash
# Controlled same-session A/B of grpc-netty NIO vs epoll for spring-vt.
# Same binary, NETTY_TRANSPORT flipped, interleaved per (mode,c) so box drift
# hits both equally. Writes /tmp/svt-ab.csv and prints a paired comparison.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"

if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi

export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=16 PG_POOL_MIN=4
export LISTEN_PORT=50056
JVM_OPTS=(-Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch)
WARMUP="${WARMUP:-10s}"; DUR="${DUR:-40s}"
OUT=/tmp/svt-ab.csv
echo "mode,c,transport,rps,p99" > "$OUT"

wait_port(){ for _ in $(seq 1 80); do (exec 3<>/dev/tcp/127.0.0.1/50056) 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

run_one(){
  local t="$1" mode="$2" c="$3"
  PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
  if [ "$mode" = read ]; then
    PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$c-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1
  fi
  NETTY_TRANSPORT="$t" taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" >/tmp/ab-srv.log 2>&1 &
  local pid=$!
  if ! wait_port; then echo "$mode,$c,$t,START_FAIL,0" >> "$OUT"; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; return; fi
  taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:50056 -c "$c" -d "$DUR" -warmup "$WARMUP" -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out /tmp/ab.json >/dev/null 2>&1
  local line
  line=$(python3 -c "import json;r=json.load(open('/tmp/ab.json'));print('%.0f,%.3f'%(r['rps'],r['lat_p99_ms']))" 2>/dev/null || echo "ERR,0")
  echo "$mode,$c,$t,$line" >> "$OUT"
  kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
}

echo "AB START $(date +%H:%M:%S)  warmup=$WARMUP dur=$DUR"
for mode in execute read; do
  for c in 1 8 32 64 128; do
    for t in nio epoll; do
      run_one "$t" "$mode" "$c"
      echo "done $mode c=$c $t $(date +%H:%M:%S)"
    done
  done
done
echo "AB DONE $(date +%H:%M:%S)"

echo "=== paired comparison ==="
python3 - "$OUT" <<'PY'
import csv,sys
d={}
for r in csv.DictReader(open(sys.argv[1])):
    try: d[(r['mode'],int(r['c']),r['transport'])]=(float(r['rps']),float(r['p99']))
    except: pass
for mode in ['execute','read']:
    cs=sorted({k[1] for k in d if k[0]==mode})
    if not cs: continue
    print(f"\n{mode}:   c |   nio rps |  epoll rps |  d rps  |  nio p99 | epoll p99")
    for c in cs:
        n=d.get((mode,c,'nio')); e=d.get((mode,c,'epoll'))
        if n and e:
            dr=100*(e[0]-n[0])/n[0] if n[0] else 0
            print(f"      {c:4d} | {n[0]:8.0f} | {e[0]:9.0f} | {dr:+5.1f}% | {n[1]:7.2f} | {e[1]:7.2f}")
PY
