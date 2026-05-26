# Chapter 5 — Avoiding Blocking

> **You should finish this chapter able to:** explain exactly why blocking the
> event loop is catastrophic, tune the blocked-thread checker for your
> environment, choose between `executeBlocking`, `awaitBlocking`, a dedicated
> `WorkerExecutor`, and `ThreadingModel.VIRTUAL_THREAD`, recognize the common
> sources of accidental blocking, and use async-profiler / JFR /
> BlockedThreadChecker to find them in production.

Chapters 1-4 set up the model. This chapter is about keeping it intact under
real-world pressure. Every Vert.x outage I have seen in production was
ultimately some form of "an event loop got blocked." Make this chapter muscle
memory.

## 5.1  Recap: why one blocking call is so destructive

Chapter 1 said it once. Repeat it with feeling.

A single event-loop thread serves N connections concurrently by interleaving
their handlers. If one handler stalls for 50 ms in `Thread.sleep`, **every
connection assigned to that loop stalls for 50 ms**. With M event loops and a
balanced workload, you have just added 50 ms to roughly `1/M` of all in-flight
requests — and the next request that arrives during the stall sits in the
TCP accept backlog.

```
EVENT LOOP TIMELINE (single loop, 4 concurrent requests R0..R3)

healthy:        R0─R1─R0─R2─R3─R1─R2─R3─R0─R1─...
                  ▲  ▲  ▲                                  each tick = some
                  └──┴──┴── tiny handler ticks (µs)        progress on a request

blocked:        R0─R1─[XXXXXXXXXX 50 ms blocking call XXXXXXXXX]─R2─R3─...
                                  ▲
                                  └── R1 froze the loop. R2, R3, *every other*
                                      connection on this loop, plus newly-arrived
                                      ones, are stuck waiting on it.
```

The latency penalty is not "50 ms for the slow request." It is "50 ms
*added to whatever those other requests were doing*." This is called
**head-of-line blocking**, and it is why p99 latency in a Vert.x service is
the single most sensitive symptom of accidental blocking.

There is also a more subtle cost: TCP back-pressure. If your loop cannot read
from the socket, the kernel's receive window shrinks; the peer slows its
sends. The damage propagates upstream.

```
+----------------+      back-pressure      +----------------+
|  upstream peer | <---------------------- |  blocked loop  |
+----------------+   (TCP window shrinks)  +----------------+
```

Coroutines do not save you here. A `suspend fun` that internally calls
`Thread.sleep(100)` blocks the carrier event loop exactly as a plain function
does. **Suspension is not blocking; blocking is not suspension.**

## 5.2  The blocked-thread checker

Vert.x ships a built-in detector. Every event loop records, before each task,
the timestamp at which that task started. A separate scheduled task (the
"blocked thread checker") wakes periodically, walks the registered event loops
and worker threads, and complains if any of them has been running the same
task for longer than its configured threshold.

### 5.2.1  Defaults

| Option | Default | Meaning |
|---|---|---|
| `setMaxEventLoopExecuteTime(ns)` | 2_000_000_000 (2 s) | Max wall time *one task* may run on an event loop before being flagged. |
| `setMaxWorkerExecuteTime(ns)` | 60_000_000_000 (60 s) | Same for worker pool. |
| `setBlockedThreadCheckInterval(ms)` | 1000 ms | How often the checker scans. |
| `setWarningExceptionTime(ns)` | 5_000_000_000 (5 s) | Stack trace is logged once the task has been running this long. |
| `setMaxEventLoopExecuteTimeUnit(...)` | `NANOSECONDS` | Unit applied to `maxEventLoopExecuteTime`. |

The 2-second default is fine for batch jobs and lab demos. For an HTTP API
serving p99 < 50 ms, **2 seconds is two orders of magnitude too lenient** —
by the time the checker fires, your SLO is already breached and your
upstream load balancer is opening circuit breakers.

### 5.2.2  Our settings in `Main.kt`

See [`Main.kt`](../code/full-app/src/main/kotlin/com/example/app/Main.kt#L37-L48):

```kotlin
val vertxOptions = VertxOptions()
    .setEventLoopPoolSize(2 * Runtime.getRuntime().availableProcessors())
    .setWorkerPoolSize(20)
    .setBlockedThreadCheckInterval(1_000)         // scan every 1 s
    .setMaxEventLoopExecuteTime(100_000_000L)     // 100 ms in nanoseconds
    .setMetricsOptions(metricsOptions)
    .setPreferNativeTransport(true)
```

**Line by line:**

- `setBlockedThreadCheckInterval(1_000)` — the checker wakes once a second.
  This is intentionally not aggressive; the checker itself is a scheduled
  task on Vert.x's internal timer wheel and we do not want it to dominate.
- `setMaxEventLoopExecuteTime(100_000_000L)` — 100 ms in nanoseconds. The
  argument unit is *nanoseconds by default*. If a single event-loop task runs
  longer than 100 ms, log it.
- Why 100 ms? Empirically, anything above ~10 ms on the event loop will cost
  you in p99; we set the floor at 100 ms so that genuine handler work (deserialization,
  small response building) does not trigger the alarm, but a runaway regex or
  a thoughtless JDBC call does. **For batch / ETL workloads we bump this to
  several seconds.** For latency-critical gateways, 50 ms is reasonable.

### 5.2.3  What the checker prints

When the checker fires, you see this in your logs (logger:
`io.vertx.core.impl.BlockedThreadChecker`):

```
WARN  i.v.c.impl.BlockedThreadChecker - Thread Thread[vert.x-eventloop-thread-3,5,main]
has been blocked for 312 ms, time limit is 100 ms
io.vertx.core.VertxException: Thread blocked
    at java.base@21/java.lang.Thread.sleep0(Native Method)
    at java.base@21/java.lang.Thread.sleep(Thread.java:509)
    at com.example.app.UserService.lookup(UserService.kt:42)
    at com.example.app.http.UserHandler$handle$1.invokeSuspend(UserHandler.kt:18)
    at kotlin.coroutines.jvm.internal.BaseContinuationImpl.resumeWith(...)
    ...
```

Read it bottom-up like any stack trace, but with two extras:

1. **The thread name pins the loop**: `vert.x-eventloop-thread-3` — instance 3.
2. **The frame just above the JDK frames is your offender.** In the example
   above, `UserService.lookup:42` is the culprit, not the coroutine machinery
   below it.
3. The exception class `VertxException: Thread blocked` is *synthetic* — the
   checker constructs it to capture the offending thread's stack. The thread
   has not thrown; it is still running.

If the same warning repeats every interval with the same stack, the loop is
stuck in a tight loop or under a long blocking call (the warning prints once
per scan as long as the same task is still running). If it fires once and
goes away, you had a one-shot CPU spike (GC pause, big JSON serialization).

## 5.3  `executeBlocking` — short, occasional blocking work

When you have one piece of code that *legitimately* needs to block — typically
a CPU-bound chunk of work or a call into a synchronous library — wrap it:

```kotlin
val resultFuture: Future<String> = vertx.executeBlocking<String> { promise ->
    val hash = expensiveHash(payload)          // CPU bound, takes ~30 ms
    promise.complete(hash)
}
```

The Future-returning overload is more idiomatic:

```kotlin
val resultFuture: Future<String> = vertx.executeBlocking {
    expensiveHash(payload)                     // last expression is the result
}
```

**Signature** (modern Vert.x 5):

```kotlin
fun <T> Vertx.executeBlocking(
    blockingCodeHandler: Callable<T>,
    ordered: Boolean = true                    // see below
): Future<T>
```

### 5.3.1  Where the work runs

The block runs on a thread from the *worker pool* — `vert.x-worker-thread-N`.
The pool default size is **20** (`VertxOptions.setWorkerPoolSize(20)`). The
returned `Future` completes on the **calling Context** (your event loop), so
`coAwait()` on it gives you back the original event-loop thread for the next
line of code:

```kotlin
suspend fun computeHash(payload: ByteArray): String =
    vertx.executeBlocking<String> { expensiveHash(payload) }.coAwait()
//        ^ runs on worker pool                         ^ resumes on event loop
```

### 5.3.2  Ordered vs unordered

The boolean second argument controls per-Context ordering:

- `ordered = true` (default) — within one Context (event loop), blocking
  tasks are queued and executed *one at a time*. Useful if your blocking code
  has shared state per-loop.
- `ordered = false` — tasks are dispatched as soon as a worker thread is
  free, regardless of order on the calling Context. Use this when the
  blocking calls are independent (the common case).

```kotlin
vertx.executeBlocking({ expensiveHash(payload) }, false)
//                                                 ▲
//                                                 │ unordered: more parallelism
```

In practice: **default to `false` for I/O-style blocking** (one call per
request, no shared state). Keep the default `true` if you are interacting
with a non-thread-safe SDK.

## 5.4  `awaitBlocking` — the suspending counterpart

`vertx-lang-kotlin-coroutines` ships a one-liner wrapper:

```kotlin
import io.vertx.kotlin.coroutines.awaitBlocking

suspend fun loadConfig(): JsonObject = awaitBlocking {
    JsonObject(Files.readString(Path.of("/etc/app/config.json")))
}
```

Implementation-wise this is `executeBlocking { block() }.coAwait()`. The
difference is purely ergonomic, but it documents intent: the body is
*allowed* to block. Reviewers see `awaitBlocking` and know to treat the
block specifically.

We already met it in chapter 3.4 — restating the rule: **use `coAwait()` for
futures, `awaitBlocking` for code that actually blocks the thread itself.**
Confusing them is the most common mistake in coroutine-Vert.x code.

```
                 +-----------------------+--------------------------------+
                 |  Returns a Future?    |  Use coAwait()                 |
                 |  Blocks the thread?   |  Use awaitBlocking { ... }     |
                 |  Both?                |  It is a code smell — split it |
                 +-----------------------+--------------------------------+
```

## 5.5  The worker pool — sizing and dedicated pools

### 5.5.1  Why default 20?

The shared worker pool defaults to 20 threads. This number reflects two
assumptions:

1. Blocking calls are *occasional* — not every request takes the worker pool.
2. Each blocking call is *short* — milliseconds, not seconds.

If both assumptions hold, 20 is fine. If they do not — for example, you have
a heavy ImageMagick batch step that takes 800 ms each and runs on every
upload — your pool will saturate and `executeBlocking` will queue. Once the
queue grows, your effective concurrency for blocking work is `20`, not
`request rate × duration`.

### 5.5.2  Sizing rule of thumb

If your blocking workload has average concurrency `C` and average wall time
`T`, and you want queue depth to stay near zero:

```
   worker pool size  ≈  C × T / interval_between_arrivals
                     ≈  arrival_rate × T            (Little's law)
```

A worker pool of 200 threads is fine *if you have the CPU and memory headroom*
and the work is mostly I/O-bound. A worker pool of 200 doing CPU work on an
8-core machine will just thrash the scheduler.

### 5.5.3  Dedicated `WorkerExecutor`

Sharing one pool across "image transcoding," "PDF generation," and "calls
into legacy JDBC" means one slow workload starves the others. Split them:

```kotlin
val imagePool: WorkerExecutor = vertx.createSharedWorkerExecutor(
    "image-worker",                       // name → shows up in metrics
    8,                                    // pool size
    60_000_000_000L                       // max execute time: 60 s
)

suspend fun transcode(input: ByteArray): ByteArray =
    imagePool.executeBlocking<ByteArray> { transcodeImage(input) }.coAwait()
```

**Line by line:**

1. `createSharedWorkerExecutor(name, ...)` is idempotent per `name` — call it
   anywhere; multiple verticles asking for `"image-worker"` get the *same*
   pool. This is how you safely share a sized pool across verticle instances.
2. The size is independent of the global `setWorkerPoolSize`. Tune per
   workload.
3. The `maxExecuteTime` argument controls the blocked-thread checker
   threshold *for this pool only*. Image transcoding legitimately takes
   seconds — bumping it to 60 s avoids spurious warnings.
4. Use the executor's `executeBlocking` (not `vertx.executeBlocking`) so the
   work actually lands in this pool.

Close it on shutdown: `imagePool.close().coAwait()`.

## 5.6  Virtual threads (`ThreadingModel.VIRTUAL_THREAD`)

JDK 21 made virtual threads GA. Vert.x 5 lets a verticle run with its handlers
dispatched on virtual threads:

```kotlin
val opts = DeploymentOptions()
    .setThreadingModel(ThreadingModel.VIRTUAL_THREAD)
    .setInstances(1)

vertx.deployVerticle(::JdbcVerticle, opts)
```

### 5.6.1  How a virtual thread "works"

A virtual thread is a JVM-managed coroutine-like construct. The JVM owns a
small pool of **carrier threads** (default: roughly `availableProcessors()`).
When a virtual thread executes a blocking JDK call (`socket.read`, `JDBC`,
`Thread.sleep`, `LockSupport.park` …), the JVM detaches it from its carrier,
parks the virtual thread on the heap as a continuation, and returns the
carrier to run other virtual threads.

```
   carrier thread (real OS thread)
        │
        │   runs       runs       runs
        │  VT-1  ───► VT-2  ───► VT-3 ───► VT-1 resumes ...
        │   ▲          ▲          ▲          ▲
        │   │          │          │          └── JDBC response arrived
        │   └ blocks   └ blocks   └ blocks
        │   on JDBC    on read    on sleep
```

The number of *virtual* threads can be in the millions; the number of carriers
is small. This makes "thread per request" architecturally cheap again — but
only inside the JDK's instrumented blocking primitives.

### 5.6.2  When to use it instead of `executeBlocking`

`executeBlocking` puts work on a *bounded* worker pool. If you have a
legitimately concurrent blocking workload (1000 simultaneous JDBC queries),
a worker pool of 20 will queue. A virtual-thread verticle will scale the
number of in-flight JDBC calls to whatever the database connection pool can
service — without needing 1000 OS threads.

So:

| Situation | Pick |
|---|---|
| Occasional blocking call (config load, infrequent crypto) | `executeBlocking` / `awaitBlocking` |
| High-concurrency blocking workload, JDK-aware primitives only | `VIRTUAL_THREAD` verticle |
| Mostly non-blocking with a few blocking spots | Standard event-loop verticle + `awaitBlocking` for the spots |
| Native code, JNI, `synchronized` over long sections | Worker verticle (genuine OS thread) |

### 5.6.3  The pinning gotcha

A virtual thread is **pinned** to its carrier (= cannot be detached, blocks
the carrier just like a normal thread) when:

- It is inside a `synchronized` block.
- It is executing native code (JNI).
- It is inside a `Thread.holdsLock` region the JVM cannot unmount.

For example, `synchronized (this) { dbConnection.query(...) }` — if the JDBC
driver internally blocks on a socket while inside that `synchronized` block,
your carrier blocks. With only `availableProcessors()` carriers, you can
deadlock or destroy throughput.

**Mitigations:**

- Prefer `ReentrantLock` over `synchronized` in code paths that may run on
  virtual threads. `ReentrantLock.lock()` is unmountable.
- Run with `-Djdk.tracePinnedThreads=full` during development — the JVM will
  print stack traces for pinning events. Cleanup is iterative.
- Audit your driver: many older JDBC drivers (Oracle pre-23ai, some MySQL
  legacy paths) still use `synchronized` internally. Test under load.

### 5.6.4  `executeBlocking` vs virtual threads — side by side

```
                executeBlocking          virtual threads
                ─────────────────        ─────────────────
Concurrency cap pool size (e.g. 20)      ≈ ∞ (heap only)
Per-task cost   OS thread context        small (KB) continuation
Blocking JDK    OK                       OK (unless pinned)
Native/JNI      OK                       Pins carrier — bad
synchronized    OK                       Pins carrier — bad
Best for        occasional, short        many concurrent, JDK-aware
GC pressure     low                      higher (continuations on heap)
```

Treat them as complementary, not as one replacing the other.

## 5.7  Common sources of accidental blocking

The most expensive bugs are the boring ones. Here are the offenders you will
actually meet, ordered roughly by frequency.

1. **JDBC.** `java.sql.*` is a synchronous API. Every `executeQuery` blocks
   the calling thread until the network round-trip completes. Use
   `vertx-pg-client` / `vertx-mysql-client` / `vertx-mssql-client` instead
   (chapter 8). If you must use JDBC: `awaitBlocking` for occasional calls,
   virtual-thread verticle for high concurrency.
2. **`Thread.sleep(n)`.** Always wrong on an event loop. Use
   `vertx.setTimer(n) { ... }` from callback code, or `delay(n)` (kotlinx
   coroutines, suspending) inside a coroutine.
3. **`java.io.File.read*` / `Files.readAllBytes`.** Synchronous disk I/O.
   Vert.x's `FileSystem` API has async variants — `vertx.fileSystem()
   .readFile(path).coAwait()`.
4. **OkHttp / Apache HttpClient / RestTemplate.** Synchronous HTTP clients.
   Replace with `vertx-web-client` or `WebClient.create(vertx)`.
5. **`InetAddress.getByName("foo.example.com")`** and similar — synchronous
   DNS resolution. Vert.x has an async resolver built in
   (`vertx.resolveAddress(...)`); most Vert.x clients use it automatically.
   Avoid hand-rolled DNS.
6. **JSON serialization of very large objects.** Jackson is fast but not
   magic. Serializing a 50 MB response on the event loop will pin it for tens
   of milliseconds. Either stream the response (chapter 7) or do the
   serialization in `executeBlocking`.
7. **Regex with catastrophic backtracking.** `(a+)+b` against
   `aaaaaaaaaaaaaaaaX` runs for seconds on the event loop. Use
   `Pattern.compile(..., Pattern.LITERAL)` for fixed strings, validate inputs,
   and prefer `Matcher.find()` with timeouts via a watcher thread, or use the
   `re2j` library (linear-time regex).
8. **Logger appenders without async.** `ConsoleAppender` in Logback is
   synchronous and locks under contention. Wrap critical appenders in
   `AsyncAppender` (chapter 6 covers this in detail).
9. **`synchronized` / `ReentrantLock` under contention.** Holding a lock that
   is contested by many threads turns the event loop into a queue. Avoid
   shared mutable state across event loops; use the event bus, or
   `LongAdder` / atomic types for counters.
10. **GC pauses.** Not strictly "your code blocking," but a 200 ms G1 pause
    on a hot heap will trigger the blocked-thread checker. Switch to ZGC on
    JDK 21+ (chapter 13).

The pattern is the same in every case: **anything that the OS or runtime
would normally satisfy with a syscall must be either async or on a worker.**

## 5.8  Detection tooling

You cannot fix what you cannot see.

### 5.8.1  BlockedThreadChecker (already covered)

The cheapest detector. Tune it down (we use 100 ms), watch the logs in
staging. Any warning is a bug.

### 5.8.2  async-profiler — wall-clock mode

[`async-profiler`](https://github.com/async-profiler/async-profiler) is the
single most useful JVM profiler. For finding blocking, run in **wall-clock**
mode (samples *every* thread, including sleeping/blocked):

```bash
# Find the PID
jps -l | grep com.example.app

# 30-second wall-clock profile, flamegraph output
asprof -e wall -d 30 -f /tmp/wall.html <pid>

# Open the flamegraph; tall stacks rooted at
# vert.x-eventloop-thread-* that include socketRead0, JDBC, sleep, etc.
# are exactly your culprits.
open /tmp/wall.html
```

Why wall-clock instead of CPU? CPU mode misses everything that is *blocked*
because the thread is not on-CPU. Wall mode samples regardless of state, so a
thread sleeping inside `Thread.sleep(50)` shows up.

In async-profiler 3.0+, `-e wall` also captures *off-CPU* time with kernel
stacks if you pass `--alloc` / `--lock`. For blocking detection:

```bash
asprof -e wall --threads -d 60 -f profile.html <pid>
```

`--threads` keeps per-thread stacks separate; you can filter the flamegraph
to a single `vert.x-eventloop-thread-N` and inspect what *that* loop was doing.

### 5.8.3  Java Flight Recorder

JFR ships with the JDK. Lower overhead, less precise than async-profiler but
zero install:

```bash
# Start a 60s recording on a running JVM
jcmd <pid> JFR.start name=block duration=60s filename=/tmp/block.jfr settings=profile

# Stop manually if you used no duration
jcmd <pid> JFR.stop name=block filename=/tmp/block.jfr

# Open in JDK Mission Control (jmc) or analyze CLI
jfr summary /tmp/block.jfr
```

Look at the **Java Application → Thread Park / Socket Read / File Read /
Monitor Blocked** events. Filter by thread name `vert.x-eventloop-*`. Any
non-trivial duration on those threads from a non-selector source is a bug.

For continuous profiling in production, enable JFR at startup:

```
-XX:StartFlightRecording=name=app,settings=profile,disk=true,maxsize=1g,maxage=2h
-XX:FlightRecorderOptions=stackdepth=128
```

### 5.8.4  Micrometer event-loop metrics

Vert.x exposes per-event-loop scheduling delay via Micrometer
(`vertx-micrometer-metrics`, which `Main.kt` enables). The metric
`vertx.eventbus.handlers` and `vertx.pool.queue.delay` are useful, but the
single most actionable one is:

```
vertx_pool_usage_seconds_max{pool_type="worker"}
vertx_pool_queue_size{pool_type="worker"}
```

If `queue_size` for the worker pool is consistently > 0, you are saturating
it — either bump the pool size or move work to a virtual-thread verticle.
Scrape Prometheus from `/metrics` (we expose it in `HttpServerFactory.kt`).

For the event loop specifically, watch:

```
vertx_pool_usage_seconds_max{pool_type="event-loop"}
```

Any value above your `maxEventLoopExecuteTime` is a confirmed block.

## 5.9  A short troubleshooting checklist

When p99 latency spikes or BlockedThreadChecker fires, work through this
list in order:

```
┌──────────────────────────────────────────────────────────────────────┐
│ 1.  Open the log around the event. Find the BlockedThreadChecker     │
│     warning. Identify thread and frame.                              │
│                                                                      │
│ 2.  Is the frame in your code? → fix it.                             │
│     Is the frame in a library? → check 5.7 for the typical fix.      │
│                                                                      │
│ 3.  Can't reproduce? Take a wall-clock async-profiler sample under   │
│     load:                                                            │
│         asprof -e wall --threads -d 60 -f wall.html <pid>            │
│                                                                      │
│ 4.  Filter the flamegraph to vert.x-eventloop-thread-*. Anything     │
│     above the selector loop frames that takes > 1% of wall time is   │
│     a candidate.                                                     │
│                                                                      │
│ 5.  Check Micrometer:                                                │
│     - vertx_pool_queue_size{pool_type="worker"} > 0    → resize pool │
│     - vertx_pool_usage_seconds_max{event-loop} > 0.1   → handler bug │
│                                                                      │
│ 6.  If still stuck, take a JFR profile during the next incident.     │
│     Look for "Java Monitor Blocked" / "Socket Read" on event-loop    │
│     threads.                                                         │
│                                                                      │
│ 7.  GC-pause culprit? Look at GC log for > maxEventLoopExecuteTime   │
│     pauses. Switch to ZGC if not already.                            │
└──────────────────────────────────────────────────────────────────────┘
```

## 5.10  Cancellation does not stop a blocking call

A subtle but important property. Structured concurrency cancels
*coroutines*, but it cannot cancel an in-flight blocking JDK call.

```kotlin
val job = launch {
    awaitBlocking {
        Thread.sleep(60_000)         // this is in-flight
        println("done")
    }
}
delay(10)
job.cancel()                          // returns immediately
job.join()                            // blocks for ~60 s
```

`cancel()` marks the coroutine for cancellation; `join()` waits for it. But
the worker thread is sleeping inside the JDK and only the JDK can interrupt
it. `Thread.sleep` *will* throw `InterruptedException` if the worker is
interrupted, but `executeBlocking` does not interrupt by default — the
caller's cancellation only delivers when the block returns and the next
suspension point is reached.

**Practical consequence:** design your blocking code with **its own
timeout**, not just an outer coroutine timeout:

```kotlin
val rs = awaitBlocking {
    statement.queryTimeout = 5         // seconds, JDBC level
    statement.executeQuery(sql)
}
```

For `vertx-pg-client` you do not have this problem — the underlying network
operation *is* cancellable because there is no thread blocked on it.

## 5.11  Things people get wrong

**"`coAwait()` on a Future is the same as `awaitBlocking { Future... }`."**
No. `coAwait` *suspends* the coroutine; the event loop continues. `awaitBlocking`
*runs the block on a worker pool* and suspends the coroutine. Using
`awaitBlocking` to wait for a Future ties up a worker thread doing nothing.

**"I'll bump `maxEventLoopExecuteTime` to 10 seconds so the warnings go
away."** This is hiding the symptom. The warning is information; the
underlying block still hurts every concurrent request. Fix the block.

**"Virtual threads make blocking free."** They make *some* blocking
acceptable. They do not eliminate the wall-clock cost of waiting for a DB.
And they pin under `synchronized`, JNI, and a handful of legacy JDK paths.
Always profile with `-Djdk.tracePinnedThreads=full` in staging.

**"I'll spawn a `Thread { ... }.start()` to do the heavy work and unblock
the loop."** Now you have a thread Vert.x does not know about, will not
gracefully shut down, has no metrics. Use `executeBlocking` or a sized
`WorkerExecutor` — they integrate.

**"`executeBlocking` is async, so I can call it from a normal handler."**
You can. The returned Future completes back on the calling Context, which is
your event loop. Make sure the result-handling code is itself non-blocking.

**"I need to size the worker pool to my peak concurrent request rate."**
Almost never. Worker pool size = peak concurrent *blocking* operations, not
peak requests. Most requests do not touch the worker pool.

**"BlockedThreadChecker has its own overhead, I should disable it in prod."**
Don't. It walks a list of N event loops once per second. The cost is
nanoseconds. The benefit is catching production bugs in seconds rather than
days. We leave it on with `100 ms` threshold in `Main.kt`.

## 5.12  Try it

1. **Block on purpose.** In `HttpServerFactory.kt`, add a route that does
   `Thread.sleep(200)` inside its `coHandler`. Hit it with
   `wrk -t 4 -c 64 -d 30s http://localhost:8080/blocked`. Observe (a) the
   `BlockedThreadChecker` warnings printed at ~1 Hz, and (b) p99 latency on
   the *other*, non-blocked endpoint going up. Compare against the same load
   when you replace `Thread.sleep(200)` with `delay(200)`.

2. **Profile the block.** Run `asprof -e wall --threads -d 30 -f wall.html
   <pid>` while the load from step 1 is running. Open the flamegraph, filter
   to one event-loop thread, and find the `Thread.sleep` frame. Now replace
   the blocking call with
   `vertx.executeBlocking { Thread.sleep(200) }.coAwait()` and re-profile —
   confirm the frame moves to `vert.x-worker-thread-*`.

3. **Try a virtual-thread verticle.** Create a new verticle with
   `setThreadingModel(ThreadingModel.VIRTUAL_THREAD)` whose handler calls a
   blocking JDBC driver (use H2 in-memory). Deploy with high concurrency
   (`wrk -c 500`) and observe with `-Djdk.tracePinnedThreads=full` whether
   any pinning occurs. Wrap the JDBC call in `synchronized(this) { ... }` and
   re-run — you should see pinning warnings and a throughput collapse.

[← Ch 4](04-event-bus.md) · [Next: Chapter 6 — Logging →](06-logging.md)
