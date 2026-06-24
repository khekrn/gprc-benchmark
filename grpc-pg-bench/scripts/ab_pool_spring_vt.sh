#!/usr/bin/env bash
# HikariCP pool-size sweep for spring-vt (epoll + 1 I/O thread + 2 GB), same
# binary. Tests whether throughput rises with pool size (pool-bound) or stays
# flat (CPU/DB-bound). CPU profile already showed Hikari ~0.1% CPU, so we expect
# FLAT. Pools 16/32/64 at the high-concurrency levels (64,128) where contention
# would show. Interleaved per (mode,c).
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
export PG_HOST=127.0.0.1 PG_PORT=5432 PG_DB=bench PG_USER=postgres PG_PASSWORD=sam
export DATABASE_URL="postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"
export LISTEN_PORT=50056
JVM_OPTS=(-Xms2048m -Xmx2048m -XX:+UseZGC -XX:+AlwaysPreTouch)
WARMUP="${WARMUP:-10s}"; DUR="${DUR:-30s}"
POOLS=(16 32 64)
OUT=/tmp/svt-pool-ab.csv
echo "mode,c,pool,rps,p99" > "$OUT"

wait_port(){ for _ in $(seq 1 80); do (exec 3<>/dev/tcp/127.0.0.1/50056) 2>/dev/null && { exec 3>&- 3<&-; return 0; }; sleep 0.5; done; return 1; }

run_one(){
  local pool="$1" mode="$2" c="$3"
  PGPASSWORD=sam psql "$DATABASE_URL" -q -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1
  if [ "$mode" = read ]; then
    PGPASSWORD=sam psql "$DATABASE_URL" -q -c "INSERT INTO workflow_state(workflow_id,state,version,updated_at) SELECT 'wf-'||w||'-'||k,'seed',1,now() FROM generate_series(0,$c-1) w, generate_series(0,9999) k ON CONFLICT DO NOTHING;" >/dev/null 2>&1
  fi
  PG_POOL_MAX="$pool" PG_POOL_MIN=4 taskset -c 2,3 java "${JVM_OPTS[@]}" -jar "$ROOT/bin/spring-vt-bench.jar" >/tmp/pool-srv.log 2>&1 &
  local pid=$!
  if ! wait_port; then echo "$mode,$c,$pool,START_FAIL,0" >> "$OUT"; kill "$pid" 2>/dev/null; wait "$pid" 2>/dev/null; return; fi
  taskset -c 4,5 "$ROOT/bin/loadgen" -addr 127.0.0.1:50056 -c "$c" -d "$DUR" -warmup "$WARMUP" -payload 256 -conns 4 -mode "$mode" -keyspace 10000 -out /tmp/pool.json >/dev/null 2>&1
  local line; line=$(python3 -c "import json;r=json.load(open('/tmp/pool.json'));print('%.0f,%.3f'%(r['rps'],r['lat_p99_ms']))" 2>/dev/null || echo "ERR,0")
  echo "$mode,$c,$pool,$line" >> "$OUT"
  kill -TERM "$pid" 2>/dev/null; wait "$pid" 2>/dev/null
}

echo "POOL-SWEEP START $(date +%H:%M:%S)  warmup=$WARMUP dur=$DUR"
for mode in execute read; do
  for c in 64 128; do
    for pool in "${POOLS[@]}"; do
      run_one "$pool" "$mode" "$c"
      echo "done $mode c=$c pool=$pool $(date +%H:%M:%S)"
    done
  done
done
echo "POOL-SWEEP DONE $(date +%H:%M:%S)"

echo "=== HikariCP pool-size sweep (rps / p99) ==="
python3 - "$OUT" <<'PY'
import csv,sys
d={}
for r in csv.DictReader(open(sys.argv[1])):
    try: d[(r['mode'],int(r['c']),int(r['pool']))]=(float(r['rps']),float(r['p99']))
    except: pass
pools=[16,32,64]
for mode in ['execute','read']:
    cs=sorted({k[1] for k in d if k[0]==mode})
    if not cs: continue
    print(f"\n{mode}:  c | " + " | ".join(f"pool={p:>3}" for p in pools))
    for c in cs:
        cells=[]
        for p in pools:
            if (mode,c,p) in d:
                rps,p99=d[(mode,c,p)]; cells.append(f"{rps:6.0f}({p99:5.1f})")
            else: cells.append("   --   ")
        print(f"   {c:4d} | " + " | ".join(cells))
PY
