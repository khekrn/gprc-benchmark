# Chapter 6 — Logging Done Right

> **You should finish this chapter able to:** wire SLF4J + Logback for Vert.x,
> emit structured JSON logs, propagate request-scoped MDC across coroutines
> and event loops, avoid the hidden cost of synchronous appenders, and read
> the right thing from your logs in incidents.

Logging is the cheapest observability you have and the most misused. In a
reactor-based system it has two specific pitfalls that bring whole services
down:

1. **Synchronous appenders block the event loop.** A naive `FileAppender`
   doing a `write(2)` syscall on a hot path is exactly the kind of thing the
   blocked-thread checker exists to catch.
2. **MDC (mapped diagnostic context) is ThreadLocal**, which means it dies the
   moment you suspend a coroutine and resume on the "same" event-loop thread
   handling another request. Without a coroutine-aware propagator your trace
   IDs vanish at the first `coAwait`.

This chapter fixes both.

## 6.1  The logging stack we use

```
   Your code: log.info("user {} created", id)
   ──────────────────────────────────────────
   SLF4J 2.0.x         (the API)
   ──────────────────────────────────────────
   Logback 1.5.x       (the implementation)
   ──────────────────────────────────────────
   logstash-logback-encoder 8.x  (JSON layout)
   ──────────────────────────────────────────
   AsyncAppender → ConsoleAppender → STDOUT
```

The principle: **emit JSON to stdout, let the container runtime / log shipper
take care of routing.** Do not write to files inside the container.

| Library | Why |
|---|---|
| SLF4J 2.0 | Modern facade; `LoggerFactory.getLogger("Foo")` plus parameterized templates |
| Logback 1.5 | Reactive-friendly, fast, no JNDI lookups (post log4shell era) |
| logstash-logback-encoder | One-line JSON layout; field name configurable |
| jackson-databind | Already a dep of vertx; reused for JSON encoding |

We use SLF4J 2.0's modern `LoggingEventBuilder` API in hot paths to avoid the
varargs allocation:

```kotlin
log.atInfo()
    .setMessage("user created")
    .addKeyValue("user.id", id)
    .addKeyValue("user.email", email)
    .log()
```

Equivalent to `log.info("user created id={} email={}", id, email)` but with
**typed structured fields** that the JSON encoder emits as real JSON
properties instead of string-interpolated text. Splunk / Loki / Datadog
queries become trivial: `user.id="abc"` instead of regex over a message body.

## 6.2  Maven dependencies

Already in [`code/full-app/pom.xml`](../code/full-app/pom.xml):

```xml
<dependency>
  <groupId>org.slf4j</groupId>
  <artifactId>slf4j-api</artifactId>
  <version>${slf4j.version}</version>
</dependency>
<dependency>
  <groupId>ch.qos.logback</groupId>
  <artifactId>logback-classic</artifactId>
  <version>${logback.version}</version>
</dependency>
<dependency>
  <groupId>net.logstash.logback</groupId>
  <artifactId>logstash-logback-encoder</artifactId>
  <version>${logstash.encoder.version}</version>
</dependency>
```

**Important:** Vert.x ships with its own logging shim (`io.vertx.core.logging`)
that auto-detects SLF4J on the classpath. You don't need to call anything to
"wire it up" — but you *do* need to set a system property if you want Vert.x
to use SLF4J for its *own* logs:

```kotlin
System.setProperty(
    "vertx.logger-delegate-factory-class-name",
    "io.vertx.core.logging.SLF4JLogDelegateFactory",
)
```

We do this at the very top of `Main.kt`, *before* the first reference to a
Vert.x class. Doing it later is too late — Vert.x's static initializer has
already cached the delegate.

## 6.3  `logback.xml` — the actual config

Here is our [`logback.xml`](../code/full-app/src/main/resources/logback.xml)
in full, line-by-line:

```xml
<configuration scan="false" packagingData="false">

  <appender name="STDOUT" class="ch.qos.logback.core.ConsoleAppender">
    <encoder class="net.logstash.logback.encoder.LogstashEncoder">
      <includeContext>false</includeContext>
      <includeMdcKeyName>request_id</includeMdcKeyName>
      <includeMdcKeyName>trace_id</includeMdcKeyName>
      <includeMdcKeyName>span_id</includeMdcKeyName>
      <includeMdcKeyName>user_id</includeMdcKeyName>
      <timestampPattern>yyyy-MM-dd'T'HH:mm:ss.SSSXXX</timestampPattern>
      <fieldNames>
        <timestamp>ts</timestamp>
        <message>msg</message>
        <logger>logger</logger>
        <thread>thread</thread>
        <level>level</level>
      </fieldNames>
    </encoder>
  </appender>

  <appender name="ASYNC" class="ch.qos.logback.classic.AsyncAppender">
    <appender-ref ref="STDOUT"/>
    <queueSize>8192</queueSize>
    <discardingThreshold>0</discardingThreshold>
    <neverBlock>true</neverBlock>
    <includeCallerData>false</includeCallerData>
  </appender>

  <logger name="io.netty"      level="WARN"/>
  <logger name="io.vertx"      level="INFO"/>
  <logger name="com.example"   level="DEBUG"/>

  <root level="INFO">
    <appender-ref ref="ASYNC"/>
  </root>

</configuration>
```

**Why each setting matters:**

| Setting | Effect |
|---|---|
| `scan="false"` | We don't want Logback polling the file every minute |
| `packagingData="false"` | Disables expensive JAR-version lookup on each event |
| `LogstashEncoder` | Renders one JSON object per line |
| `includeContext="false"` | Drops global Logback context properties (noise) |
| `includeMdcKeyName` | Whitelist MDC keys (rather than dump everything) |
| `AsyncAppender queueSize=8192` | Buffers events; producer logs in O(1) |
| `discardingThreshold=0` | Don't drop INFO/WARN/ERROR even when queue full |
| `neverBlock=true` | If queue full, *drop the event* instead of blocking the event loop |
| `includeCallerData=false` | Don't compute stack-walks for caller line numbers (expensive) |

The `neverBlock=true` is critical. The default is **false**, which means once
the 8192-slot ring buffer fills up the producer thread *blocks*. If that
producer is your event loop, you've just blocked it. With `neverBlock=true`
the appender silently drops events when overloaded — a much better failure
mode than freezing every connection on that loop.

You can also see we silence Netty and Vert.x at INFO/WARN. Vert.x emits one
"Succeeded in deploying verticle" message per startup, which is fine. Netty
emits a lot of `LEAK` warnings if you forget to release ByteBufs in a custom
codec — keep WARN on for those.

## 6.4  Sample log lines

What comes out of stdout:

```json
{"ts":"2026-05-27T10:32:14.013+00:00","level":"INFO","thread":"vert.x-eventloop-thread-3","logger":"AppVerticle","msg":"REST server listening on 8080","request_id":null}
{"ts":"2026-05-27T10:32:14.044+00:00","level":"INFO","thread":"vert.x-eventloop-thread-3","logger":"c.e.a.h.UserHandler","msg":"user fetched","request_id":"01HXY9...","user.id":"u-42"}
```

Pipe through `jq` for local development:

```bash
./mvnw -pl full-app exec:java | jq -c '. | {ts, level, msg, request_id}'
```

In Datadog, query `service:full-app user.id:"u-42"` lights up immediately.

## 6.5  MDC and the coroutine problem

Mapped Diagnostic Context (MDC) is a `ThreadLocal<Map<String,String>>` inside
SLF4J that the encoder reads at log time. The classic pattern in
thread-per-request servers is:

```kotlin
MDC.put("request_id", req.id())   // at entry
try { handle(req) } finally { MDC.clear() }
```

**This pattern breaks in Vert.x.** Reasons:

1. One event loop thread serves *thousands* of requests. If you `MDC.put` at
   the start of request A and don't clear before the loop dispatches request
   B's continuation, B's log line will say `request_id=A`.
2. When you `coAwait()`, the rest of your handler resumes *later*, on the
   *same* event-loop thread. But between suspend and resume, many other
   handlers ran on that thread — each may have mutated MDC.

The fix has two parts.

### Part 1 — Use `MDCContext` from kotlinx-coroutines-slf4j

```xml
<dependency>
  <groupId>org.jetbrains.kotlinx</groupId>
  <artifactId>kotlinx-coroutines-slf4j</artifactId>
  <version>${kotlinx.coroutines.version}</version>
</dependency>
```

`MDCContext` snapshots the current MDC at coroutine launch and restores it on
every resumption. So:

```kotlin
import kotlinx.coroutines.slf4j.MDCContext

router.coroutineRouter {
    route().handler { ctx ->
        MDC.put("request_id", ctx.request().getHeader("x-request-id") ?: newId())
        ctx.next()
    }
    route("/api/v1/users/:id").coHandler { ctx ->
        // automatically: MDC is preserved across suspensions
        val user = users.get(ctx.pathParam("id"))
        // ...
    }
}
```

But this isn't enough on its own — the `coHandler { ... }` DSL launches a
coroutine, but does so with a context that does **not** include `MDCContext`
by default. So a request that suspends on `users.get(...)` loses MDC at the
suspension.

### Part 2 — A small extension to install MDCContext

Add this helper:

```kotlin
// MDCRouter.kt
package com.example.app.http

import io.vertx.ext.web.Router
import io.vertx.ext.web.RoutingContext
import io.vertx.kotlin.coroutines.CoroutineRouterSupport
import io.vertx.kotlin.coroutines.coroutineRouter
import kotlinx.coroutines.slf4j.MDCContext
import kotlinx.coroutines.withContext
import org.slf4j.MDC

/** Like coHandler but pins MDC into the coroutine context. */
fun Router.mdcRouter(block: io.vertx.ext.web.Route.() -> Unit = {}) {
    // Install once: every request gets a stable request_id in MDC.
    route().handler { ctx: RoutingContext ->
        val rid = ctx.request().getHeader("x-request-id")
            ?: java.util.UUID.randomUUID().toString()
        MDC.put("request_id", rid)
        ctx.addBodyEndHandler { MDC.remove("request_id") }   // clean up after response
        ctx.response().putHeader("x-request-id", rid)
        ctx.next()
    }
}

suspend inline fun <T> withMdc(crossinline block: suspend () -> T): T =
    withContext(MDCContext()) { block() }
```

And register it before your routes:

```kotlin
router.mdcRouter()
router.coroutineRouter {
    route("/api/v1/users/:id").coHandler { ctx ->
        withMdc {
            val user = users.get(ctx.pathParam("id"))
            // log here carries request_id correctly across suspend points
        }
    }
}
```

**Subtle but important detail:** for `MDC.put` *before* the coroutine launch
to propagate, you must enter a coroutine context that snapshots MDC at
launch. `MDCContext()` does that exactly. Without it, the snapshot happens at
the first `withContext(MDCContext())` and is empty (because by then we're on
the dispatcher thread without MDC).

### A cleaner alternative: per-handler scope

If you prefer not to use `MDC.put` directly, you can carry the request ID in
the coroutine context as a custom element:

```kotlin
data class RequestId(val value: String) : CoroutineContext.Element {
    companion object Key : CoroutineContext.Key<RequestId>
    override val key get() = Key
}

suspend fun currentRequestId(): String? =
    coroutineContext[RequestId]?.value
```

For most projects MDC + `MDCContext` is fine. The custom-element approach is
better if you also propagate things like a tenant ID, user role, etc.

## 6.6  Vert.x's request log: `LoggerHandler`

`vertx-web` ships a built-in access-log handler. You already added it in
[`HttpServerFactory.kt`](../code/full-app/src/main/kotlin/com/example/app/http/HttpServerFactory.kt#L42):

```kotlin
router.route().handler(LoggerHandler.create())
```

By default this emits common-log-format text. Switch to default JSON via the
logger name `io.vertx.ext.web.handler.impl.LoggerHandlerImpl` — but a better
approach is **write your own one-line access logger** so you control fields:

```kotlin
router.route().handler { ctx ->
    val start = System.nanoTime()
    ctx.addBodyEndHandler {
        val durMs = (System.nanoTime() - start) / 1_000_000.0
        log.atInfo()
            .setMessage("access")
            .addKeyValue("method", ctx.request().method().name())
            .addKeyValue("path",   ctx.request().path())
            .addKeyValue("status", ctx.response().statusCode)
            .addKeyValue("dur_ms", durMs)
            .addKeyValue("bytes",  ctx.response().bytesWritten())
            .log()
    }
    ctx.next()
}
```

`addBodyEndHandler` fires after the response is fully flushed. Use it instead
of `addEndHandler` (which fires on socket close — too late, and your log line
will say `status=0` if the connection was reset).

## 6.7  Log levels: a brief opinion

| Level | When to use |
|---|---|
| `ERROR` | A request failed in a way that needs human attention. Page the on-call. |
| `WARN`  | Recoverable but suspicious (retry succeeded, fallback used, 4xx with abnormal payload). |
| `INFO`  | Start/stop/deploy events, access log, slow-query/circuit-breaker state. **Never per-business-event.** |
| `DEBUG` | Per-call details: parameters, intermediate results. Disabled in prod by default. |
| `TRACE` | Wire-level. Body dumps. Disabled in prod and ideally in staging. |

A common mistake: emitting `INFO` for "successfully fetched user X". That
floods your log volume and bills, and obscures actually-interesting events.
Reserve `INFO` for things you'd grep for in an incident.

## 6.8  Exceptions: log them once, with the cause

```kotlin
try {
    users.get(id)
} catch (e: Exception) {
    // BAD: loses stack trace
    log.error("failed: " + e.message)

    // BAD: logs twice if the catcher rethrows
    log.error("failed", e)
    throw e

    // GOOD: log at the boundary (handler, scheduled task), rethrow once
    log.atError().setMessage("user fetch failed").setCause(e).log()
    throw e
}
```

Rule of thumb: **the boundary that decides on the HTTP/gRPC status code logs
the exception with its cause; everywhere else just rethrows**. Vert.x's
`coHandler` failure path will forward to the router's error handler which is
the right place to log.

## 6.9  Async appender: how it works underneath

The async-appender pattern is essentially a producer-consumer ring buffer.
Logback's `AsyncAppender` uses `ArrayBlockingQueue<ILoggingEvent>`:

```
producer threads (event loops)               consumer thread (logback-1)
       │                                              │
       │ append(event)                                │ poll(event)
       │                                              │ → format JSON
       │                                              │ → write(2) syscall
       │                                              │ → flush
       ▼                                              ▼
            ┌──────────────────────────────┐
            │  ArrayBlockingQueue (8192)   │
            └──────────────────────────────┘
```

The producer's `append()` is a single `offer(event)` — nanoseconds. The
expensive work (JSON formatting, the syscall) happens on **one dedicated
thread** that drains the queue. That thread is *not* one of your event loops.

`neverBlock=true` makes `offer()` return immediately when the queue is full,
dropping the event. Without it, the producer falls back to `put(event)`
which blocks until there is space.

There are even faster appenders (LMAX Disruptor via Log4j2 async loggers).
For 99% of services Logback's `AsyncAppender` with the right config is more
than enough.

## 6.10  Worked example: tracing one request

Curl with a custom request ID:

```bash
curl -H "x-request-id: r-demo-1" http://localhost:8080/api/v1/users/u-42
```

Logs you should see (filtered with `jq`):

```json
{"ts":"...","level":"INFO","logger":"AppVerticle","msg":"REST server listening on 8080"}
{"ts":"...","level":"INFO","logger":"access","msg":"access","method":"GET","path":"/api/v1/users/u-42","status":200,"dur_ms":3.4,"request_id":"r-demo-1"}
{"ts":"...","level":"DEBUG","logger":"c.e.a.d.UserService","msg":"cache miss","user.id":"u-42","request_id":"r-demo-1"}
{"ts":"...","level":"DEBUG","logger":"c.e.a.d.UserRepository","msg":"select by id","sql.duration_ms":1.2,"request_id":"r-demo-1"}
```

Notice the `request_id` propagates from the access log line into the
`UserService` and `UserRepository` lines, even though each ran on a different
*coroutine continuation* on the same event loop. That's `MDCContext` doing
its job.

## 6.11  Common pitfalls

**Calling `System.out.println` anywhere.** You bypass the framework, lose
JSON structure, and your stdout interleaves badly with Logback's stdout.

**Defining `log` as `val log = LoggerFactory.getLogger(this::class.java)` at
the top of a class.** That's fine in Java, but in Kotlin `this::class.java`
inside a constructor argument evaluates *before* the class is fully built.
Use either `LoggerFactory.getLogger(ThisClass::class.java)` or a companion
object: `companion object { private val log = LoggerFactory.getLogger(...) }`.

**Forgetting to set the SLF4J delegate factory for Vert.x.** Vert.x will
default to Java util logging and you'll get two unrelated formats in your
stdout. Set the system property *before any Vert.x class is loaded*.

**Including caller data.** `includeCallerData=true` triggers a stack walk per
log event. Easy 5-10× slowdown on hot paths. Always false in production.

**Logging request bodies.** Even if PII isn't an issue, body logging at
production volume costs you 10× the log bytes. Sample or strip first.

## 6.12  Try it

1. Set `vertx.logger-delegate-factory-class-name` to `JULLogDelegateFactory`
   (the default) and observe Vert.x's own startup messages. Then switch back
   to SLF4J and compare formats.
2. Stress the service with `wrk -t 8 -c 256 -d 10s ...` and watch a continual
   stream of logs. Then change `neverBlock` to `false` and try again under
   load. Observe how response p99 reacts (you should see a tail).
3. Replace `MDCContext` with the custom `RequestId` context element from
   §6.5. Implement `currentRequestId()` in your access logger and verify all
   downstream logs still receive it.

[← Ch 5](05-avoiding-blocking.md) · [Next: Chapter 7 — REST API with Vert.x Web →](07-rest-api.md)
