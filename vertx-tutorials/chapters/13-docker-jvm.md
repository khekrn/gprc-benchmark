# Chapter 13 — Docker & JVM Tuning

> **You should finish this chapter able to:** write a small, layered, multi-
> stage Dockerfile; understand JVM container-awareness; choose the right GC
> (ZGC generational on JDK 21+); use `jlink` for a custom runtime; harden
> the image; and reason about cold-start latency vs steady-state throughput.

A Vert.x service is a JVM application, and the JVM ships with a *lot* of
defaults that were tuned for bare-metal datacenters circa 2010. Containers
changed everything: memory limits via cgroups, ephemeral filesystems, no
permission to inspect /proc/cpuinfo. The good news is JDK 17+ knows about
cgroups out of the box. The better news is JDK 21 made the generational
ZGC the right default for most low-latency services.

## 13.1  The Dockerfile we ship

[`code/full-app/Dockerfile`](../code/full-app/Dockerfile):

```dockerfile
# syntax=docker/dockerfile:1.7
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

ENV JAVA_OPTS="-XX:+UseContainerSupport \
               -XX:MaxRAMPercentage=75 \
               -XX:+UseZGC -XX:+ZGenerational \
               -XX:+ExitOnOutOfMemoryError \
               -Dfile.encoding=UTF-8 \
               -Djava.net.preferIPv4Stack=true"

EXPOSE 8080 9090
ENTRYPOINT ["sh","-c","exec java $JAVA_OPTS -jar /app/app.jar"]
```

Let's go through each piece.

### `# syntax=docker/dockerfile:1.7`

Enables BuildKit features: `--mount=type=cache` (a persistent Maven cache),
heredoc syntax, parallel stages. BuildKit is the modern default in Docker
20.10+ but the syntax directive locks the version for reproducibility.

### `ARG JDK_VERSION=25`

Parameterized JDK version. Lets you build with JDK 21 (LTS) or JDK 25 (latest)
from the same Dockerfile: `docker build --build-arg JDK_VERSION=21 ...`. We
default to 25 to pick up generational ZGC default behavior, JEP-486
(security-related), and stable structured concurrency.

### `eclipse-temurin:25-jdk-noble`

`eclipse-temurin` is the Adoptium-built OpenJDK distribution — a clean,
liberally-licensed JDK with regular CPU updates. `noble` is Ubuntu 24.04
LTS. Two alternatives you'll see in the wild:

| Base | Pros | Cons |
|---|---|---|
| `eclipse-temurin:25-jdk-noble` | Familiar Ubuntu, glibc, easy to debug | ~ 350 MB image |
| `eclipse-temurin:25-jdk-alpine` | Smaller (~150 MB), musl libc | Some native libs misbehave with musl |
| `eclipse-temurin:25-jdk-ubi` | Red Hat UBI, no licensing for RHEL hosts | Lower-level package management |
| `distroless/java25-debian12` | Minimal (~80 MB), no shell at all | Hard to exec into for debugging |

For learning, stick with `noble`. For production, evaluate `distroless` if
your security posture demands no shell in the image.

### Two-stage build

The first stage compiles, the second only contains the jar:

```
builder stage   →   jdk + maven + source code  ~750 MB
                              ↓
                          shaded jar (60 MB)
                              ↓
runtime stage   →   jre + app.jar              ~250 MB
```

Discarding the build tools after compilation cuts ~500 MB. The `--mount=type=cache`
trick reuses Maven's local repo across builds, so subsequent builds are
~10 s instead of ~3 min.

### `useradd -r -u 1001 -g root app` and `USER 1001`

Running as root in a container is a CVE waiting to happen. We create a
system user with UID 1001 (matches K8s `runAsUser: 1001` patterns) and `USER`
it before the entrypoint. Containers running rootful is *almost always*
unnecessary.

### `JAVA_OPTS` — the flags worth knowing

| Flag | Effect |
|---|---|
| `-XX:+UseContainerSupport` | Default on 17+. JVM reads cgroup limits. |
| `-XX:MaxRAMPercentage=75` | Heap caps at 75% of cgroup memory limit. Leaves room for code cache, metaspace, native, stack. |
| `-XX:+UseZGC` | Z Garbage Collector — pause times in microseconds, even at 100GB heaps. |
| `-XX:+ZGenerational` | JEP 439: generational ZGC. Cuts CPU overhead by ~30% vs single-gen ZGC. |
| `-XX:+ExitOnOutOfMemoryError` | OOM → exit fast → restart by orchestrator. Better than half-dead JVM. |
| `-Dfile.encoding=UTF-8` | Avoid platform-default surprises (esp. on macOS / Windows). |
| `-Djava.net.preferIPv4Stack=true` | K8s networks are predominantly v4; skips dual-stack resolution. |

Skip these flags from older guides:
- `-Xmx=...` — let `MaxRAMPercentage` handle it.
- `-XX:+UseG1GC` — Z is better for our use case (low pause).
- `-XX:+PrintGCDetails` — verbose; just emit GC events via `-Xlog:gc*:stdout:time` if needed.
- `-XX:ParallelGCThreads=N` — JVM auto-detects from cgroup CPU limit.

## 13.2  Container-awareness in detail

Pre-JDK-10, the JVM read `/proc/cpuinfo` and saw all 96 host cores even when
the container had a 1-CPU limit. Result: 96 GC threads, OOMs, mayhem.

JDK 10+ added `+UseContainerSupport`, default on 17+. It reads:

- **`/sys/fs/cgroup/cpu.max`** (cgroup v2) or `cpu.cfs_quota_us` (cgroup v1)
  to determine the *effective* CPU count. `Runtime.availableProcessors()`
  reflects the cgroup limit, not the host.
- **`/sys/fs/cgroup/memory.max`** to determine memory budget. `MaxRAMPercentage`
  is applied to this value.

To verify in a running container:

```
java -XshowSettings:system -version 2>&1 | grep -A1 'CPUs\|memory'
```

Should show e.g. "CPUs total: 2, Memory Limit: 1.00G" if your container has
2 CPUs and 1 GiB memory.

### Memory budget math

For a 1 GiB container with `MaxRAMPercentage=75`:

```
Heap:        750 MB    (-XX:MaxRAMPercentage=75)
Metaspace:    ~80 MB
Code cache:   ~50 MB   (JIT-compiled code)
Direct mem:   ~30 MB   (Netty ByteBufs!)
Stacks:       N × 1 MB (one per JVM thread)
Native libs:  ~20 MB
─────────────
Total:       ~960 MB   tight but workable
```

If you use Netty native transports (chapter 14), bump direct-mem allowance
or the container OOM-killer fires.

For lower-bound RAM containers:
- 256 MiB: too small for any non-trivial JVM service. Use Quarkus native or
  GraalVM.
- 512 MiB: works but expect throughput hits.
- 1 GiB: comfortable minimum for Vert.x.
- 2-4 GiB: typical sweet spot.

## 13.3  Garbage collector choice

| GC | Pauses | Throughput | When |
|---|---|---|---|
| Serial | 100s of ms | Low | Tiny apps, single thread. Not for servers. |
| Parallel | ~100 ms | Highest | Batch jobs that don't care about latency. |
| G1 | ~10-50 ms | High | Default 11-20. Solid all-rounder. |
| ZGC | < 1 ms | High | Latency-sensitive. **Our choice on 21+.** |
| ZGC generational | < 1 ms | Higher than single-gen ZGC | **Best low-latency GC on 21+.** |
| Shenandoah | < 1 ms | Slightly less than ZGC | Red Hat's low-pause GC. Use if you're on OpenJ9 or want it. |

Generational ZGC (`-XX:+ZGenerational`) was production-ready in JDK 21,
default in JDK 25. It segregates short-lived objects into a "young" gen and
collects them more frequently — cheap because most allocations die young
(weak generational hypothesis).

**Don't** use `-XX:+UseG1GC` and `-XX:+UseZGC` together — last one wins,
spurious warnings.

To watch GC behavior live:

```
-Xlog:gc*=info:stdout:time
```

You'll see lines like:

```
[2026-05-27T10:30:14.012] GC(42) Pause Young (Allocation Rate) 12M->5M(64M) 0.421ms
```

For low-pause GCs, total pause time over a 10-min interval should be < 100ms.
If it isn't, find the culprit allocation hotspot with async-profiler
(chapter 14).

## 13.4  Layering and image size

Each `COPY` is a layer; if it changes, downstream layers rebuild. Order
from least-frequently-changed to most:

```dockerfile
COPY .mvn .mvn                       # never changes
COPY mvnw pom.xml ./                 # changes on dependency bumps
COPY ../pom.xml ../pom.xml
RUN --mount=type=cache=... -q -B dependency:go-offline    # tip: pre-fetch deps
COPY src src                         # changes every commit
RUN --mount=type=cache=... -q -B -DskipTests package
```

Pre-fetching dependencies (`dependency:go-offline`) means a code-only change
hits a cached dep layer. CI builds drop from 3 min to 30 s.

Tag with content hashes for reproducibility:

```
docker build -t myapp:$(git rev-parse --short HEAD) ...
docker build -t myapp:latest ...
```

Never push `latest` to production. Always use a content-hash tag so a
rollback is unambiguous.

## 13.5  `jlink` — custom JRE

A standard JRE is ~200 MB. If you don't need the entire JDK, build a custom
one with only the modules you use:

```dockerfile
FROM eclipse-temurin:25-jdk-noble AS jlink
RUN jlink \
    --module-path /opt/java/openjdk/jmods \
    --add-modules java.base,java.logging,java.management,java.naming,java.net.http,java.security.jgss,java.sql,jdk.unsupported,jdk.crypto.ec \
    --no-header-files --no-man-pages --strip-debug --compress=2 \
    --output /opt/jre

FROM ubuntu:24.04 AS runtime
COPY --from=jlink /opt/jre /opt/jre
ENV PATH=/opt/jre/bin:$PATH
COPY --from=builder /workspace/target/*.jar /app/app.jar
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
```

Result: ~70 MB JRE (vs 200 MB stock). Identify required modules with
`jdeps --print-module-deps target/app.jar`.

Caveat: third-party libs sometimes use modules they don't declare (looking at
you, Netty's `sun.misc.Unsafe`). Add `jdk.unsupported` and `jdk.crypto.ec` as
defensive includes.

## 13.6  Security hardening

```dockerfile
USER 1001                          # not root
COPY --chown=1001:0 ...            # files owned by app user

# Read-only root FS (set on K8s side, no Dockerfile change needed):
#   securityContext: { readOnlyRootFilesystem: true }
# Then mount a small /tmp tmpfs volume.

# Drop all capabilities:
#   securityContext.capabilities.drop: [ALL]
```

In `JAVA_OPTS`, harden TLS defaults:

```
-Dhttps.protocols=TLSv1.3,TLSv1.2
-Dsecurity.useSystemPropertiesFile=false
```

On JDK 17+, the default `cacerts` includes the standard CA set. If you have
a private CA, mount it at `/etc/pki/ca-trust/source/anchors/` and re-trust,
or use `-Djavax.net.ssl.trustStore=...`.

## 13.7  Cold start and warm-up

Vert.x cold-starts in ~200 ms. Most of that is JIT compilation, classloading,
and DNS resolution at startup. To accelerate:

1. **Class data sharing (CDS)**. The JDK can pre-load common classes from a
   shared archive:
   ```
   java -XX:ArchiveClassesAtExit=/tmp/app.jsa -jar app.jar     # train run
   java -XX:SharedArchiveFile=/tmp/app.jsa  -jar app.jar       # subsequent runs
   ```
   Cuts startup ~30-40%.

2. **Warm-up traffic before serving real users.** Kubernetes `startupProbe`
   + a warm-up endpoint that exercises hot paths a few hundred times. Lets
   the JIT promote critical methods to C2 before traffic floods in.

3. **AOT (`jaotc` / GraalVM native image)** for ultra-cold-start scenarios.
   Vert.x has [reactive native](https://quarkus.io/) support via Quarkus. For
   serverless-style deployments where every cold start matters, evaluate it.
   For long-running services, stick with JVM.

## 13.8  Diagnostics inside the container

The image deliberately has no debug tools. To debug:

```bash
kubectl exec -it pod -- /bin/sh           # if shell exists
kubectl debug pod -it --image=busybox     # ephemeral debug container
```

For runtime profiling, use **async-profiler** with `-XX:+EnableDynamicAgentLoading`:

```
java -XX:+EnableDynamicAgentLoading ...
jcmd <pid> JFR.start filename=/tmp/profile.jfr duration=60s
```

Or attach async-profiler:

```
java -agentpath:/opt/async-profiler/libasyncProfiler.so=start,event=cpu,file=/tmp/profile.html ...
```

JFR is built-in and free; async-profiler is more accurate for native frames.
Either is a chapter-14 read.

## 13.9  Health probes integration

For Kubernetes, `livenessProbe` checks "is the JVM alive?" and `readinessProbe`
checks "is the service ready to take traffic?". Our app exposes:

```
/healthz/live   → 200 if JVM is up    (used by liveness)
/healthz/ready  → 200 if DB+Redis OK  (used by readiness)
/metrics        → Prometheus scrape
```

```yaml
livenessProbe:
  httpGet:  { path: /healthz/live,  port: 8080 }
  initialDelaySeconds: 10
  periodSeconds: 10
  failureThreshold: 3

readinessProbe:
  httpGet:  { path: /healthz/ready, port: 8080 }
  initialDelaySeconds: 5
  periodSeconds: 5
  failureThreshold: 3
```

Critical detail: **readiness can flap; liveness cannot**. If your DB blips,
readiness fails (= no traffic) but liveness must stay green (= no restart).
Otherwise you're in a restart loop fighting a temporary outage.

## 13.10  Putting it together — `docker compose` for local dev

```yaml
services:
  postgres:
    image: postgres:17-alpine
    environment:
      POSTGRES_DB: app
      POSTGRES_USER: app
      POSTGRES_PASSWORD: app
    ports: ["5432:5432"]
    volumes:
      - ./code/full-app/src/main/resources/db/migration:/docker-entrypoint-initdb.d:ro

  redis:
    image: redis:8-alpine
    ports: ["6379:6379"]

  app:
    build: ./code/full-app
    depends_on: [postgres, redis]
    environment:
      APP_PG_HOST: postgres
      APP_REDIS_URI: redis://redis:6379
    ports: ["8080:8080", "9090:9090"]
```

`docker compose up --build` brings up the whole world locally in ~15 s.

## 13.11  Common pitfalls

**Setting `-Xmx` instead of `MaxRAMPercentage`.** Pin the JVM to one size,
then bumping the K8s memory limit does nothing. `MaxRAMPercentage` follows
the limit.

**Forgetting `+ExitOnOutOfMemoryError`.** A half-dead OOM'd JVM with broken
internal state continues to serve 500s. Better: die, get restarted.

**Mounting host paths into the container for code reload.** Defeats
reproducibility. Use proper builds for prod; only for dev.

**Running as root + read-only filesystem.** The combination prevents the
JVM from writing temporary class files. Always run as a *non-root* user and
mount `/tmp` as a `emptyDir` or tmpfs.

**Building images on the production server.** Build once in CI, push to a
registry, pull. Otherwise builds compete with serving traffic for resources.

**Not pinning the base image tag.** `eclipse-temurin:25` floats; a CPU update
two weeks later changes behavior. Pin to a content-addressable digest:
`eclipse-temurin:25-jdk-noble@sha256:abc...`.

**Setting absurdly high `MaxRAMPercentage` in a small container.** With
`MaxRAMPercentage=90` on a 512 MiB container, native memory has 50 MiB —
guaranteed Netty OOM under load.

## 13.12  Try it

1. Build the image with `--build-arg JDK_VERSION=21` and verify the app
   still runs. Compare startup and steady-state CPU/memory.
2. Add a `RUN jdeps --print-module-deps target/full-app-shaded.jar` in the
   builder stage. Use the output to build a `jlink` JRE. Measure image-size
   reduction.
3. Run the app with `-Xlog:gc*=info:stdout:time` and pump 1M requests with
   wrk. Look at pause durations and frequencies. Now switch to `-XX:+UseG1GC`
   and compare.

[← Ch 12](12-testing.md) · [Next: Chapter 14 — Performance tuning Vert.x →](14-performance.md)
