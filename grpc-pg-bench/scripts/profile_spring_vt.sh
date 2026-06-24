#!/usr/bin/env bash
# Reproducible async-profiler capture for the spring-vt server under load.
#
# Starts spring-vt (CPU-pinned, configurable JVM_OPTS), drives it with the
# shared Go loadgen on separate cores, attaches async-profiler at steady state,
# and writes BOTH a machine-readable collapsed file and an openable flamegraph
# HTML into results/profile-spring-vt-<ts>/.
#
# Usage:
#   scripts/profile_spring_vt.sh <event> [mode] [concurrency] [profile_secs]
#     event        itimer | wall | alloc   (default itimer)
#                    itimer = on-CPU time (where cycles burn)
#                    wall   = wall-clock incl. BLOCKED time (lock/pool waits)
#                    alloc  = heap allocation sites (GC pressure)
#     mode         execute | exectx | read (default execute)
#     concurrency  loadgen -c                (default 64)
#     profile_secs async-profiler -d         (default 30)
#
#   Override the JVM (for the param matrix) and pool via env:
#     JVM_OPTS="-Xms512m -Xmx1024m -XX:+UseG1GC" PG_POOL_MAX=32 \
#       scripts/profile_spring_vt.sh itimer execute 128 30
#
# Why itimer/wall/alloc and not `-e cpu`: this box has
# /proc/sys/kernel/perf_event_paranoid = 4, so the kernel denies perf_event_open
# to non-root. async-profiler's itimer (SIGPROF), wall (per-thread sampling) and
# alloc (JVMTI) engines need no perf_events, so they work unprivileged.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT="$(pwd)"
source ./scripts/config.sh 2>/dev/null || true

EVENT="${1:-itimer}"
MODE="${2:-execute}"
C="${3:-64}"
PSECS="${4:-30}"

# Which server to profile (defaults to spring-vt). Override for other stacks:
#   JAR=bin/spring-rt-bench.jar LISTEN_PORT=50058 LABEL=spring-rt \
#     scripts/profile_spring_vt.sh wall execute 64 30
JAR="${JAR:-bin/spring-vt-bench.jar}"
LABEL="${LABEL:-spring-vt}"

# --- DB / server env (matches the benchmark) ---
export PG_HOST="${PG_HOST:-127.0.0.1}" PG_PORT="${PG_PORT:-5432}" PG_DB="${PG_DB:-bench}"
export PG_USER="${PG_USER:-postgres}" PG_PASSWORD="${PG_PASSWORD:-sam}"
export DATABASE_URL="postgres://${PG_USER}:${PG_PASSWORD}@${PG_HOST}:${PG_PORT}/${PG_DB}?sslmode=disable"
export LISTEN_PORT="${LISTEN_PORT:-50056}"
export PG_POOL_MAX="${PG_POOL_MAX:-16}" PG_POOL_MIN="${PG_POOL_MIN:-4}"
# spring-vt tuning runs use a 2 GB fixed heap (was 1 GB): more headroom for ZGC
# means fewer GC cycles → smoother tail latency. Fits the 4 GB server cgroup.
JVM_OPTS="${JVM_OPTS:--Xms2048m -Xmx2048m -XX:+UseZGC -XX:+AlwaysPreTouch}"
SERVER_CPUS="${PIN_SERVER_CPUS:-2,3}"
CLIENT_CPUS="${PIN_CLIENT_CPUS:-4,5}"

# Pick up SDKMAN JDK 25.
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u; source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true; set -u
fi
command -v asprof >/dev/null || { echo "asprof (async-profiler) not on PATH"; exit 1; }

TS="$(date +%Y%m%d-%H%M%S)"
OUT="${ROOT}/results/profile-${LABEL}-${TS}"
mkdir -p "${OUT}"
echo ">> output dir: ${OUT}"
echo ">> event=${EVENT} mode=${MODE} c=${C} profile_secs=${PSECS} pool_max=${PG_POOL_MAX}"
echo ">> JVM_OPTS=${JVM_OPTS}"
{ echo "event=${EVENT} mode=${MODE} c=${C} psecs=${PSECS}"; echo "JVM_OPTS=${JVM_OPTS}";
  echo "PG_POOL_MAX=${PG_POOL_MAX} PG_POOL_MIN=${PG_POOL_MIN}"; } > "${OUT}/params.txt"

# Clean slate; seed workflow_state for read/mixed (same grid the loadgen reads).
PGPASSWORD="${PG_PASSWORD}" psql "${DATABASE_URL}" -q \
  -c "TRUNCATE commands, workflow_state, outbox RESTART IDENTITY;" >/dev/null 2>&1 || true
if [ "${MODE}" = "read" ] || [ "${MODE}" = "mixed" ]; then
  PGPASSWORD="${PG_PASSWORD}" psql "${DATABASE_URL}" -q -c "
    INSERT INTO workflow_state (workflow_id,state,version,updated_at)
    SELECT 'wf-'||w||'-'||k,'seed',1,now()
    FROM generate_series(0,${C}-1) w, generate_series(0,9999) k
    ON CONFLICT DO NOTHING;" >/dev/null 2>&1 || true
fi

# Start server. -XX:+EnableDynamicAgentLoading lets async-profiler attach at
# runtime without the JDK "dynamic agent loading" warning.
# shellcheck disable=SC2086
taskset -c "${SERVER_CPUS}" java ${JVM_OPTS} -XX:+EnableDynamicAgentLoading \
  -jar "${ROOT}/${JAR}" > "${OUT}/server.log" 2>&1 &
PID=$!
echo ">> server PID=${PID}, waiting for port ${LISTEN_PORT}..."
for _ in $(seq 1 80); do
  (exec 3<>"/dev/tcp/127.0.0.1/${LISTEN_PORT}") 2>/dev/null && { exec 3>&- 3<&-; break; }
  sleep 0.5
done

# Load runs long enough to cover steady-state warmup + two capture windows
# (collapsed + flamegraph). 15s warmup + 2*PSECS + 8s buffer.
LOAD_SECS=$(( 15 + 2*PSECS + 8 ))
# shellcheck disable=SC2086
taskset -c "${CLIENT_CPUS}" "${ROOT}/bin/loadgen" -addr "127.0.0.1:${LISTEN_PORT}" \
  -c "${C}" -d "${LOAD_SECS}s" -warmup 0s -payload 256 -conns 4 -mode "${MODE}" \
  -keyspace 10000 -out "${OUT}/load.json" > "${OUT}/load.txt" 2>&1 &
LG=$!

sleep 15  # reach steady state before sampling
echo ">> [1/2] ${EVENT} -> collapsed (${PSECS}s)"
asprof -d "${PSECS}" -e "${EVENT}" -o collapsed  -f "${OUT}/${EVENT}.collapsed" "${PID}" 2>&1 | tail -2
echo ">> [2/2] ${EVENT} -> flamegraph html (${PSECS}s)"
asprof -d "${PSECS}" -e "${EVENT}" -o flamegraph -f "${OUT}/${EVENT}.html"      "${PID}" 2>&1 | tail -2

wait "${LG}" 2>/dev/null || true
kill -TERM "${PID}" 2>/dev/null || true
wait "${PID}" 2>/dev/null || true

echo
echo "=== throughput during capture (${MODE} c=${C}) ==="
grep -E '"rps"|"lat_p99_ms"|"total_err"' "${OUT}/load.json" 2>/dev/null || cat "${OUT}/load.txt"
echo
echo "=== analysis: ${EVENT} ==="
python3 "${ROOT}/scripts/analyze_profile.py" "${OUT}/${EVENT}.collapsed" 30
echo
echo ">> flamegraph (open in a browser to cross-check): ${OUT}/${EVENT}.html"
