# Chapter 16 — Observability: metrics, tracing, health

> You will expose Prometheus metrics, wire OpenTelemetry tracing so a
> trace flows from HTTP → service → PG → gRPC, and run a real-readiness
> health check.

## 16.1 Metrics

We use **Micrometer** with a **Prometheus** registry, wired into
Vert.x via `vertx-micrometer-metrics`:

In Vert.x 5 `MicrometerMetricsOptions` **no longer has**
`setMicrometerRegistry`. To attach a pre-built registry you supply a
`MicrometerMetricsFactory` to the `Vertx` builder:

```kotlin
// observability/Metrics.kt
val registry = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

fun options() = MicrometerMetricsOptions()
    .setPrometheusOptions(VertxPrometheusOptions().setEnabled(true))
    .setEnabled(true)
    .setJvmMetricsEnabled(true)

// The registry is handed to Vert.x through a factory, not the options:
fun factory() = MicrometerMetricsFactory(registry)

// Retrieve the live backend registry anywhere with:
//   BackendRegistries.getDefaultNow()
```

And in `Main.kt` you build Vert.x with both the options and the factory:

```kotlin
val vertx = Vertx.builder()
    .with(VertxOptions().setMetricsOptions(Metrics.options()))
    .withMetrics(Metrics.factory())
    .build()
```

(`Vertx.vertx(options)` still exists but cannot attach a custom metrics
factory, so it cannot bind your pre-built registry.)

Free metrics you get:

- `jvm_memory_*`, `jvm_gc_*`, `jvm_threads_*`, `process_cpu_usage`
- `vertx_http_server_*` (requests, errors, latencies, by method+code)
- `vertx_pg_pool_*` (queue size, acquire time, in-use)
- `vertx_eventbus_*` (if you use the event bus)

Custom metrics in `Routes.kt`:

```kotlin
Metrics.registry.timer("http.request",
    "method", method, "status", statusCode
).record(elapsedNanos, TimeUnit.NANOSECONDS)
```

Expose at `/metrics`:

```kotlin
router.get("/metrics").handler { ctx ->
    ctx.response()
        .putHeader("Content-Type", "text/plain; version=0.0.4")
        .end(Metrics.registry.scrape())
}
```

A typical Prometheus scrape config:

```yaml
scrape_configs:
  - job_name: full-app
    metrics_path: /metrics
    static_configs:
      - targets: ['localhost:8080']
```

## 16.2 What to alert on

Per HTTP route:

- `p99 latency > X` for 5 min.
- `error rate > 1 %` for 5 min.
- `inflight > N` (saturation).

Per gRPC method:

- `grpc.request` with `status != OK` rate.

Per Pool:

- `acquire_time` p99 — high means pool too small.
- `wait_count` rising — same.
- connections opening/closing.

Per JVM:

- GC pause p99 (`jvm_gc_pause_seconds`).
- heap usage trend.

## 16.3 Tracing

OpenTelemetry has first-class Vert.x integration via
`opentelemetry-instrumentation-vertx`. Add the agent jar to the JVM:

```bash
java -javaagent:opentelemetry-javaagent.jar \
     -Dotel.service.name=full-app \
     -Dotel.traces.exporter=otlp \
     -Dotel.exporter.otlp.endpoint=http://localhost:4317 \
     -jar full-app.jar
```

The agent will:

- Start a span per HTTP request, with route + status.
- Inject `traceparent` into outgoing HTTP / gRPC.
- Track PG queries as child spans (with SQL).

In coroutine code, propagate the OTel `Context` via
`io.opentelemetry.extension.kotlin.asContextElement()`:

```kotlin
withContext(Context.current().asContextElement()) {
    repo.findById(id)
}
```

Without this, the span would close when the coroutine suspends.

## 16.4 Correlate traces with logs

Put the `traceId` and `spanId` into MDC. The OTel agent's
`MDCInstrumentation` does this automatically once enabled:

```bash
-Dotel.instrumentation.common.mdc.resource-attributes=enabled
```

Now every log line carries the trace id; click any log line in your
aggregator and pivot to the trace.

## 16.5 Health checks

Kubernetes wants two endpoints:

- `/healthz` (liveness): is the JVM alive? Cheap; never check the DB.
- `/readyz` (readiness): can I accept traffic? Check DB connectivity
  with a fast query.

Our skeleton:

```kotlin
router.get("/healthz").handler { it.response().end("ok") }
router.get("/readyz").handler { ctx ->
    scope.launch {
        try {
            pool.query("SELECT 1").execute().coAwait()
            ctx.response().end("ok")
        } catch (t: Throwable) {
            ctx.response().setStatusCode(503).end("not ready")
        }
    }
}
```

Don't block startup on `/readyz`. Mark "starting" → "ready" → "stopping"
via a tiny state field; readiness returns 503 during "stopping" so the
load balancer drains traffic gracefully.

## 16.6 Graceful shutdown

We installed a JVM shutdown hook in `AppShutdown.kt`:

```kotlin
Runtime.getRuntime().addShutdownHook(Thread({
    runBlocking {
        vertx.undeploy(deploymentId).coAwait()
        vertx.close().coAwait()
    }
}, "app-shutdown"))
```

`vertx.undeploy` calls our `stop()`, which closes HTTP, gRPC, Pool.
Active RPCs get a chance to complete (within the grace period).

For Kubernetes: a `preStop` hook that hits `/readyz?stop=true` to flip
to 503, sleeps 5 s for the LB to notice, then exits — gives you a
sub-second-loss-free deploy.

## 16.7 Exercises

1. Add a `grpc.request` timer via a generic `callHandler` (Vert.x 5 has
   no interceptor SPI) and graph p50/p99 per method.
2. Wire OTel exporter to a local Jaeger; confirm trace IDs in logs
   match span IDs in Jaeger.
3. Add a slow Pool by setting `maxSize=2`. Watch `pg_pool_queue_time`
   in Prometheus to identify saturation.

---

[← Chapter 15](15-grpc-interceptors.md) · [Next → Chapter 17: Performance & tuning](17-performance.md)
