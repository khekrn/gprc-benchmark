# Chapter 7 — Config, structured logging, MDC across coroutines

> You will have a layered config loaded from YAML + env, JSON logs in
> production with request-correlation IDs that survive across coroutine
> suspensions, and a `withMdc { }` helper for ad-hoc context.

## 7.1 Config layering

We use `vertx-config` with three stores, layered later-overrides-earlier:

1. **YAML** in classpath `config/application.yaml` — sensible defaults.
2. **Environment variables** — `DB_PASSWORD=…`, picked up via the env
   store with `_` → `.` mapping.
3. **System properties** — `-Dhttp.port=9000` for local overrides.

```kotlin
// code/full-app/src/main/kotlin/com/example/app/config/AppConfig.kt
val retriever = ConfigRetriever.create(
    vertx,
    ConfigRetrieverOptions()
        .addStore(yamlStore("config/application.yaml"))
        .addStore(envStore())
        .addStore(sysStore())
)
```

`AppConfig` is a strongly-typed object. We parse the JSON once and pass
the typed `AppConfig` around. Untyped `JsonObject` lookups in business
code are an anti-pattern.

## 7.2 Why we don't use `@Value`-style injection

A YAML file plus a single typed object beats fifteen `@Value` annotations
scattered across the codebase:

- One place to read.
- Compile-time-safe accessors (`cfg.db.poolMaxSize`).
- Easy to mock in tests.
- Easy to dump for debugging.

## 7.3 Hot reload

`ConfigRetriever` can poll the YAML and emit changes:

```kotlin
retriever.listen { change ->
    val updated = parse(change.newConfiguration)
    redeploy(updated)
}
```

We do *not* enable hot reload in the demo — for a small service it's
not worth the complexity. For a config-driven feature-flag system you
might.

## 7.4 Logback configuration

We use SLF4J + Logback with two encoder choices:

- `CONSOLE_TEXT`: human-friendly, includes MDC keys inline.
- `CONSOLE_JSON`: `LogstashEncoder` for production aggregation.

Both wrap in `AsyncAppender` to avoid blocking the event loop on a slow
console / stdout pipe. The `neverBlock=true` setting *drops* log events
when the queue is full — preferable to a stalled event loop. Adjust
`queueSize` for your noise level.

`logback.xml` excerpt:

```xml
<appender name="ASYNC" class="ch.qos.logback.classic.AsyncAppender">
    <appender-ref ref="CONSOLE_TEXT"/>
    <queueSize>8192</queueSize>
    <discardingThreshold>0</discardingThreshold>
    <neverBlock>true</neverBlock>
</appender>
```

## 7.5 MDC across coroutines

SLF4J's `MDC` is thread-local. Coroutines hop threads on resume. Without
plumbing, your `requestId` set before `coAwait` is gone after.

We ship a `MDCContext` (`observability/MdcSupport.kt`) that implements
`ThreadContextElement`. It snapshots the current MDC into the coroutine
context. On every dispatch / resume, kotlinx-coroutines calls
`updateThreadContext` / `restoreThreadContext`, putting MDC back where
the suspending function expects to find it.

Usage:

```kotlin
withContext(MDCContext(mapOf("requestId" to reqId))) {
    log.info("started")     // requestId is in the line
    repo.findById(id)       // suspension: ok, MDC restored on resume
    log.info("done")        // requestId still in the line
}
```

In `Routes.coHandler` we launch the route's coroutine inside an
`MDCContext` we built from the `requestId` header.

## 7.6 Production-friendly log fields

| Field        | Source                       | When to add                          |
|--------------|------------------------------|---------------------------------------|
| `requestId`  | inbound header or generated  | every external-facing request         |
| `traceId`    | W3C `traceparent` header     | wired by Chapter 16's OTel module     |
| `spanId`     | OTel                         | wired by Chapter 16                   |
| `userId`     | auth filter                  | for user-action audit                 |
| `verticle`   | static                       | when >1 verticle type                 |
| `pgConn`     | Pool stats                   | debugging connection contention       |

Keep the MDC small. Big MDC = big log lines = log shipping cost.

## 7.7 Common gotchas

1. **MDC in `executeBlocking`.** Vert.x's worker pool also resets the
   thread-locals. Use `MDCContext` or pass IDs explicitly.
2. **JSON encoder + secrets.** `LogstashEncoder` will happily serialise
   any object you log. Sanitise. Annotate sensitive types or use a
   redaction filter.
3. **Logging in a tight loop.** Each `log.info` is a method call, a
   format, and an enqueue. In hot paths, log at `DEBUG` with a
   `isDebugEnabled` guard.

## 7.8 Exercises

1. Add a `userId` to the MDC after looking up the user, log inside a
   suspended block, confirm the field is on the resumed log line.
2. Throw an exception in a `coHandler`. Find the log line. Does it
   contain the `requestId`? It should.
3. Switch the appender to `CONSOLE_JSON` via an env var. (Hint:
   Logback supports `<if condition>` JNI — or set a property and use
   `<then>`/`<else>`.)

---

[← Chapter 6](06-structured-concurrency.md) · [Next → Chapter 8: REST with Vert.x Web](08-rest-api.md)
