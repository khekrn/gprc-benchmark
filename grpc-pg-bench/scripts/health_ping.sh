#!/usr/bin/env bash
# External REST health-check probe for the co-host experiment.
#
# While the gRPC benchmark hammers spring-vt (Netty, port 50056), this pings the
# Jetty REST /health endpoint every INTERVAL seconds from OUTSIDE the benchmark
# script. The point is to see whether co-hosting two network stacks in one JVM on
# a 2-core box makes the *cheap* REST liveness probe stall — every ping does ~zero
# server work, so any latency spike is contention (CPU starvation, GC pause, Netty
# vs Jetty event-loop/thread fighting), not the handler.
#
# Pin it to a core the server (2,3) and loadgen (4,5) do NOT use so the probe is a
# bystander, not a competitor — on the 6-core box cores 0,1 are free.
#
# Usage:
#   scripts/health_ping.sh                       # 127.0.0.1:8080, every 5s, until Ctrl-C
#   HOST=127.0.0.1 PORT=8080 INTERVAL=5 scripts/health_ping.sh
#   DURATION=1830 scripts/health_ping.sh         # auto-stop after ~30m + a margin
#   OUT=results/.../health.csv scripts/health_ping.sh
#
# Output: a CSV (epoch, iso, http_code, latency_ms) — one row per ping — plus a
# end-of-run summary (count, fails/timeouts, p50/p99/max latency).
set -uo pipefail
cd "$(dirname "$0")/.."

HOST="${HOST:-127.0.0.1}"
PORT="${PORT:-${SPRING_VT_HTTP_PORT:-8080}}"
INTERVAL="${INTERVAL:-5}"
DURATION="${DURATION:-0}"                 # 0 = run until killed
MAX_TIME="${MAX_TIME:-5}"                 # per-request timeout (s); longer = a stall
PIN_CPUS="${PIN_CPUS:-0,1}"               # cores the bench does NOT use
URL="http://${HOST}:${PORT}/health"

TS="$(date +%Y%m%d-%H%M%S)"
OUT="${OUT:-results/health-ping-${TS}.csv}"
mkdir -p "$(dirname "$OUT")"
echo "epoch,iso,http_code,latency_ms" > "$OUT"

PIN=()
if command -v taskset >/dev/null 2>&1; then PIN=(taskset -c "${PIN_CPUS}"); fi

echo "HEALTH-PING $URL every ${INTERVAL}s (timeout ${MAX_TIME}s, pin ${PIN_CPUS})  -> $OUT"
[ "$DURATION" != "0" ] && echo "  will auto-stop after ${DURATION}s"

START=$(date +%s)
trap 'summary; exit 0' INT TERM

summary() {
  echo ""
  echo "=== health-ping summary ($OUT) ==="
  awk -F, 'NR>1{
      n++;
      if ($3!="200") bad++;
      ms=$4+0; arr[n]=ms; sum+=ms; if(ms>max)max=ms;
    }
    END{
      if(n==0){print "no samples"; exit}
      # sort latencies for percentiles
      for(i=1;i<=n;i++) for(j=i+1;j<=n;j++) if(arr[j]<arr[i]){t=arr[i];arr[i]=arr[j];arr[j]=t}
      p50=arr[int(0.50*n)+ (n%2?0:0)]; if(p50=="")p50=arr[1];
      p99i=int(0.99*n); if(p99i<1)p99i=1; if(p99i>n)p99i=n;
      printf "samples=%d  non200=%d  avg=%.1fms  p50=%.1fms  p99=%.1fms  max=%.1fms\n",
             n, bad+0, sum/n, arr[int(0.50*n)<1?1:int(0.50*n)], arr[p99i], max;
    }' "$OUT"
}

while :; do
  NOW=$(date +%s)
  if [ "$DURATION" != "0" ] && [ $((NOW - START)) -ge "$DURATION" ]; then break; fi
  # %{http_code} and %{time_total}; --max-time turns a hung server into code 000.
  RES="$("${PIN[@]}" curl -s -o /dev/null --max-time "$MAX_TIME" \
        -w '%{http_code} %{time_total}' "$URL" 2>/dev/null || echo '000 0')"
  CODE="${RES%% *}"; T="${RES##* }"
  MS="$(awk "BEGIN{printf \"%.1f\", ${T:-0}*1000}")"
  ISO="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "${NOW},${ISO},${CODE},${MS}" >> "$OUT"
  # Flag stalls inline so they're visible in the live console too.
  if [ "$CODE" != "200" ]; then
    echo "  [!] ${ISO} code=${CODE} latency=${MS}ms  (stall/fail)"
  else
    awk "BEGIN{exit !(${MS} > 100)}" && echo "  [~] ${ISO} slow ping ${MS}ms"
  fi
  sleep "$INTERVAL"
done
summary
