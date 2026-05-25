#!/usr/bin/env bash
# Shared configuration for the benchmark scripts.
# Override any value by exporting it before running a script.

# --- Postgres ---
export PG_HOST="${PG_HOST:-127.0.0.1}"
export PG_PORT="${PG_PORT:-5432}"
export PG_DB="${PG_DB:-bench}"
export PG_USER="${PG_USER:-bench}"
export PG_PASSWORD="${PG_PASSWORD:-bench}"
export DATABASE_URL="${DATABASE_URL:-postgres://${PG_USER}:${PG_PASSWORD}@${PG_HOST}:${PG_PORT}/${PG_DB}?sslmode=disable}"

# --- Server listen addresses ---
export GO_ADDR="${GO_ADDR:-127.0.0.1:50051}"
export KOTLIN_HOST="${KOTLIN_HOST:-127.0.0.1}"
export KOTLIN_PORT="${KOTLIN_PORT:-50052}"
export KOTLIN_ADDR="${KOTLIN_HOST}:${KOTLIN_PORT}"

# --- Resource limits (2 cores / 4 GB box) ---
# We pin servers to cores 0-1 and the load generator to... the same cores,
# because on a 2-core box there is nowhere else to put it. This is the honest
# constraint: client and server contend, exactly as your target hardware will.
export PIN_SERVER_CPUS="${PIN_SERVER_CPUS:-0,1}"
export PIN_CLIENT_CPUS="${PIN_CLIENT_CPUS:-0,1}"

# Go runtime
export GOMAXPROCS="${GOMAXPROCS:-2}"

# JVM heap and GC, tuned for Java 25 + the 4 GB box:
#   - 1 GB max heap leaves room for Postgres + OS.
#   - ZGC is the right pick at this size on 25: sub-ms pauses with negligible
#     throughput cost on small heaps, no pause-target knob to mistune. It is
#     generational by default on 24+ (the explicit +ZGenerational flag was
#     removed in 24, so passing it now would just be ignored with a warning).
#   - AlwaysPreTouch front-loads page faults so the first measured phase
#     isn't taxed by lazy commit.
export JVM_OPTS="${JVM_OPTS:--Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch}"
export VERTX_EVENT_LOOPS="${VERTX_EVENT_LOOPS:-2}"

# Connection pool size (identical for both stacks)
export PG_POOL_MAX="${PG_POOL_MAX:-16}"
export PG_POOL_MIN="${PG_POOL_MIN:-4}"

# --- Load profile ---
export WARMUP="${WARMUP:-5s}"
export DURATION="${DURATION:-30s}"
export PAYLOAD="${PAYLOAD:-256}"
export CLIENT_CONNS="${CLIENT_CONNS:-4}"
# Concurrency levels to sweep. Edit to taste.
export CONCURRENCY_LEVELS="${CONCURRENCY_LEVELS:-1 8 32 64 128}"

# Directories
export ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
export RESULTS_DIR="${RESULTS_DIR:-${ROOT_DIR}/results}"

# taskset wrapper (no-op if taskset unavailable, e.g. on macOS).
#
# We use *prefix arrays* rather than functions because the orchestrator
# backgrounds the server (`cmd &`). If the wrapper is a shell function,
# `cmd &` runs the function in a subshell, and `$!` captures that subshell's
# PID — not the server's. Killing $! then leaves the real server reparented
# to launchd/init, still holding the port. Prefix arrays expand inline, so
# `$!` is the actual server PID and `kill $!` works.
if command -v taskset >/dev/null 2>&1; then
  SERVER_PIN=(taskset -c "${PIN_SERVER_CPUS}")
  CLIENT_PIN=(taskset -c "${PIN_CLIENT_CPUS}")
else
  SERVER_PIN=()
  CLIENT_PIN=()
fi
# Foreground helpers used by run_go_server.sh / run_kotlin_server.sh (they
# `exec` into us, so the no-double-fork concern doesn't apply there).
server_pin() { "${SERVER_PIN[@]}" "$@"; }
client_pin() { "${CLIENT_PIN[@]}" "$@"; }
export -f server_pin client_pin 2>/dev/null || true
