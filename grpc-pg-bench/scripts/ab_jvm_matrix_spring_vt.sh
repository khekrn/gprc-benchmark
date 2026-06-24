#!/usr/bin/env bash
# JVM-param matrix for spring-vt (epoll + 1 I/O thread, 2 GB), same binary.
# Configs interleaved per (mode,c) so box drift hits all equally.
#   zgc      : ZGC (baseline)
#   g1       : G1
#   parallel : Parallel GC (throughput collector)
#   coh      : ZGC + experimental compact object headers (16->8 byte headers)
# Profile says GC is only ~2.6% of CPU, so expect small effects — measured, not assumed.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export PG_POOL_MAX=16 PG_POOL_MIN=4 LISTEN_PORT=50056
WARMUP="${WARMUP:-10s}"; DUR="${DUR:-40s}"
HEAP="-Xms2048m -Xmx2048m -XX:+AlwaysPreTouch"
declare -A CFG=(
  [zgc]="-XX:+UseZGC"
  [g1]="-XX:+UseG1GC"
  [parallel]="-XX:+UseParallelGC"
  [coh]="-XX:+UseZGC -XX:+UnlockExperimentalVMOptions -XX:+UseCompactObjectHeaders"
)
ORDER=(zgc g1 parallel coh)
OUT=/tmp/svt-jvm-ab.csv
echo "mode,c,cfg,rps,p99" > "$OUT"

wait_port(){ for _ in $(seq 1 80); do (exec 3<>/dev/tcp/127.0.0.1/50056) 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

run_one(){
  local cfg="$1" mode="$2" c="$3"
  PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
  if [ "$mode" = read ]; then
    PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$c-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1
  fi
  # shellcheck disable=SC2086
  taskset -c 2,3 java $HEAP ${CFG[$cfg]} -jar "$ROOT/bin/spring-vt-bench.jar" >/tmp/jvm-srv.log 2>&1 &
  local pid=$!
  if ! wait_port; then echo "$mode,$c,$cfg,START_FAIL,0" >> "$OUT"; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; return; fi
  taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:50056 -c "$c" -d "$DUR" -warmup "$WARMUP" -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out /tmp/jvm.json >/dev/null 2>&1
  local line; line=$(python3 -c "import json;r=json.load(open('/tmp/jvm.json'));print('%.0f,%.3f'%(r['rps'],r['lat_p99_ms']))" 2>/dev/null || echo "ERR,0")
  echo "$mode,$c,$cfg,$line" >> "$OUT"
  kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
}

echo "JVM-MATRIX START $(date +%H:%M:%S)  warmup=$WARMUP dur=$DUR"
for mode in execute read; do
  for c in 32 64 128; do
    for cfg in "${ORDER[@]}"; do
      run_one "$cfg" "$mode" "$c"
      echo "done $mode c=$c $cfg $(date +%H:%M:%S)"
    done
  done
done
echo "JVM-MATRIX DONE $(date +%H:%M:%S)"

echo "=== JVM config matrix (rps / p99) ==="
python3 - "$OUT" <<'PY'
import csv,sys
d={}
for r in csv.DictReader(open(sys.argv[1])):
    try: d[(r['mode'],int(r['c']),r['cfg'])]=(float(r['rps']),float(r['p99']))
    except: pass
order=['zgc','g1','parallel','coh']
for mode in ['execute','read']:
    cs=sorted({k[1] for k in d if k[0]==mode})
    if not cs: continue
    print(f"\n{mode} rps (p99 ms):  c | " + " | ".join(f"{o:>14}" for o in order))
    for c in cs:
        cells=[]
        best=max((d[(mode,c,o)][0] for o in order if (mode,c,o) in d), default=0)
        for o in order:
            if (mode,c,o) in d:
                rps,p99=d[(mode,c,o)]
                tag='*' if rps==best else ' '
                cells.append(f"{rps:6.0f}{tag}({p99:5.1f})")
            else: cells.append(f"{'--':>14}")
        print(f"   {c:18d} | " + " | ".join(f"{x:>14}" for x in cells))
PY
