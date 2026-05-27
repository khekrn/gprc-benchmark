# Chapter 14 — Performance Tuning Vert.x

> **You should finish this chapter able to:** size event loops, enable native
> transports (epoll / kqueue / io_uring), reduce allocations on the hot path,
> tune HTTP/2 settings, profile with async-profiler and JFR, set up
> Prometheus + Micrometer, and reason about the trade-offs each tuning knob
> implies.

A vanilla Vert.x app already saturates 100k+ RPS on a small machine. This
chapter is about the tuning that gets you to 1M+ RPS, predictable p99.9
latency, and graceful behavior under back-pressure. Each knob has a cost —
either complexity, memory, or measurability — so we'll talk about *when* a
change is worth making, not just *how*.

## 14.1  The performance toolkit

You can't tune what you can't measure. Standard kit:

| Tool | What it shows |
|---|---|
| `wrk` / `wrk2` | HTTP load with consistent RPS or open-loop |
| `ghz` | gRPC load with histograms |
| `vmstat 1` / `iostat 1` | OS-level CPU/IO usage |
| `jcmd <pid> JFR.start` | JDK Flight Recorder (built in, free) |
| async-profiler | CPU / alloc / lock profiles in flame-graph form |
| Prometheus + Grafana | Time-series metrics across hosts |
| Vert.x's blocked-thread checker | Find blocking on the event loop |

Establish a baseline *before* tuning. Document it. Every change rolls back
or sticks based on whether it moved the baseline in the right direction.

## 14.2  Event loop sizing

Default: `2 × Runtime.availableProcessors()` event loops. Our `Main.kt`:

```kotlin
VertxOptions()
    .setEventLoopPoolSize(2 * Runtime.getRuntime().availableProcessors())
    .setWorkerPoolSize(20)
    .setMaxEventLoopExecuteTime(100)
    .setMaxEventLoopExecuteTimeUnit(TimeUnit.MILLISECONDS)
    .setBlockedThreadCheckInterval(1_000)
    .setPreferNativeTransport(true)
```

When to deviate:

| Workload | Event loops |
|---|---|
| Pure I/O, mostly waiting | `1 × cores` to `2 × cores` |
| Mix of I/O and CPU work | `2 × cores` |
| CPU-heavy serialization (large JSON, encryption) | `1 × cores` (and offload CPU to workers) |
| Pinned-core deployment (`--cpus`) | match the pinned count |

Each event loop is a Netty `EpollEventLoop` (or NIO) thread plus a small
selector. Too many: context switching kills you. Too few: I/O backs up.
**2× cores is the right default in 95% of cases.**

`workerPoolSize` is the pool used by `executeBlocking` / `awaitBlocking`.
Default 20. If you don't block, leave it. If you wrap blocking SDKs, set it
based on how many concurrent blocking ops you tolerate.

## 14.3  `setMaxEventLoopExecuteTime` — your safety net

```kotlin
.setMaxEventLoopExecuteTime(100)
.setMaxEventLoopExecuteTimeUnit(TimeUnit.MILLISECONDS)
```

The blocked-thread checker logs a stack trace if any event-loop task takes
longer than the threshold. Default 2 s. **Lower it to 100 ms in dev** — it
will catch CPU hotspots you didn't know about. Keep it at 200-500 ms in
production (lower threshold = more false positives on a noisy machine).

What it shows:

```
[WARN] Thread vert.x-eventloop-thread-3 has been blocked for 350 ms
io.vertx.core.VertxException: Thread blocked
    at com.fasterxml.jackson.databind.ObjectMapper.writeValue(...)
    at com.example.app.handler.serialize(...)
```

This is the most useful diagnostic in your toolbox. If you ever see "thread
blocked" warnings in your logs, **fix them before tuning anything else**.

## 14.4  Native transports — epoll / kqueue / io_uring

`setPreferNativeTransport(true)` tells Vert.x to use platform-native I/O
multiplexing if the JNI library is on the classpath. Add:

```xml
<dependency>
  <groupId>io.netty</groupId>
  <artifactId>netty-transport-native-epoll</artifactId>
  <classifier>linux-x86_64</classifier>
</dependency>
<!-- and/or for ARM64: -->
<dependency>
  <groupId>io.netty</groupId>
  <artifactId>netty-transport-native-epoll</artifactId>
  <classifier>linux-aarch_64</classifier>
</dependency>
```

Why bother? NIO's `Selector` is implemented in Java over `epoll_wait` on
Linux, but does extra syscalls per event. Netty's native `EpollEventLoop`
calls `epoll_wait` directly via JNI, plus uses level-triggered mode and
edge-triggered tricks for small wins.

In practice, native transports are 10-20% faster for high-concurrency I/O
workloads and reduce syscall overhead. **Use them on Linux, always.**

For macOS / BSD, add `netty-transport-native-kqueue`. For Linux 5.1+ with
io_uring support, add `netty-transport-native-io_uring` — that's still
experimental but offers a further 10-15% on syscall-bound paths.

Verify in startup logs:

```
[INFO] Channel type: io.netty.channel.epoll.EpollServerSocketChannel
```

If it says `NioServerSocketChannel`, the native lib isn't loading. Check
the classifier matches your container architecture.

## 14.5  `SO_REUSEPORT` and accept scaling

```kotlin
HttpServerOptions().setReusePort(true)
```

This is the **single biggest "free" win** for high-concurrency HTTP servers.

Without `SO_REUSEPORT`, all your verticle instances share one OS-level
accept queue. The kernel picks one to wake — usually the same thread (the
"thundering herd" + cache-locality dance). With `SO_REUSEPORT`, each
verticle binds its own socket to the same port, and the kernel hashes
incoming SYNs across them. Now each event loop has its own accept queue,
its own cache line, its own backlog.

```kotlin
val deploymentOptions = DeploymentOptions()
    .setInstances(Runtime.getRuntime().availableProcessors())
vertx.deployVerticle(::AppVerticle, deploymentOptions).coAwait()
```

Combined: 8 instances × 1 event loop each × SO_REUSEPORT = 8 independent
accept paths.

## 14.6  HTTP and HTTP/2 tuning

```kotlin
HttpServerOptions()
    .setPort(8080)
    .setReusePort(true)
    .setTcpFastOpen(true)             // saves a round-trip for repeated clients
    .setTcpNoDelay(true)              // disable Nagle's algorithm
    .setTcpKeepAlive(true)
    .setCompressionSupported(true)
    .setCompressionContentSizeThreshold(1024)
    .setMaxInitialLineLength(4096)
    .setMaxHeaderSize(16 * 1024)
    .setIdleTimeout(60)
    .setIdleTimeoutUnit(TimeUnit.SECONDS)
    .setInitialSettings(Http2Settings()
        .setMaxConcurrentStreams(256)
        .setInitialWindowSize(64 * 1024)
        .setHeaderTableSize(4096)
        .setMaxFrameSize(16 * 1024))
```

| Setting | When to change |
|---|---|
| `setTcpFastOpen` | Saves 1 RTT for repeat clients. Free, leave on. |
| `setTcpNoDelay` | Don't buffer small writes. Always on for low-latency. |
| `setCompressionSupported` | gzip for >1 KB JSON. CPU cost; measurable savings on egress. |
| `Http2Settings.maxConcurrentStreams` | Allow more parallel streams per connection. |
| `Http2Settings.initialWindowSize` | Larger window = less ACK waiting for big payloads. |

For pure-RPC (gRPC) servers, the HTTP/2 settings matter more than HTTP/1.1
ones. Tune `maxConcurrentStreams` based on real client behavior.

## 14.7  Connection pooling cheat sheet

| Pool | Max size guide | Pipelining |
|---|---|---|
| Postgres (`vertx-pg-client`) | 4-16 per JVM (shared) | `pipeliningLimit=256` for reads |
| Redis (`vertx-redis-client`) | 4-16 | implicit |
| HTTP client (`WebClient`) | 16-32 per host | enabled via `keepAlive=true` |
| gRPC client | 1-8 channels per upstream | multiplexed by HTTP/2 |

Postgres is typically the bottleneck. If the connection pool is 100%
utilized, your DB is the limit, not Vert.x. The fix is either pipelining,
query optimization, or moving load to Redis cache.

## 14.8  Allocation hygiene

Allocations on the event loop create work for the GC, which can pause your
event loop. Patterns to internalize:

### Use `Buffer` correctly

```kotlin
// BAD: allocates String + intermediate ByteArray
ctx.response().end("Hello $name")

// BETTER: skip intermediate string when reusable
ctx.response().end(Buffer.buffer().appendString("Hello ").appendString(name))

// BEST for static prefixes: pre-allocate once
private val HELLO_PREFIX = Buffer.buffer("Hello ")
ctx.response().end(Buffer.buffer(HELLO_PREFIX).appendString(name))
```

For *truly* hot paths (>50k RPS), reach for Netty `ByteBuf`s directly. For
most apps, `Buffer` is fine.

### Avoid auto-boxing on tight loops

```kotlin
// BAD: boxes Int → Integer
val counter = AtomicReference<Int>(0)

// GOOD: use the typed Atomic
val counter = AtomicLong(0)
```

JIT usually inlines this away, but on hot loops it doesn't.

### JSON: pick a fast path

```kotlin
// Vert.x JsonObject is convenient but does ~4 allocs per key
ctx.json(JsonObject().put("id", id).put("name", name))

// Direct String build is faster for tiny payloads
ctx.response().end("""{"id":"$id","name":"$name"}""")

// For complex objects, Jackson with afterburner module is fastest
val mapper = JsonMapper.builder().addModule(AfterburnerModule()).build()
ctx.response().end(mapper.writeValueAsString(user))
```

### Reuse `LoggingEventBuilder` paths

```kotlin
// Avoid varargs allocation in hot loop:
log.info("user {} created", id)         // varargs Object[] alloc

// Better via fluent API:
log.atInfo().setMessage("user created").addKeyValue("id", id).log()
```

The difference is small (~5 ns) but adds up at 100k RPS.

## 14.9  Profiling: JFR and async-profiler

### JDK Flight Recorder

JFR is built into the JDK. Capture a 60s profile:

```bash
jcmd <pid> JFR.start name=app filename=/tmp/profile.jfr duration=60s
```

Open with JDK Mission Control (jmc.org). Look at:

- **Method Profiling.** Where's CPU going?
- **GC Pauses.** Any pause > 50 ms?
- **Allocation by class.** Who's making garbage?
- **Socket I/O.** Network bottlenecks?
- **Lock contention.** Blocking?

JFR has very low overhead (~1%) and runs continuously in production. Wire
it up to a continuous-recording config and have ops collect a rolling
window during incidents.

### async-profiler

For CPU flame-graphs:

```bash
java -agentpath:/opt/async-profiler/libasyncProfiler.so=start,event=cpu,duration=60,file=/tmp/flame.html ...
```

Or attach to a running process:

```bash
asprof -d 60 -e cpu -f /tmp/flame.html <pid>
```

Open `flame.html` in a browser; the wide bars are your hot paths. Common
findings:

- **JSON serialization** dominates: cache responses, switch encoder.
- **TLS handshake** dominates: enable session resumption, use HTTP/2 connection pooling.
- **GC threads** prominent: switch to ZGC generational, reduce alloc rate.
- **`Unsafe.park`** dominant: too much synchronization. Re-think pool config.

### Allocation profiling

```bash
asprof -d 60 -e alloc -f /tmp/alloc.html <pid>
```

Find the top allocators and consider object pooling or `Buffer` reuse.

## 14.10  Micrometer + Prometheus

Already wired in `Main.kt`:

```kotlin
val metricsOptions = MicrometerMetricsOptions()
    .setPrometheusOptions(VertxPrometheusOptions().setEnabled(true))
    .setJvmMetricsEnabled(true)
    .setEnabled(true)

VertxOptions().setMetricsOptions(metricsOptions)
```

Vert.x emits dozens of metrics out of the box:

| Metric | What it tells you |
|---|---|
| `vertx_eventbus_messages_total` | Event bus throughput |
| `vertx_http_server_active_connections` | Live connections per port |
| `vertx_http_server_response_time_seconds` | Response latency histogram |
| `vertx_http_server_bytes_written_total` | Egress bandwidth |
| `vertx_pool_in_use{pool_name=pg-pool}` | PG connections busy |
| `vertx_pool_queue_pending{pool_name=pg-pool}` | Requests waiting for a connection |

Add custom metrics in your code:

```kotlin
val registry = BackendRegistries.getDefaultNow() as PrometheusMeterRegistry
val timer = Timer.builder("user_get_duration_seconds")
    .description("Time to fetch a user")
    .publishPercentiles(0.5, 0.95, 0.99)
    .register(registry)

suspend fun get(id: String): User? = timer.recordCallable {
    runBlocking { repo.findById(id) }
}!!
```

Hook up `Counter`, `Gauge`, `DistributionSummary` similarly. Always use
**histogram** (percentiles) instead of just averages — averages hide tails.

## 14.11  Scrape and alerting

`router.get("/metrics").handler(PrometheusScrapingHandler.create())` exposes
the standard Prometheus exposition format. Configure Prometheus:

```yaml
scrape_configs:
  - job_name: full-app
    scrape_interval: 15s
    static_configs:
      - targets: ['app:8080']
```

A starter set of alerts:

| Alert | Expression |
|---|---|
| HighErrorRate | `rate(http_server_responses_total{status=~"5.."}[5m]) / rate(http_server_responses_total[5m]) > 0.01` |
| EventLoopBlocked | `vertx_event_loop_blocked_count > 0` |
| HighPGQueueDepth | `vertx_pool_queue_pending{pool_name="pg-pool"} > 50` |
| HighGCPause | `rate(jvm_gc_pause_seconds_sum[1m]) / rate(jvm_gc_pause_seconds_count[1m]) > 0.05` |
| ContainerNearOOM | `(jvm_memory_used_bytes / jvm_memory_max_bytes) > 0.9` |

## 14.12  Open-loop vs closed-loop load testing

Most load tools (`ab`, `wrk` default) are **closed-loop**: they wait for each
response before sending the next. When the server slows down, they slow
down — masking real-world behavior where clients keep coming.

`wrk2` and `ghz` are **open-loop** (constant RPS regardless of latency).
Use them for production-realistic stress:

```bash
wrk2 -t 8 -c 256 -R 10000 -d 60s --latency http://localhost:8080/api/v1/users/u-42
```

Watch for the *coordinated omission* fix: high latency spikes in p99.9
that closed-loop testers would not have shown.

## 14.13  Garbage collection tuning

For most apps, the only GC knob you need is:

```
-XX:+UseZGC -XX:+ZGenerational
```

If you have weird allocation patterns or large heaps (>32 GB):

- **`-XX:ZAllocationSpikeTolerance=N`** raises the alloc-spike absorption.
- **`-XX:SoftMaxHeapSize=N`** suggests a soft cap below `-Xmx`.

Don't pre-emptively tune GC. Measure first, then tune the bottleneck.

## 14.14  Real-world wins (one-line each)

- **`setReusePort(true)`**: +20-50% throughput on multi-core boxes.
- **Native transports**: +10-20%, free.
- **`pipeliningLimit=256` on PG**: +5-10× concurrent query throughput.
- **`setMaxEventLoopExecuteTime(100)` + fix everything it complains about**:
  +500% in tail latency reduction.
- **ZGC generational**: -50-80% GC pause times vs G1.
- **Async-appender logging**: removes a 5-10 ms latency tail at high log volume.
- **Hand-built JSON serializer for one hot endpoint**: -50% CPU on that endpoint.

## 14.15  Common anti-patterns

**Tuning before profiling.** Every benchmark you don't have is fiction.

**`setBlockedThreadCheckInterval(10_000)` to silence warnings.** The warnings
are right; the code is wrong.

**Caching JSON-serialized responses globally.** Works until a content-aware
header (`Accept-Language`, `Authorization`-scoped data) breaks the cache.
Cache per-request *fragments*, not whole responses.

**Object pooling everywhere.** ZGC is fast; allocations are cheap.
Pool only objects you allocated > 1M/sec.

**Bumping pool sizes "to handle more load."** A bigger pool can't process
faster than the upstream. Find the actual bottleneck (DB CPU, network).

## 14.16  Try it

1. Run `wrk2 -t 8 -c 256 -R 20000 -d 60s --latency http://localhost:8080/api/v1/users/u-42`
   on the default config. Capture p50/p95/p99.9.
2. Enable `netty-transport-native-epoll` on Linux. Re-run the same load.
   Compare numbers and verify Epoll channel in logs.
3. Use async-profiler to capture an alloc profile during the load. Identify
   the top allocator and reduce its rate (e.g., by Buffer reuse). Re-measure.

[← Ch 13](13-docker-jvm.md) · [Next: Chapter 15 — Netty deep dive →](15-netty.md)
