# Chapter 15 — Interceptors, deadlines, cancellation, status codes

> You will add server-side interceptors for logging and auth, set client
> and server deadlines that propagate end-to-end, handle cancellation
> cleanly, and know which gRPC status to return when.

## 15.1 Vert.x gRPC interceptors

`GrpcServer` lets you register handlers that wrap *every* call. There is
no "AOP" annotation — it's a plain handler. Two pieces:

```kotlin
val grpcServer = GrpcServer.server(vertx)
    .interceptor { call ->
        val started = System.nanoTime()
        val method  = call.methodName().fullMethodName()
        call.response().endHandler {
            val elapsed = System.nanoTime() - started
            Metrics.registry.timer(
                "grpc.request",
                "method", method,
                "status", call.response().status().toString()
            ).record(elapsed, TimeUnit.NANOSECONDS)
        }
    }
```

Add a logging interceptor:

```kotlin
.interceptor { call ->
    val reqId = call.headers().get("x-request-id")
        ?: java.util.UUID.randomUUID().toString()
    MDC.put("requestId", reqId)
    call.response().headers().add("x-request-id", reqId)
    log.info("grpc-start {} from={}", call.methodName().fullMethodName(),
        call.connection().remoteAddress())
}
```

The exact API for "GrpcServer interceptor" varies a little between
vertx-grpc 5.x point releases (some expose `addService`, some
`callHandler`, some a `serverInterceptor` Builder). The mental model is
constant: **wrap every call, before/after, with access to headers and
status**.

## 15.2 Auth interceptor

For a real service you'll want JWT validation in front of every call:

```kotlin
.interceptor { call ->
    val auth = call.headers().get("authorization")
    val token = auth?.removePrefix("Bearer ")?.trim()
    if (token.isNullOrEmpty()) {
        call.response().status(GrpcStatus.UNAUTHENTICATED).end()
        return@interceptor
    }
    val sub = jwt.verify(token) ?: run {
        call.response().status(GrpcStatus.UNAUTHENTICATED).end()
        return@interceptor
    }
    // Make subject available to the handler via call context
    call.context().put("userId", sub)
}
```

Interceptors run on the event loop. Avoid blocking calls (use a
non-blocking JWT verifier such as `nimbus-jose-jwt` with a JWKS cache,
async-fetched).

## 15.3 Deadlines

A **deadline** is "this RPC must complete by absolute time T". Clients
set them; servers honour them.

### Client-side

```kotlin
client.call(method)
    .deadline(System.currentTimeMillis() + 200)
    .execute(req)
```

If the server doesn't return by deadline, the client gets
`DEADLINE_EXCEEDED` and cancels the stream.

### Server-side

The framework gives you the deadline via the call. Honour it by setting
a coroutine timeout:

```kotlin
override fun getUser(request: GetUserRequest): Future<UserReply> = vertxFuture {
    withTimeout(remainingMillisOrDefault(200)) {
        users.getById(request.id).toReply()
    }
}
```

`withTimeout` throws `TimeoutCancellationException` if elapsed; map it
to `GrpcStatus.DEADLINE_EXCEEDED`.

Deadlines propagate naturally **through coroutines**: if A awaits B, and
A's timeout fires, B is cancelled at its next suspension point.

## 15.4 Cancellation

Cancellation comes from three sources:

1. **Client cancel** (RST_STREAM). Server's coroutine sees a
   `CancellationException` at the next suspension.
2. **Deadline.** Same as cancel, but the exception is
   `TimeoutCancellationException`.
3. **Server shutdown.** Verticle scope cancelled in `stop()`.

In all three, the in-flight code unwinds, `finally` blocks run, the
PG connection returns to the pool, the channel closes, the stream
ends. Coroutines do the right thing if you don't fight them.

The only rule: **don't swallow `CancellationException`**. If you must
log it, log and re-throw:

```kotlin
try { work() } catch (e: CancellationException) { log.info("cancelled"); throw e }
```

## 15.5 Status code cheat sheet

| Status                | Use when                                                |
|-----------------------|---------------------------------------------------------|
| `OK`                  | success                                                 |
| `INVALID_ARGUMENT`    | client error: bad payload                               |
| `NOT_FOUND`           | the named resource doesn't exist                        |
| `ALREADY_EXISTS`      | resource cannot be created because it already exists    |
| `PERMISSION_DENIED`   | auth'd but not allowed                                  |
| `UNAUTHENTICATED`     | no/invalid credentials                                  |
| `RESOURCE_EXHAUSTED`  | rate limited, quota                                     |
| `FAILED_PRECONDITION` | state guard fails (account inactive)                    |
| `ABORTED`             | transaction abort, conflict                             |
| `OUT_OF_RANGE`        | page > total                                            |
| `UNIMPLEMENTED`       | method not on this server                               |
| `UNAVAILABLE`         | transient: client should retry                          |
| `DATA_LOSS`           | irrecoverable                                           |
| `DEADLINE_EXCEEDED`   | timeout                                                 |
| `CANCELLED`           | client cancelled                                        |
| `INTERNAL`            | bug: should not happen                                  |

Pick precisely. Clients use these to decide retry policy. Wrong code →
wrong retry → cascade failures.

## 15.6 Retry policy (client side)

A reasonable client retries `UNAVAILABLE` and (carefully) `ABORTED`
with backoff + jitter. *Never* retry `INTERNAL` blindly — you can't
know if the work happened.

For idempotent calls, retry is safe. For non-idempotent (create
without idempotency key), retries can dual-insert. Two patterns:

- **Idempotency key header.** Server records and rejects duplicates.
- **`ON CONFLICT DO NOTHING` upserts.** Cheaper, requires
  natural-key column.

## 15.7 Header / trailer plumbing

gRPC has both initial headers and trailers (set at end-of-stream). Use
trailers for:

- `grpc-status` and `grpc-message` (framework sets these).
- Custom diagnostic fields you want to send *after* the data.
- Pagination cursors when end-of-stream marks completion.

Headers (initial) are for things known up front: auth, content-type
overrides, request-id propagation.

## 15.8 Compression

gRPC supports per-message compression with negotiation. Vert.x gRPC
enables gzip on the server when the client requests it. For high-volume
text payloads (JSON-ish protos), enable it; for tiny messages, the
overhead can hurt latency. Measure.

## 15.9 Exercises

1. Add a deadline propagation test: client sets 100 ms, server delays
   200 ms. Server should see cancellation; client should see
   `DEADLINE_EXCEEDED`.
2. Implement a simple rate-limit interceptor (per-IP token bucket) that
   returns `RESOURCE_EXHAUSTED`.
3. Add an idempotency-key interceptor that stores the key + response
   pair in PG and returns the cached reply on retry.

---

[← Chapter 14](14-grpc-bidi.md) · [Next → Chapter 16: Observability](16-observability.md)
