# grpc-pg-bench — Claude context

Single-purpose repo: benchmark **Go (grpc-go + jackc/pgx)** vs **Kotlin
(Vert.x 5 + vertx-pg-client + coroutines)** on the gRPC-unmarshal → tiny CPU
touch → single Postgres INSERT path. Target hardware: **2 cores / 4 GB RAM,
Ubuntu Linux**. The benchmark exists to decide a stack for a next-gen
workflow engine.

The full architecture, fairness decisions, and result interpretation live in
`README.md`. This file is for things that aren't obvious from the code and
that future-you will need to remember when running on Linux.

## Layout (one-line each)

```
proto/command.proto            # shared contract
sql/schema.sql                 # one table, one index, one INSERT path
go-pgx/main.go                 # Go server (graceful stop, keepalive, health)
kotlin-vertx/                  # Maven project — Java 25, Kotlin 2.2.x
  src/main/kotlin/com/beam/bench/
    Main.kt                    # bootstrap + shutdown hook
    MainVerticle.kt            # builds shared Pool, deploys N GrpcVerticles
    GrpcVerticle.kt            # one instance per event loop
    CommandServiceImpl.kt      # vertxFuture(scope) { ... } bridge
    Db.kt                      # pool + insertCommand suspend fn + warmup(min)
    Config.kt, Fnv.kt
  src/main/resources/logback.xml   # AsyncAppender, INFO root
loadgen/main.go                # closed-loop gRPC driver (used by both stacks)
scripts/
  config.sh                    # all env knobs live here
  setup_db.sh                  # creates role `bench` + db `bench` + schema
  build_go.sh                  # protoc + go build (writes bin/go-server, bin/loadgen)
  build_kotlin.sh              # mvn package + copies jar to bin/kotlin-vertx-bench.jar
  run_*_server.sh              # standalone server runners
  run_benchmark.sh             # orchestrator
results/<ts>/                  # per-run JSON + summary.csv + environment.txt
```

## Prerequisites (Ubuntu)

```bash
# Java 25 — Corretto or Temurin, both work
# Example (Corretto via Amazon's repo, or sdkman):
sdk install java 25.0.1-amzn
sdk use java 25.0.1-amzn

# Go 1.23+
sudo apt install -y golang-go || (download from go.dev/dl)
# protoc (the Go build script uses it; the Kotlin build downloads its own):
sudo apt install -y protobuf-compiler

# Postgres 14+ — local install is best for this test
sudo apt install -y postgresql postgresql-client

# Maven 3.9+
sudo apt install -y maven

# taskset (in util-linux, already on every Ubuntu install) — UNLIKE macOS, this
# actually pins to cores on Linux. The orchestrator detects it automatically.
which taskset
```

## Hard version requirements (why)

- **JDK 25**. Kotlin 2.2.x's bundled IntelliJ libs are the first that can parse
  the Java 25 version string; older Kotlins die with `IllegalArgumentException: 25.0.1`.
  `scripts/build_kotlin.sh` hard-fails if `java -version` is < 25.
- **Kotlin 2.2.21** (pinned in `kotlin-vertx/pom.xml`). 2.1.x can't read JDK 25.
- **Vert.x 5.0.5**. In this version `vertxFuture` takes `scope` as a *regular*
  parameter (not an extension receiver on `CoroutineScope`). The call form is
  `vertxFuture(scope) { … }`, not `scope.vertxFuture { … }`. If you upgrade
  Vert.x, verify the metadata of `VertxCoroutineKt.vertxFuture` before changing
  call sites — it was an extension in 4.x.
- **scram-client comes in transitively** from vertx-pg-client. Don't pin it
  explicitly (`com.ongres.scram:scram-client:2.1` does not exist on Maven
  Central — the artifact was renamed at version 3; vertx-pg-client 5.0.5 pulls
  3.2 transitively).

## Build + run (Ubuntu, end-to-end)

```bash
./scripts/setup_db.sh           # one-time DB + schema
./scripts/build_go.sh           # produces bin/go-server + bin/loadgen
./scripts/build_kotlin.sh       # produces bin/kotlin-vertx-bench.jar
./scripts/run_benchmark.sh      # full sweep over CONCURRENCY_LEVELS
```

Override credentials / DB without editing files:

```bash
export PG_USER=postgres PG_PASSWORD=sam PG_DB=proddb
export DATABASE_URL="postgres://${PG_USER}:${PG_PASSWORD}@${PG_HOST:-127.0.0.1}:${PG_PORT:-5432}/${PG_DB}?sslmode=disable"
./scripts/run_benchmark.sh
```

For a publishable run (per README's own advice):

```bash
WARMUP=15s DURATION=60s ./scripts/run_benchmark.sh
```

## Linux-vs-macOS deltas worth remembering

1. **`taskset` is real on Linux.** Both servers and the loadgen *will* be pinned
   to `PIN_SERVER_CPUS` / `PIN_CLIENT_CPUS` (default `0,1` each). On the macOS
   smoke-test runs we did, pinning was a no-op — Linux numbers can differ
   substantially because the runtime constraint is finally enforced.
2. **No Netty DNS resolver warning.** The `i.n.r.d.DnsServerAddressStreamProviders`
   line you saw on macOS won't appear on Linux. If you *do* see it on Linux,
   something is wrong with the JDK / Netty native libs.
3. **GC and `+AlwaysPreTouch`.** JVM_OPTS uses ZGC + `AlwaysPreTouch`. On Linux
   the pre-touch is meaningful (forces page allocation up front); on macOS it
   was mostly harmless overhead.
4. **Postgres CPU watching matters more.** With taskset actually pinning the
   *servers* to cores 0–1, Postgres has cores 2+ to itself, so the bottleneck
   may shift from "DB is the wall at c=32" (what we saw on Mac) to runtime
   differences becoming visible. Run `top` or `htop` in another terminal and
   watch what's actually saturating.

## Architectural decisions that aren't obvious from the code

- **Kotlin: one shared Pool, N CoroutineVerticles.** The Pool is built once on
  the root `Vertx` in `MainVerticle.start()` and shared across `eventLoops`
  instances of `GrpcVerticle`. vertx-pg-client multiplexes connections across
  event loops internally; per-verticle pools would burn connections for no win.
- **Kotlin: `vertxFuture(scope) { … }` bridge keeps work on the event loop.**
  The verticle's `CoroutineScope` has the verticle's event-loop dispatcher, so
  the suspend block (including `coAwait` on prepared queries) runs on the same
  thread the request arrived on. Never switch to `Dispatchers.IO`/`Default` for
  the DB call — it would hop off the event loop and force a re-dispatch back.
- **Kotlin: pool warmup mirrors pgx's MinConns.** `Db.warmup(min)` acquires and
  releases `min` connections at startup so the Vert.x pool starts at the same
  size as the pgx pool. Without it, the first ~5s of traffic grows the pool
  lazily, which would skew the warmup phase against Kotlin.
- **Go: `run() error` instead of `log.Fatal` everywhere.** Lets `defer
  pool.Close()` actually fire on the fatal path. Graceful stop is bounded to
  15s before SIGKILL fallback so a wedged drain can't hang the orchestrator.
- **Go: gRPC health service registered.** Not used by the loadgen, but it
  reflects what you'd actually ship.

## Known gotchas (already fixed, in case they regress)

1. **Orchestrator orphan-server bug** (fixed in `scripts/config.sh` +
   `scripts/run_benchmark.sh`). The wrapper used to be a shell function called
   via `&`. That meant `$!` was the subshell PID, not the server's — `kill $!`
   killed the subshell and the actual server got reparented to init, kept
   holding the port. *Every subsequent `start_server` silently failed with
   `bind: address already in use` and the loadgen quietly hit the c=1
   server.* Symptoms: results that look fine but the *only* server log lines
   that remain are from the last failed start, and `ps -ef | grep go-server`
   shows orphans after the run. Fix uses a prefix array
   (`SERVER_PIN=(taskset -c …)` or `()`) instead of a function so `$!` is the
   real PID. **If you see orphan servers after a run, the array expansion
   probably regressed under `set -u`** — must use
   `${SERVER_PIN[@]+"${SERVER_PIN[@]}"}`, not `"${SERVER_PIN[@]}"`.

2. **Startup-failure deadlock in Kotlin** (fixed in `Main.kt`). Calling
   `exitProcess` from `.onFailure { … }` runs it on the event loop;
   `System.exit` then triggers the shutdown hook, which tries to
   `vertx.close().get()` — but the event loop is blocked inside `System.exit`.
   Hand exit off to a separate thread. Symptom: 9–10s of `BlockedThreadChecker`
   "Thread blocked" warnings when DB ping fails.

3. **Maven `compile-custom` goal**. The protobuf-maven-plugin's `compile-custom`
   goal requires a top-level `<pluginId>`, which the `<protocPlugins>` block
   doesn't provide. Use *only* `<goal>compile</goal>` — that goal reads
   `<protocPlugins>` and runs the Vert.x gRPC plugin alongside the built-in
   Java generator.

## Validation checklist for the Ubuntu run

After `./scripts/run_benchmark.sh` completes:

```bash
# 1. No orphans
ps -ef | grep -E '(go-server|kotlin-vertx-bench)' | grep -v grep && echo "BAD: orphans" || echo "OK"

# 2. Ports free
ss -ltn 'sport = :50051 or sport = :50052' | tail -n +2

# 3. Each server had one clean lifecycle per concurrency level
RUN="results/$(ls -t results/ | head -1)"
grep -c 'go-pgx server listening' "${RUN}/go-pgx.server.log"          # = N levels
grep -c 'graceful stop complete'  "${RUN}/go-pgx.server.log"          # = N levels
grep -c 'kotlin-vertx server up'  "${RUN}/kotlin-vertx.server.log"    # = N levels
grep -c 'vertx closed'            "${RUN}/kotlin-vertx.server.log"    # = N levels

# 4. ZERO blocked-event-loop warnings (otherwise vertxFuture / pool / Db is
#    doing something blocking on the event loop and the benchmark is invalid)
grep -c BlockedThreadChecker "${RUN}/kotlin-vertx.server.log"          # MUST be 0

# 5. Loadgen reported zero errors at every level
awk -F, 'NR>1 && $9!="0" {print "BAD: errors at",$1,"c="$2,$9}' "${RUN}/summary.csv"

# 6. Sanity: Postgres CPU. If it's pinned at 100% of a core, the bench is
#    measuring PG, not either runtime. Decide which question matters.
top -bn1 | grep -E '^[[:space:]]*[0-9]+.*postgres' | head -5
```

If #4 is non-zero on Linux, *stop and investigate before trusting numbers* —
that means we accidentally blocked an event loop and the whole "Vert.x reactive
advantage" comparison is invalid for that run.

## Tweaking the workload

All knobs are in `scripts/config.sh`. The ones you'll actually change:

| Var | Default | Notes |
|-----|---------|-------|
| `CONCURRENCY_LEVELS` | `1 8 32 64 128` | the sweep |
| `WARMUP` / `DURATION` | `5s` / `30s` | bump for publishable numbers |
| `PG_POOL_MIN/MAX` | `4` / `16` | match between stacks |
| `PIN_SERVER_CPUS` / `PIN_CLIENT_CPUS` | `0,1` / `0,1` | on Linux this matters |
| `JVM_OPTS` | `-Xms512m -Xmx1024m -XX:+UseZGC -XX:+AlwaysPreTouch` | Java 25 |

To stress *runtime/driver* and not Postgres: switch `commands` to `UNLOGGED`
in `sql/schema.sql`, drop the index, or move PG to a different box. Document
in the run notes whichever you did — it changes what the numbers mean.
