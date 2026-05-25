#!/usr/bin/env bash
# Start the Kotlin/Vert.x gRPC server, pinned to the configured CPUs.
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

JAR="${ROOT_DIR}/bin/kotlin-vertx-bench.jar"
[ -f "${JAR}" ] || { echo "Build first: ./scripts/build_kotlin.sh"; exit 1; }

export LISTEN_HOST="${KOTLIN_HOST}"
export LISTEN_PORT="${KOTLIN_PORT}"
echo ">> Starting Kotlin/Vert.x server on ${KOTLIN_ADDR} (CPUs ${PIN_SERVER_CPUS}, JVM_OPTS=${JVM_OPTS})"
# shellcheck disable=SC2086
exec server_pin java ${JVM_OPTS} -jar "${JAR}"
