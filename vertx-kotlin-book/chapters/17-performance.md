# Chapter 17 — Performance & tuning for high traffic

> Cookbook of the knobs that matter. We measure each, set defaults that
> hold up for ~100 k RPS on commodity hardware, and tell you what to
> measure next.

## 17.1 The general method

1. **Get a baseline** with `hey` / `wrk` / `ghz` on a *cold* checkout.
2. **Watch four things**: requests/sec, p99 latency, JVM CPU%, GC pause.
3. **Change one variable**. Re-measure.
4. **Promote** the change if numbers move; revert otherwise.
5. **Save the rig** as a `Makefile` target so you can re-run.

Don't tune without measurement; you'll change the wrong knob.

## 17.2 Vert.x knobs

```kotlin
Vertx.vertx(
    VertxOptions()
        .setEventLoopPoolSize(N)           // default 2 * cores
        .setWorkerPoolSize(W)              // default 20
        .setInternalBlockingPoolSize(I)    // default 20
        .setPreferNativeTransport(true)    // epoll/kqueue
        .setBlockedThreadCheckInterval(100L)
        .setMaxEventLoopExecuteTime(100_000_000L) // ns; 100 ms
        .setMaxWorkerExecuteTime(1_000_000_000L)
)
```

Heuristics:

| Workload                   | EventLoopPoolSize         | Instances of verticle |
|----------------------------|---------------------------|-----------------------|
| 100 k+ tiny replies        | cores                     | 1                     |
| heavy fan-out per request  | cores                     | cores                 |
| many slow blocking calls   | (move to worker)          | n/a                   |

If you see CPU saturated on event loops, scale instances. If you see
some loops hot and others idle, you likely have sticky long
connections (gRPC streams). Add instances; load balancer / kernel
will spread future connections.

## 17.3 HTTP server tuning

```kotlin
vertx.createHttpServer(
    HttpServerOptions()
        .setAcceptBacklog(2048)
        .setReuseAddress(true)
        .setReusePort(true)               // requires linux + N instances
        .setTcpFastOpen(true)
        .setTcpNoDelay(true)
        .setTcpQuickAck(true)
        .setIdleTimeout(60)
        .setReadIdleTimeout(60)
        .setWriteIdleTimeout(60)
)
```

- `reusePort` + `setInstances(N)` = N independent sockets for `:8080`,
  load-balanced by the kernel.
- `tcpFastOpen` cuts a round-trip for re-connecting clients. Most
  clouds support it.
- `idleTimeout` keeps zombies from holding file descriptors.

## 17.4 HTTP/2 tuning

```kotlin
HttpServerOptions()
    .setUseAlpn(true)
    .setSsl(...)
    .setInitialSettings(
        Http2Settings()
            .setMaxConcurrentStreams(2048)
            .setInitialWindowSize(1 shl 20)   // 1 MB per stream
    )
```

Default initial window (64 KB) is small for streaming. Bumping it
reduces window-update chatter. Increase per-stream max for high-RPS
multiplexing.

## 17.5 Postgres pool tuning

| Symptom                  | Fix                                                |
|--------------------------|----------------------------------------------------|
| `pg_pool.queue.time` p99 high | increase `setMaxSize`                          |
| `pg_pool.in_use` always full | scale verticle instances or pool size           |
| many short queries, pool small | increase `setPipeliningLimit`                 |
| connection churn         | check `setReconnectAttempts`, app reconnect logic  |

Start at `maxSize = 2 × cores`. Pipelining at `256`. Move from there.

## 17.6 JVM tuning

In our Dockerfile:

```
-XX:MaxRAMPercentage=75
-XX:+UseZGC
-XX:+UnlockExperimentalVMOptions
-XX:+EnableDynamicAgentLoading
```

Modern defaults that just work:

- **ZGC generational** is the default on JDK 25.
- **`MaxRAMPercentage=75`** keeps room for off-heap (Netty direct
  buffers) without us computing exact MB.
- **Don't use `-Xmx`** in containers; let `MaxRAMPercentage` see the
  cgroup limit.

Direct buffers Netty allocates count against `-XX:MaxDirectMemorySize`,
which by default equals `-Xmx`. For very high traffic you may want
`-XX:MaxDirectMemorySize=512m` explicit.

## 17.7 Native transports

Linux: `epoll` via Netty. Add the `netty-transport-classes-epoll` and
classifier `linux-x86_64` to your runtime.

io_uring: requires kernel ≥ 5.6 and `netty-incubator-transport-classes-io_uring`.
Vert.x 5 supports it but it's not the default. For very chatty
workloads (many small messages), measure ~5-15 % gain.

## 17.8 Where the latency goes

Anatomy of a `GET /api/users/:id` at p99 ≈ 5 ms:

```
  TLS handshake (warm conn)     0   μs
  HTTP parsing                 30   μs
  routing & MDC                10   μs
  pool acquire                 50   μs
  Postgres round trip         3000  μs
  row mapping                  10   μs
  JSON encode                 100   μs
  HTTP write                   30   μs
  Netty + kernel               --   μs
                            ───────
                             3230  μs
```

Postgres dominates. Tune the SQL (indexes!) before tuning the JVM.

## 17.9 Load-test rig

```bash
# REST
hey -n 100000 -c 200 -m GET http://localhost:8080/api/users/1

# gRPC unary
ghz --insecure \
    --proto code/full-app/src/main/proto/users.proto \
    --call com.example.app.grpc.Users/GetUser \
    -d '{"id":1}' -c 200 -n 100000 \
    localhost:9090

# gRPC server streaming throughput
ghz --insecure --proto … --call …/ListUsers -d '{}' -c 50 -z 30s localhost:9090
```

Look at:

- Throughput (req/s)
- p50, p95, p99, p999
- Error rate

## 17.10 Quick wins

1. **Index your SQL.** A missing index is a 100× perf hit.
2. **Reduce allocations.** Reuse `Buffer`/`StringBuilder` for hot
   formatting. Use `vertx.fileSystem().setReuseAddress`.
3. **Avoid `JsonObject.mapFrom`** in hot paths if your data class is
   tiny — write the JSON by hand or use Jackson with a precompiled
   `ObjectWriter`.
4. **Don't log on every request at INFO.** Log at DEBUG with sampling.

## 17.11 Exercises

1. Add an index on `(email)` (already there), then drop it. Compare
   `findByEmail` latency. Why the slope?
2. Crank `maxSize` to 64. Watch `pg_pool.acquire.time`. Find the
   knee of the curve.
3. Enable epoll on a Linux box; compare with NIO baseline. Run
   `perf stat -e cycles` if you want to see why it's faster.

---

[← Chapter 16](16-observability.md) · [Next → Chapter 18: Testing](18-testing.md)
