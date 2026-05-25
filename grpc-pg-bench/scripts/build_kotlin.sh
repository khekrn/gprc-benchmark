#!/usr/bin/env bash
# Build the Kotlin/Vert.x gRPC server fat jar via Maven.
# The protobuf-maven-plugin downloads protoc + the Vert.x gRPC plugin itself,
# so you do NOT need a system protoc for this build.
#
# Requires: JDK 25, Maven 3.9+.
# (Kotlin 2.2's tooling cannot parse the Java 25 version string when run on
# older JDKs that bundle a pre-25 IntelliJ — and we want to *run* on 25 anyway.)
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

KT_DIR="${ROOT_DIR}/kotlin-vertx"

command -v mvn >/dev/null || { echo "mvn not found in PATH"; exit 1; }
JAVA_VER="$(java -version 2>&1 | head -1 | awk -F\" '{print $2}')"
JAVA_MAJOR="${JAVA_VER%%.*}"
echo ">> java version: ${JAVA_VER}"
if [ "${JAVA_MAJOR:-0}" -lt 25 ]; then
  echo "ERROR: JDK 25+ required (found ${JAVA_VER}). Point JAVA_HOME at a 25 install." >&2
  exit 1
fi

echo ">> Building Kotlin/Vert.x server (mvn clean package)"
( cd "${KT_DIR}" && mvn -q -DskipTests clean package )

JAR="${KT_DIR}/target/kotlin-vertx-bench-1.0.0.jar"
if [ -f "${JAR}" ]; then
  mkdir -p "${ROOT_DIR}/bin"
  cp "${JAR}" "${ROOT_DIR}/bin/kotlin-vertx-bench.jar"
  echo ">> Done. Jar at ${ROOT_DIR}/bin/kotlin-vertx-bench.jar"
else
  echo "Build did not produce expected jar at ${JAR}" >&2
  exit 1
fi
