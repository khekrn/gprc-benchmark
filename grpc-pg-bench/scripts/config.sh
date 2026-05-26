#!/usr/bin/env bash
# Shared configuration for the benchmark scripts.
# Override any value by exporting it before running a script.

# --- Postgres ---
export PG_HOST="${PG_HOST:-127.0.0.1}"
export PG_PORT="${PG_PORT:-5432}"
export PG_DB="${PG_DB:-bench}"
export PG_USER="${PG_USER:-postgres}"
export PG_PASSWORD="${PG_PASSWORD:-sam}"
export DATABASE_URL="${DATABASE_URL:-postgres://${PG_USER}:${PG_PASSWORD}@${PG_HOST}:${PG_PORT}/${PG_DB}?sslmode=disable}"

# --- Server listen addresses ---
export GO_ADDR="${GO_ADDR:-127.0.0.1:50051}"
export KOTLIN_HOST="${KOTLIN_HOST:-127.0.0.1}"
export KOTLIN_PORT="${KOTLIN_PORT:-50052}"
export KOTLIN_ADDR="${KOTLIN_HOST}:${KOTLIN_PORT}"
export RUST_ADDR="${RUST_ADDR:-127.0.0.1:50053}"
# Rust tokio worker threads — matches GOMAXPROCS / VERTX_EVENT_LOOPS for fairness.
export RUST_WORKER_THREADS="${RUST_WORKER_THREADS:-2}"

# --- Resource limits (server simulates a 2-core / 4 GB box) ---
# Server pinned to two cores and capped at 4 GB via systemd-run scope (see
# MEM_MAX below + run_benchmark.sh). The load generator is pinned to a
# *different* pair of cores so it never steals CPU from the server. This is
# the production-realistic measurement: in production the server sits on its
# 2-core box and clients call it from elsewhere on the network.
#
# On Apple Silicon (M1 Pro / Asahi) the kernel orders cores E-first:
#   cpu0-1  Icestorm (E, 2064 MHz, capacity 485)
#   cpu2-9  Firestorm (P, 3036 MHz, capacity 1024)
# So we pin the server to two P-cores (2,3) — that matches a typical 2-core
# ARM production box. On a uniform x86/Graviton system, cpu0-1 is fine.
export PIN_SERVER_CPUS="${PIN_SERVER_CPUS:-2,3}"
export PIN_CLIENT_CPUS="${PIN_CLIENT_CPUS:-4,5}"

# Memory cap enforced on the server process via `systemd-run --user --scope`
# (cgroup v2 MemoryMax). The cap covers RSS + page cache attributable to the
# server; if the JVM tries to grow past it, the kernel OOM-kills the scope.
# Set to empty string to disable.
export MEM_MAX="${MEM_MAX:-4G}"

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

# Workload shape. Passed to loadgen as -mode/-read-pct/-keyspace.
#   execute  — single autocommit INSERT (original benchmark, default)
#   exectx   — 3-statement TX (INSERT command + UPSERT state + INSERT outbox)
#   mixed    — per-iter coin flip between ExecuteTx and GetState
export LOADGEN_MODE="${LOADGEN_MODE:-execute}"
export LOADGEN_READ_PCT="${LOADGEN_READ_PCT:-20}"
export LOADGEN_KEYSPACE="${LOADGEN_KEYSPACE:-10000}"

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
