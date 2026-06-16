# --- Build stage --------------------------------------------------------
FROM maven:3.9-eclipse-temurin-25 AS build
WORKDIR /workspace

# Copy poms first so dependency download caches.
COPY pom.xml /workspace/pom.xml
COPY full-app/pom.xml /workspace/full-app/pom.xml
RUN mvn -B -pl full-app -am dependency:go-offline || true

COPY full-app/src /workspace/full-app/src
RUN mvn -B -pl full-app -am package -DskipTests

# --- Runtime stage ------------------------------------------------------
FROM eclipse-temurin:25-jre-noble
LABEL org.opencontainers.image.source="https://github.com/khekrn/gprc-benchmark"

RUN useradd --system --uid 1001 --create-home app
USER 1001
WORKDIR /app

COPY --from=build /workspace/full-app/target/full-app-1.0.0-SNAPSHOT.jar /app/app.jar

# Container-aware JVM defaults.  -XX:+UseZGC enables generational ZGC by default on 25.
ENV JAVA_OPTS="-XX:MaxRAMPercentage=75 -XX:+UseZGC -XX:+UnlockExperimentalVMOptions \
               -XX:+EnableDynamicAgentLoading \
               -Dfile.encoding=UTF-8 \
               -Dvertx.disableDnsResolver=true"

EXPOSE 8080 9090
ENTRYPOINT exec java $JAVA_OPTS -jar /app/app.jar
