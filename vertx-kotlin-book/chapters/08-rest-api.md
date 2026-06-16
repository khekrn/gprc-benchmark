# Chapter 8 — REST API with Vert.x Web + coroutines

> By the end of this chapter you will have a clean idiom for writing
> coroutine-based routes, NDJSON streaming with end-to-end
> back-pressure, problem+json error handling, and per-request metrics.

## 8.1 Vert.x Web is the router; the handler is just code

`Vert.x Web`'s `Router` is a `chain of route matchers → handler`. Each
handler receives a `RoutingContext` with the request, response, and a
key/value bag for sharing state between handlers (auth, request id).

```kotlin
val router = Router.router(vertx)
router.route().handler(BodyHandler.create())
router.get("/api/users/:id").handler { ctx -> /* … */ }
```

The handler runs on the **event loop**. Our `coHandler` extension
launches a coroutine in the verticle's scope so the body can suspend.

```kotlin
// code/full-app/src/main/kotlin/com/example/app/http/Routes.kt
private fun Route.coHandler(block: suspend (RoutingContext) -> Unit) =
    handler { ctx ->
        val reqId = ctx.get<String>("requestId") ?: "-"
        scope.launch(MDCContext(mapOf("requestId" to reqId))) {
            try {
                block(ctx)
            } catch (t: Throwable) {
                if (!ctx.response().ended()) writeProblem(ctx, 500, "Internal Server Error", t.message)
            }
        }
    }
```

We start each request's coroutine inside an `MDCContext` so the
`requestId` is in every log line, before, during, and after suspension.

## 8.2 The route file in full

The route file (`Routes.kt`) is short. The structure:

```
mount(router) {
    request-id tagging  (always)
    metrics timer       (always)
    error handlers      (500, 404)
    /healthz, /readyz   (cheap)
    /metrics            (Prometheus scrape)
    /api/users/:id      GET    coHandler  →  handleGetUser
    /api/users          POST   coHandler  →  handleCreateUser
    /api/users/bulk     POST   coHandler  →  handleBulkCreate
    /api/users          GET    coHandler  →  handleStreamUsers (NDJSON)
}
```

The handlers themselves are *small*. They:

1. Parse and validate input. Bad input → 400 problem+json.
2. Call the `UserService`.
3. Translate domain errors to HTTP codes.
4. Encode the result.

## 8.3 Validation: keep it close to the handler

We do validation at the edge — in the handler — by constructing a
`NewUser` whose `init` block enforces invariants:

```kotlin
data class NewUser(val email: String, val fullName: String) {
    init {
        require(email.contains('@')) { "email must contain '@'" }
        require(fullName.isNotBlank()) { "fullName must not be blank" }
        require(email.length <= 320) { "email too long" }
        require(fullName.length <= 200) { "fullName too long" }
    }
}
```

If you outgrow `init { require(...) }`, use `vertx-web-validation`
which can validate against a JSON Schema and feed errors into the
RoutingContext. For a service this small, plain Kotlin wins on
readability.

## 8.4 Error responses follow RFC 7807 (problem+json)

```kotlin
private fun writeProblem(ctx, status, title, detail) {
    ctx.response().setStatusCode(status)
        .putHeader("Content-Type", "application/problem+json")
        .end(JsonObject()
            .put("type", "about:blank")
            .put("title", title)
            .put("status", status)
            .apply { if (detail != null) put("detail", detail) }
            .encode())
}
```

Why? Because clients can parse a standard shape, and human readers can
scan one consistent body. Don't ship "raw stack traces"; don't ship
"empty body with 500".

## 8.5 NDJSON streaming

`handleStreamUsers` is the interesting one:

```kotlin
private suspend fun handleStreamUsers(ctx: RoutingContext) {
    val prefix = ctx.request().getParam("emailPrefix")
    val resp = ctx.response()
        .putHeader("Content-Type", "application/x-ndjson")
        .setChunked(true)
    users.streamAll(prefix).collect { user ->
        val line = JsonObject.mapFrom(user).encode() + "\n"
        if (resp.writeQueueFull()) {
            // back-pressure: suspend until the socket buffer drains
            coAwait(Future.future { p -> resp.drainHandler { p.complete() } })
        }
        resp.write(line)
    }
    resp.end()
}
```

Things to note:

- **`setChunked(true)`** sets Transfer-Encoding: chunked.
- **`writeQueueFull` + `drainHandler`** are Vert.x's back-pressure
  signals. When the kernel's send buffer is full, `writeQueueFull` is
  true; `drainHandler` fires when there's room again. We suspend on it
  via a small Future bridge — that suspension stops `collect` from
  pulling the next item from the Flow, which stops the channel from
  draining, which stops the PG cursor from fetching more rows.
- **No buffer of the whole result set.** Memory stays flat regardless
  of how many users there are.

NDJSON over a stream like this is the cheapest way to expose paged data
to a curl/jq pipeline. Pair with `pv -L` to throttle and watch the back
pressure.

## 8.6 Per-request metrics

Look at the second middleware in `mount`:

```kotlin
router.route().handler { ctx ->
    val started = System.nanoTime()
    ctx.addEndHandler {
        val elapsed = System.nanoTime() - started
        Metrics.registry.timer("http.request",
            "method", ctx.request().method().name(),
            "status", ctx.response().statusCode.toString()
        ).record(elapsed, TimeUnit.NANOSECONDS)
    }
    ctx.next()
}
```

`addEndHandler` fires whether the request ended with `end()`,
`failed()`, or a `close()`. So our timer is honest.

Chapter 16 wires this into Prometheus scrape. We expose
`/metrics` via `Metrics.registry.scrape()`.

## 8.7 Why not annotations?

You could:

```kotlin
@Get("/api/users/:id")
suspend fun getById(@PathParam id: Long): User = users.getById(id)
```

But: now you need a magical handler that reflects the function and
binds it to a router; you give up control over status codes, headers,
streaming. Vert.x Web's "router builder + plain handler" stays out of
your way and is precisely as small as you want.

We are not religious — if you want such a wrapper, write it on top of
`Routes.kt` and skip it where you need control. We prefer to start
small and add a wrapper *if* the repetition gets painful.

## 8.8 Exercises

1. Add a `/api/users/by-email?email=…` route. Reuse `findByEmail` on
   the service. Return 404 if not found. Get to <100 ms p99 under load
   with `hey -n 10000 -c 100`.
2. Stream the NDJSON endpoint to `localhost:9999` via `nc -l 9999`,
   then close `nc` after a few seconds. Inspect server memory — flat,
   right? Confirm via `jcmd` `GC.heap_info`.
3. Add request body size limiting. (Hint: `BodyHandler.create().setBodyLimit(1024*1024)`.)

---

[← Chapter 7](07-config-logging.md) · [Next → Chapter 9: PostgreSQL basics](09-postgresql-basics.md)
