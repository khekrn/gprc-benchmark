#!/usr/bin/env bash
# Build the Spring Boot gRPC + Kotlin coroutines + R2DBC (reactive) server.
#
# Output: executable fat jar copied to bin/spring-rt-bench.jar, run via:
#   java $JVM_OPTS -jar bin/spring-rt-bench.jar
#
# Requires: JDK 21+ (the repo runs JDK 25; bytecode target is 21), Maven 3.9+.
set -euo pipefail
cd "$(dirname "$0")"
source ./config.sh

MOD_DIR="${ROOT_DIR}/spring-rt"

if [ -s "${HOME}/.sdkman/bin/sdkman-init.sh" ]; then
  set +u
  source "${HOME}/.sdkman/bin/sdkman-init.sh" >/dev/null 2>&1 || true
  set -u
fi

command -v java >/dev/null || { echo "java not found in PATH"; exit 1; }
JAVA_VER="$(java -version 2>&1 | head -1 | awk -F\" '{print $2}')"
if [ "${JAVA_VER%%.*}" -lt 21 ]; then echo "ERROR: JDK 21+ required (found ${JAVA_VER})." >&2; exit 1; fi
echo ">> java version: ${JAVA_VER}"

echo ">> Building spring-rt (mvn clean package)"
( cd "${MOD_DIR}" && mvn -B -q clean package -DskipTests )

JAR="${MOD_DIR}/target/spring-rt-bench-1.0.0.jar"
if [ -f "${JAR}" ]; then
  mkdir -p "${ROOT_DIR}/bin"
  cp "${JAR}" "${ROOT_DIR}/bin/spring-rt-bench.jar"
  echo ">> Done. App at ${ROOT_DIR}/bin/spring-rt-bench.jar"
else
  echo "Build did not produce ${JAR}" >&2
  exit 1
fi
