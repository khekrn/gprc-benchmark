#!/usr/bin/env bash
# Full benchmark orchestrator.
#
# For each stack (go-pgx, kotlin-vertx) and each concurrency level:
#   1. start the server (CPU-pinned)
#   2. wait until it accepts connections
#   3. TRUNCATE the table for a clean slate
#   4. run the load generator (warmup + measured), save JSON
#   5. stop the server
#
# Results land in results/<timestamp>/ as one JSON per (stack, concurrency),
# plus a summary.csv. Designed for a 2-core / 4 GB box: only one server runs
# at a time, so the JVM and Go process never compete with each other.
#
# Usage:
#   ./scripts/run_benchmark.sh                # both stacks, default sweep
#   STACKS="go-pgx" ./scripts/run_benchmark.sh
#   CONCURRENCY_LEVELS="32 64" ./scripts/run_benchmark.sh
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh
# Pick up SDKMAN-installed java/mvn if the user manages JVMs that way.
# Without this, the Kotlin server start fails with "taskset: failed to
# execute java: No such file or directory" because the script's PATH
# doesn't include ~/.sdkman/candidates/*/current/bin by default.
# Toggle set -u off briefly: sdkman-init.sh references several unset vars
# of its own and would otherwise abort the whole script.
if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u
  source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true
  set -u
fi

STACKS="${STACKS:-go-pgx kotlin-vertx rust-tokio}"
LOADGEN="${ROOT_DIR}/bin/loadgen"
[ -x "${LOADGEN}" ] || { echo "Build loadgen first: ./scripts/build_go.sh"; exit 1; }

TS="$(date +%Y%m%d-%H%M%S)"
RUN_DIR="${RESULTS_DIR}/${TS}"
mkdir -p "${RUN_DIR}"
SUMMARY="${RUN_DIR}/summary.csv"
echo "stack,concurrency,rps,p50_ms,p90_ms,p99_ms,p999_ms,max_ms,total_ok,total_err" > "${SUMMARY}"

# Record environment for reproducibility.
{
  echo "timestamp: ${TS}"
  echo "stacks: ${STACKS}"
  echo "concurrency_levels: ${CONCURRENCY_LEVELS}"
  echo "warmup: ${WARMUP}  duration: ${DURATION}  payload: ${PAYLOAD}B  client_conns: ${CLIENT_CONNS}"
  echo "pg_pool: min=${PG_POOL_MIN} max=${PG_POOL_MAX}"
  echo "server_cpus: ${PIN_SERVER_CPUS}  client_cpus: ${PIN_CLIENT_CPUS}  mem_max: ${MEM_MAX:-(unset)}"
  echo "gomaxprocs: ${GOMAXPROCS}  vertx_event_loops: ${VERTX_EVENT_LOOPS}  jvm_opts: ${JVM_OPTS}"
  echo "--- uname ---"; uname -a
  echo "--- cpu ---"; (lscpu 2>/dev/null | grep -E 'Model name|CPU\(s\)|MHz' || sysctl -n machdep.cpu.brand_string 2>/dev/null) || true
  echo "--- mem ---"; (free -h 2>/dev/null || vm_stat 2>/dev/null) || true
} > "${RUN_DIR}/environment.txt"

truncate_table() {
  PGPASSWORD="${PG_PASSWORD}" psql "${DATABASE_URL}" -q -c "TRUNCATE commands RESTART IDENTITY;" >/dev/null 2>&1 || true
}

wait_for_port() {
  local host="$1" port="$2" tries=60
  for _ in $(seq 1 "${tries}"); do
    if (exec 3<>"/dev/tcp/${host}/${port}") 2>/dev/null; then exec 3>&- 3<&-; return 0; fi
    sleep 0.5
  done
  return 1
}

start_server() {
  local stack="$1"
  # Each (stack, concurrency) run gets its own transient systemd --user scope
  # so MemoryMax (and the whole process group) is isolated. SERVER_UNIT is the
  # scope name; stop_server kills via `systemctl --user kill`, which is more
  # reliable than `kill $!` once systemd-run is in the picture (systemd-run's
  # own PID is what $! captures, and it doesn't forward signals to the cgroup).
  SERVER_UNIT="bench-${stack}-c${c}-$$.scope"
  local memcap=()
  if [ -n "${MEM_MAX:-}" ] && command -v systemd-run >/dev/null 2>&1; then
    memcap=(systemd-run --user --scope --quiet
      --unit="${SERVER_UNIT%.scope}"
      -p MemoryMax="${MEM_MAX}" -p MemorySwapMax=0 -p TasksMax=infinity)
  else
    SERVER_UNIT=""
  fi

  # Append (>>) so we keep the log from every concurrency level in the sweep
  # — overwriting (>) made debugging stale-server bugs much harder.
  # `${ARR[@]+"${ARR[@]}"}` expands to the array contents if set+non-empty,
  # otherwise to nothing — needed because `set -u` rejects `${ARR[@]}` when
  # the array is empty.
  case "${stack}" in
    go-pgx)
      LISTEN_ADDR="${GO_ADDR}" \
        ${memcap[@]+"${memcap[@]}"} \
        ${SERVER_PIN[@]+"${SERVER_PIN[@]}"} "${ROOT_DIR}/bin/go-server" \
        >> "${RUN_DIR}/${stack}.server.log" 2>&1 &
      SERVER_PID=$!
      wait_for_port "${GO_ADDR%%:*}" "${GO_ADDR##*:}"
      TARGET_ADDR="${GO_ADDR}"
      ;;
    kotlin-vertx)
      # shellcheck disable=SC2086
      LISTEN_HOST="${KOTLIN_HOST}" LISTEN_PORT="${KOTLIN_PORT}" \
        ${memcap[@]+"${memcap[@]}"} \
        ${SERVER_PIN[@]+"${SERVER_PIN[@]}"} java ${JVM_OPTS} -jar "${ROOT_DIR}/bin/kotlin-vertx-bench.jar" \
        >> "${RUN_DIR}/${stack}.server.log" 2>&1 &
      SERVER_PID=$!
      wait_for_port "${KOTLIN_HOST}" "${KOTLIN_PORT}"
      TARGET_ADDR="${KOTLIN_ADDR}"
      ;;
    rust-tokio)
      LISTEN_ADDR="${RUST_ADDR}" RUST_WORKER_THREADS="${RUST_WORKER_THREADS}" \
        ${memcap[@]+"${memcap[@]}"} \
        ${SERVER_PIN[@]+"${SERVER_PIN[@]}"} "${ROOT_DIR}/bin/rust-server" \
        >> "${RUN_DIR}/${stack}.server.log" 2>&1 &
      SERVER_PID=$!
      wait_for_port "${RUST_ADDR%%:*}" "${RUST_ADDR##*:}"
      TARGET_ADDR="${RUST_ADDR}"
      ;;
    *)
      echo "unknown stack: ${stack}" >&2
      return 1
      ;;
  esac
}

stop_server() {
  # When a systemd scope wraps the server, `systemctl --user kill` reaches
  # every process in the cgroup — kill $! only hits systemd-run itself.
  if [ -n "${SERVER_UNIT:-}" ] && systemctl --user is-active --quiet "${SERVER_UNIT}" 2>/dev/null; then
    systemctl --user kill --signal=TERM "${SERVER_UNIT}" 2>/dev/null || true
    for _ in $(seq 1 30); do
      systemctl --user is-active --quiet "${SERVER_UNIT}" 2>/dev/null || break
      sleep 0.5
    done
    if systemctl --user is-active --quiet "${SERVER_UNIT}" 2>/dev/null; then
      echo "WARN: scope ${SERVER_UNIT} did not exit on SIGTERM, forcing stop" >&2
      systemctl --user stop "${SERVER_UNIT}" 2>/dev/null || true
    fi
  fi
  # Fallback path (MEM_MAX unset, or systemd-run absent): direct PID kill.
  if [ -n "${SERVER_PID:-}" ] && kill -0 "${SERVER_PID}" 2>/dev/null; then
    kill -TERM "${SERVER_PID}" 2>/dev/null || true
    for _ in $(seq 1 20); do
      kill -0 "${SERVER_PID}" 2>/dev/null || break
      sleep 0.5
    done
    if kill -0 "${SERVER_PID}" 2>/dev/null; then
      echo "WARN: server ${SERVER_PID} did not exit on SIGTERM, sending SIGKILL" >&2
      kill -KILL "${SERVER_PID}" 2>/dev/null || true
    fi
    wait "${SERVER_PID}" 2>/dev/null || true
  fi
  SERVER_PID=""
  SERVER_UNIT=""
}
trap stop_server EXIT

for stack in ${STACKS}; do
  echo "==================================================================="
  echo " STACK: ${stack}"
  echo "==================================================================="
  for c in ${CONCURRENCY_LEVELS}; do
    echo ">> [${stack}] concurrency=${c}"
    start_server "${stack}"
    if [ -z "${TARGET_ADDR:-}" ]; then echo "server failed to start; see log"; cat "${RUN_DIR}/${stack}.server.log"; exit 1; fi
    sleep 1
    truncate_table

    OUT_JSON="${RUN_DIR}/${stack}-c${c}.json"
    ${CLIENT_PIN[@]+"${CLIENT_PIN[@]}"} "${LOADGEN}" \
      -addr "${TARGET_ADDR}" \
      -c "${c}" \
      -d "${DURATION}" \
      -warmup "${WARMUP}" \
      -payload "${PAYLOAD}" \
      -conns "${CLIENT_CONNS}" \
      -label "${stack}" \
      -out "${OUT_JSON}" \
      > "${RUN_DIR}/${stack}-c${c}.stdout" 2> "${RUN_DIR}/${stack}-c${c}.stderr" || true

    stop_server

    # Append to summary.csv via a tiny inline parser (no jq dependency).
    if [ -f "${OUT_JSON}" ]; then
      python3 - "${OUT_JSON}" "${stack}" "${c}" >> "${SUMMARY}" <<'PY'
import json, sys
path, stack, c = sys.argv[1], sys.argv[2], sys.argv[3]
with open(path) as f:
    r = json.load(f)
print("{},{},{:.0f},{:.3f},{:.3f},{:.3f},{:.3f},{:.3f},{},{}".format(
    stack, c, r["rps"], r["lat_p50_ms"], r["lat_p90_ms"], r["lat_p99_ms"],
    r["lat_p999_ms"], r["lat_max_ms"], r["total_ok"], r["total_err"]))
PY
    fi
  done
done

echo
echo ">> Benchmark complete. Results in: ${RUN_DIR}"
echo ">> Summary:"
column -s, -t "${SUMMARY}" 2>/dev/null || cat "${SUMMARY}"
