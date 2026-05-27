# Chapter 17 — Production Best Practices

> **You should finish this chapter able to:** ship a Vert.x service that
> handles graceful shutdown, exposes complete observability (metrics, traces,
> logs), implements circuit breakers and bulkheads, defends against
> back-pressure, deploys safely, and survives 3 AM incidents.

This is the consolidation chapter. You already know event loops, coroutines,
verticles, persistence, gRPC, REST, Netty, Docker, and performance. This
chapter ties it together with the operational concerns that turn a working
prototype into a service you can rely on.

## 17.1  The production checklist

Print this. Tape it to your monitor.

**Lifecycle**
- [ ] Graceful shutdown on SIGTERM with in-flight request drain.
- [ ] Liveness and readiness probes are *different* endpoints.
- [ ] `start()` exits before any traffic is taken.
- [ ] `stop()` undeploys child verticles, closes pools, flushes logs.

**Observability**
- [ ] Structured JSON logs to stdout, no file appenders in container.
- [ ] MDC carries `request_id` / `trace_id` across coroutines.
- [ ] Prometheus `/metrics` endpoint with histograms (not just gauges).
- [ ] Distributed traces via OpenTelemetry.
- [ ] At least one alert per SLI (latency, errors, saturation).

**Resilience**
- [ ] Per-call timeouts at every external boundary.
- [ ] Circuit breaker around each downstream.
- [ ] Bulkheads: bounded queues, pool sizes.
- [ ] Backpressure on slow producers; reject early, don't queue forever.
- [ ] Retries with exponential backoff + jitter.

**Security**
- [ ] Non-root container user.
- [ ] No secrets in env vars (use mounted secret files or Vault).
- [ ] TLS verified (not `setTrustAll(true)`!).
- [ ] Rate limiting at the edge.
- [ ] Dependency scanning in CI.

**Performance**
- [ ] Native transports on Linux.
- [ ] `SO_REUSEPORT` for multi-instance accept scaling.
- [ ] Blocked-thread checker tuned and respected.
- [ ] Pool sizing measured (not guessed).
- [ ] Allocations on hot paths reviewed.

**Operations**
- [ ] Runbook for top 5 alerts.
- [ ] Versioned, content-hash-tagged images.
- [ ] Canary / blue-green deploy strategy.
- [ ] Backup / DR strategy (DB, Redis, configs).

## 17.2  Graceful shutdown — done right

In our [`AppShutdown.kt`](../code/full-app/src/main/kotlin/com/example/app/observability/AppShutdown.kt):

```kotlin
object AppShutdown {
    fun install(vertx: Vertx, deploymentId: String) {
        Runtime.getRuntime().addShutdownHook(Thread {
            try {
                vertx.undeploy(deploymentId)
                    .toCompletionStage().toCompletableFuture()
                    .get(15, TimeUnit.SECONDS)
                vertx.close()
                    .toCompletionStage().toCompletableFuture()
                    .get(5, TimeUnit.SECONDS)
            } catch (e: Exception) {
                System.err.println("shutdown error: $e")
            }
        }, "shutdown-hook"))
    }
}
```

What happens on `kill -15 <pid>`:

```
1. JVM receives SIGTERM
2. JVM runs shutdown hooks (our hook)
3. Hook calls vertx.undeploy(id) → calls AppVerticle.stop()
4. AppVerticle.stop() {
     - server.close() — close listeners, refuse new connections
     - wait for in-flight requests to complete (or timeout)
     - userRepo.close() — drain PG pool
     - cache.close() — close Redis
   }
5. Hook calls vertx.close() — closes event loops, worker pool
6. JVM exits
```

### What "graceful" means in Kubernetes

Kubernetes sends SIGTERM, then waits `terminationGracePeriodSeconds`
(default 30) before SIGKILL. To shed traffic *first*:

```yaml
lifecycle:
  preStop:
    exec:
      command: ["sh", "-c", "sleep 5; curl -X POST localhost:8080/internal/drain"]
terminationGracePeriodSeconds: 60
```

The `sleep 5` gives the Kubernetes Service / load balancer time to remove
this pod from the endpoint list. Then `/internal/drain` flips readiness to
unhealthy so any straggling requests get drained.

After `preStop` finishes, K8s sends SIGTERM, your shutdown hook runs, you
have 55 more seconds to drain.

## 17.3  Health: liveness vs readiness vs startup

| Probe | What it checks | What happens on failure |
|---|---|---|
| **Startup** | "Has the JVM finished booting?" | Container restart after N failures |
| **Liveness** | "Is the JVM alive enough to reply?" | Container restart |
| **Readiness** | "Should traffic come here?" | Removed from Service endpoints |

The pitfalls:

- **Don't make liveness depend on DB or Redis.** If the DB is down, your
  service shouldn't restart-loop. It should fail readiness (= no traffic) but
  stay alive so it can recover.
- **Make readiness depend on critical downstreams.** If PG is unreachable,
  *do* fail readiness so the LB stops sending traffic.
- **Set startup probe `failureThreshold` high enough.** A cold JVM with CDS
  off can take 30 s. Don't restart-loop because the boot was slow.

```kotlin
val health = HealthChecks.create(vertx)

// Liveness — JVM is up
router.get("/healthz/live").handler { ctx ->
    ctx.response().setStatusCode(200).end("OK")
}

// Readiness — dependencies OK
health.register("postgres") { promise ->
    vertxFuture {
        if (userRepo.ping()) promise.complete(Status.OK())
        else promise.complete(Status.KO())
    }
}
health.register("redis") { promise ->
    vertxFuture {
        if (cache.ping()) promise.complete(Status.OK())
        else promise.complete(Status.KO())
    }
}
router.get("/healthz/ready").handler(HealthCheckHandler.createWithHealthChecks(health))
```

## 17.4  Distributed tracing with OpenTelemetry

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-opentelemetry</artifactId>
</dependency>
<dependency>
  <groupId>io.opentelemetry</groupId>
  <artifactId>opentelemetry-exporter-otlp</artifactId>
</dependency>
```

Wire in `Main.kt`:

```kotlin
val openTelemetry: OpenTelemetry = OpenTelemetrySdk.builder()
    .setTracerProvider(SdkTracerProvider.builder()
        .addSpanProcessor(BatchSpanProcessor.builder(
            OtlpGrpcSpanExporter.builder()
                .setEndpoint("http://otel-collector:4317")
                .build()).build())
        .setResource(Resource.getDefault().toBuilder()
            .put("service.name", "full-app")
            .put("service.version", "1.0.0")
            .build())
        .build())
    .setPropagators(ContextPropagators.create(W3CTraceContextPropagator.getInstance()))
    .buildAndRegisterGlobal()

val vertx = Vertx.builder()
    .with(VertxOptions().setTracingOptions(OpenTelemetryOptions(openTelemetry)))
    .build()
```

Now every HTTP / gRPC / SQL / Redis call automatically emits spans with
parent-child relationships. To add custom spans:

```kotlin
val tracer = openTelemetry.getTracer("com.example.app")

suspend fun process(req: Request) {
    val span = tracer.spanBuilder("process").startSpan()
    span.makeCurrent().use {
        try {
            // your code
        } catch (e: Exception) {
            span.recordException(e)
            span.setStatus(StatusCode.ERROR)
            throw e
        } finally {
            span.end()
        }
    }
}
```

Trace IDs propagate **automatically** through HTTP headers (`traceparent`),
gRPC metadata, and the event bus. The MDC integration we set up in chapter
6 puts the current trace ID in every log line — when you click a slow span
in Jaeger, you can grep logs by trace ID and see everything that happened.

## 17.5  Circuit breakers

When a downstream is failing, *stop* sending requests to it. Vert.x has
`vertx-circuit-breaker`:

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-circuit-breaker</artifactId>
</dependency>
```

```kotlin
val breaker = CircuitBreaker.create("ddb-cb", vertx, CircuitBreakerOptions()
    .setMaxFailures(5)            // after 5 consecutive failures...
    .setTimeout(2_000)            // each call has 2s deadline
    .setResetTimeout(30_000)      // wait 30s before half-opening
    .setFallbackOnFailure(true))

suspend fun getUser(id: String): User? = breaker.execute<User?> { promise ->
    vertxFuture { repo.findById(id) }
        .onSuccess(promise::complete)
        .onFailure(promise::fail)
}.recover { fallbackUser(id) }
 .coAwait()
```

State machine:

```
CLOSED ─────► OPEN ─────► HALF_OPEN ─────► CLOSED
  ↑           (refuse        (try one)         (success)
  └──         requests)                        │
   <─────────────────── (failure)──────────────┘
```

Use one breaker per *downstream*, not per call site. A breaker is *state*,
shared by all callers of that downstream.

## 17.6  Bulkheads: pool sizes and queue depths

A bulkhead isolates failures so a slow downstream doesn't drag down the
whole service. Concrete bulkheads in our app:

- **`maxSize=16`** on PG pool: at most 16 in-flight DB queries.
- **`maxWaitQueueSize=1024`** on the pool: at most 1024 queued before
  rejection.
- **`maxConcurrency`** on `executeBlocking`: bounded worker pool.
- **`BodyHandler.setBodyLimit(256 * 1024)`** on REST: cap per-request memory.
- **Hard 5 s `TimeoutHandler`**: kill requests that take forever.

Without bulkheads, one downstream queue can chain-fail every adjacent service.

## 17.7  Retries (and when not to)

Retry only:
- Idempotent operations (GET, PUT, DELETE — never POST without an idempotency key).
- Errors that are transient: timeouts, 503, gRPC `UNAVAILABLE`, DDB `Throttling*`.

Don't retry:
- 400, 401, 403, 404, gRPC `INVALID_ARGUMENT` — these are the caller's bug.
- 5xx where the body indicates a deterministic bug.

Pattern (chapter 16 used the same):

```kotlin
suspend fun <T> retry(maxAttempts: Int = 3, baseMs: Long = 50, block: suspend () -> T): T {
    var attempt = 0
    while (true) {
        try {
            return block()
        } catch (e: Exception) {
            if (++attempt >= maxAttempts || !isRetryable(e)) throw e
            delay(jitter(baseMs * (1L shl attempt)))
        }
    }
}
```

**Always combine retries with a circuit breaker.** Naked retry-on-failure
is how a small outage becomes a cascading meltdown.

## 17.8  Idempotency keys

For non-idempotent operations (POST, mutations), require the client to send
`Idempotency-Key: <uuid>`. Store the response in Redis with a long TTL:

```kotlin
router.post("/api/v1/orders").coHandler { ctx ->
    val key = ctx.request().getHeader("Idempotency-Key")
        ?: return@coHandler problem(ctx, 400, "missing_idempotency_key")
    val cached = cache.getJson<JsonObject>("idem:$key")
    if (cached != null) {
        ctx.response().setStatusCode(cached.getInteger("status")).end(cached.getString("body"))
        return@coHandler
    }
    val result = createOrder(ctx.body().asJsonObject())
    cache.putJson("idem:$key", JsonObject().put("status", 201).put("body", result.encode()), ttlSeconds = 86400)
    ctx.response().setStatusCode(201).end(result.encode())
}
```

Now if the client retries (network blip), they get the *same* response and
no duplicate order is created.

## 17.9  Rate limiting

Three layers:

1. **At the edge** (Envoy, AWS WAF, nginx). Per-IP, per-route.
2. **At the service** for per-tenant quotas:
   ```kotlin
   val rateLimiter = RedisRateLimiter(cache, capacity = 100, refillPerSec = 10)
   router.route("/api/v1/*").coHandler { ctx ->
       val key = ctx.user().principal().getString("sub")
       if (!rateLimiter.allow(key)) return@coHandler problem(ctx, 429, "too_many_requests")
       ctx.next()
   }
   ```
3. **At the downstream client** (token bucket on DB writes, etc.) to avoid
   storming the database during spikes.

## 17.10  Observability dashboards — what to chart

A starter Grafana dashboard for any Vert.x service:

**Row 1: RED metrics**
- Rate: `sum(rate(http_server_responses_total[1m]))`
- Errors: `sum(rate(http_server_responses_total{status=~"5.."}[1m]))`
- Duration: p50, p95, p99 of `http_server_response_time_seconds`

**Row 2: USE metrics for resources**
- Utilization: CPU% from JVM, event loop blocked time
- Saturation: pool queue depth, worker pool busy threads
- Errors: pool acquisition failures, OOM events

**Row 3: dependencies**
- PG: `vertx_pool_in_use{pool_name="pg-pool"}`, `vertx_pool_queue_pending`
- Redis: connection count, command rate
- gRPC client: outbound latency, error rate per upstream

**Row 4: JVM**
- Heap used / max
- GC pause times (histogram!)
- Thread count, deadlocks
- Class loading

## 17.11  Logging strategy

We covered the *mechanics* in chapter 6. Operationally:

- **Log volume budget.** A service should produce ~1 log line per request
  (the access log) plus maybe 1-2 INFOs per minute. If you're north of that,
  you're not searching, you're swimming.
- **Structured fields, always.** `addKeyValue("user.id", id)` not
  `info("user $id")`. Logs are queries to your future self.
- **Sample DEBUG.** Don't enable DEBUG globally in prod. Enable per-request
  via a header: `X-Debug: 1` → flip the verbosity for that request only.
- **No PII in logs.** Hash or truncate user emails. Redact secrets in
  request body logs.

## 17.12  Deploy strategy

Two patterns:

**Rolling update (default in K8s):**
```
v1 v1 v1 v1   →  v2 v1 v1 v1   →  v2 v2 v1 v1   →  v2 v2 v2 v2
```

Safe if graceful shutdown works and you have a small backward-incompat
surface. Most services use this.

**Canary deploy:**
```
v1 v1 v1 v1 v1 v1 v1 v1 v1 v1   (10 replicas)
v1 v1 v1 v1 v1 v1 v1 v1 v1 v2   (1 canary at 10% traffic)
                                  ← watch SLOs for 15 min
v1 v1 v1 v1 v1 v1 v1 v1 v1 v2   ← if good, promote
v2 v2 v2 v2 v2 v2 v2 v2 v2 v2
```

Canary lets you observe real-world behavior on a small slice before
committing. Implement via service-mesh weights (Istio, Linkerd) or a header
that overrides routing.

## 17.13  Backups and DR

- **Postgres**: continuous `pg_basebackup` + WAL archiving to S3.
- **Redis**: not your source of truth, but if you must, enable AOF + RDB.
- **Config**: in Git, with versioned releases.
- **Secrets**: in Vault or AWS Secrets Manager with rotation.
- **DR drill**: restore from backup into a fresh environment at least
  once a quarter. Untested backups don't exist.

## 17.14  On-call essentials

For every alert in your alert manager, the corresponding runbook section:

```
# Alert: PgPoolExhaustion

Symptom: vertx_pool_queue_pending{pool_name="pg-pool"} > 50 for 5 min

Likely causes:
  - DB CPU saturated (check `pg_stat_activity`)
  - Slow query (check `pg_stat_statements`)
  - Connection storm from a new client

Mitigation (in order):
  1. Check downstream DB health (Grafana dashboard X).
  2. If DB is healthy, bounce one app pod to clear in-flight queries.
  3. If problem persists, raise pool size temporarily (cfg edit + rollout).
  4. Find the slow query and disable it via feature flag.

Recovery: queue depth returns to <5.
```

This single template, filled in for your top 10 alerts, saves *years* of
on-call sanity.

## 17.15  Common production failures (and prevention)

| Symptom | Root cause | Prevention |
|---|---|---|
| OOM after 6 hours | Memory leak in handler closures | Heap dump + jhat / async-profiler alloc mode |
| Spike then crash | Unbounded queue / no rate limit | Bounded queues, rate limits at edge |
| Restart loop on deploy | Startup probe too aggressive | Increase failureThreshold; warm-up CDS |
| 503s during scale-up | New pod takes traffic before ready | Readiness probe + `terminationGracePeriodSeconds` |
| Trace IDs missing | MDC not propagated through coroutines | `MDCContext()` (chapter 6) |
| One request blocks event loop | Blocking SDK in handler | Blocked-thread checker + virtual threads (chapter 5) |
| All requests slow | DB connection pool exhausted | Pool size + queue metric |
| Cache stampede on TTL boundary | All keys expire at once | Jittered TTLs + single-flight (chapter 9) |
| Stuck reading 1 client | No idle timeout on HTTP server | `setIdleTimeout(30)` |
| Logs to /dev/null | Sync appender blocked → events dropped | Async appender with neverBlock |

## 17.16  Beyond this series

Topics we touched on briefly that deserve their own deep dives:

- **Event sourcing & CQRS** with Vert.x (verticles as aggregate roots, event
  bus as the journal).
- **Service mesh** integration (Istio, Linkerd) and how Vert.x interacts with
  sidecar proxies.
- **Long-running jobs** with the worker pool + a job table for resumability.
- **Vert.x clustered mode** (Hazelcast / Infinispan) — useful only for
  niche cases; mostly redundant given service meshes and external KV.
- **Native image** with GraalVM — Vert.x 5 has experimental support; pay
  attention to closed-world reflection assumptions.

## 17.17  Closing words

Vert.x rewards understanding. Each abstraction is thin — Verticle, Future,
event loop, dispatcher — and once you internalize them, everything composes.
The non-blocking promise isn't magic; it's discipline plus a runtime that
makes the discipline cheap.

If you took one thing from this series, take this: **never block the event
loop**. Everything else is a consequence — coroutines, pools, observability,
back-pressure. Keep the loop spinning and you keep latency predictable.

You're now equipped to build the next thing — including, perhaps, "beam v2".

## 17.18  Try it (the final challenges)

1. Build a circuit-breaker dashboard: per-downstream open/closed/half-open
   state, request rate, failure rate. Page on `open` lasting > 60 s.
2. Implement an OpenTelemetry export to your local Tempo / Jaeger and
   confirm trace IDs propagate from REST through gRPC to Postgres.
3. Write a chaos-engineering test: kill the PG container mid-load. Verify
   readiness flips, traffic drains; restore PG, verify recovery within 60 s.
4. Read the Vert.x 5 source code for `HttpServerImpl`. You should now be
   able to follow the entire request lifecycle from `accept()` to byte
   write. Bonus: read your favorite Netty handler in detail.

[← Ch 16](16-dynamodb-driver.md) · [← Index](../README.md)
