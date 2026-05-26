# syntax=docker/dockerfile:1.7
#
# Multi-stage build:
#   1) builder    — JDK 25 + Maven to produce a shaded jar
#   2) runtime    — JDK 25 (jre flavor) for a smaller runtime image
#
# Why JDK 25 at runtime: ZGC generational is default and excellent for low-latency
# servers, scoped values + structured concurrency are stable, and the JVM honors
# container CPU/memory limits out of the box with -XX:+UseContainerSupport (default).
ARG JDK_VERSION=25

FROM eclipse-temurin:${JDK_VERSION}-jdk-noble AS builder
WORKDIR /workspace
COPY .mvn .mvn
COPY mvnw pom.xml ./
COPY ../pom.xml ../pom.xml
COPY src src
RUN --mount=type=cache,target=/root/.m2 ./mvnw -q -B -DskipTests package

FROM eclipse-temurin:${JDK_VERSION}-jre-noble AS runtime
RUN useradd -r -u 1001 -g root app
WORKDIR /app
COPY --from=builder /workspace/target/*.jar app.jar
USER 1001

# Container-aware JVM flags.
#   -XX:+UseContainerSupport           — default on 17+, listed for clarity
#   -XX:MaxRAMPercentage=75            — use 75% of the cgroup memory limit
#   -XX:+UseZGC -XX:+ZGenerational     — low-pause GC, generational mode
#   -XX:+ExitOnOutOfMemoryError        — fast-fail so the orchestrator restarts us
#   -Dfile.encoding=UTF-8              — never rely on locale
#   -Djava.net.preferIPv4Stack=true    — typical for cloud k8s
ENV JAVA_OPTS="-XX:+UseContainerSupport \
               -XX:MaxRAMPercentage=75 \
               -XX:+UseZGC -XX:+ZGenerational \
               -XX:+ExitOnOutOfMemoryError \
               -Dfile.encoding=UTF-8 \
               -Djava.net.preferIPv4Stack=true"

EXPOSE 8080 9090
ENTRYPOINT ["sh","-c","exec java $JAVA_OPTS -jar /app/app.jar"]
