#!/usr/bin/env bash
# Start the Go gRPC server, pinned to the configured CPUs.
# Run in its own terminal; Ctrl-C to stop. Or use run_benchmark.sh which
# starts/stops it automatically.
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

BIN="${ROOT_DIR}/bin/go-server"
[ -x "${BIN}" ] || { echo "Build first: ./scripts/build_go.sh"; exit 1; }

export LISTEN_ADDR="${GO_ADDR}"
echo ">> Starting Go server on ${GO_ADDR} (CPUs ${PIN_SERVER_CPUS}, GOMAXPROCS=${GOMAXPROCS})"
exec server_pin "${BIN}"
