# Chapter 15 — Interceptors, deadlines, cancellation, status codes

> You will add server-side interceptors for logging and auth, set client
> and server deadlines that propagate end-to-end, handle cancellation
> cleanly, and know which gRPC status to return when.

## 15.1 Vert.x gRPC "interceptors"

Vert.x 5 gRPC has **no interceptor SPI** — there is no `.interceptor {}`
method and no `ServerInterceptor` type. Cross-cutting concerns are done
with a plain `Handler<GrpcServerRequest>` registered via `callHandler`.
You register your typed services with `addService(...)` (that is what
`UsersGrpcService.of(impl).bind(server)` does under the hood), and you
register a *generic* `callHandler` for buffer-level calls to observe
every request. The mental model is constant: **inspect the request,
hang handlers off its response, with access to headers and status**.

A generic timing handler that fires for every call:

```kotlin
val grpcServer = GrpcServer.server(vertx)
grpcServer.callHandler { req ->                      // GrpcServerRequest<Buffer, Buffer>
    val started = System.nanoTime()
    val method  = req.fullMethodName()
    req.response().endHandler {
        val elapsed = System.nanoTime() - started
        Metrics.registry.timer(
            "grpc.request",
            "method", method,
        ).record(elapsed, TimeUnit.NANOSECONDS)
    }
    // delegate to the actual service handler — see below
}
```

`GrpcServerRequest` exposes `fullMethodName()`, `headers()` (a
`MultiMap` of the request metadata), `connection()`, `timeout()` and
`response()`. The `GrpcServerResponse` exposes `status(GrpcStatus)`,
`statusMessage(...)` and `trailers()` (there is no `headers()` on the
response — initial metadata is implicit; everything after the data goes
in trailers).

A logging handler that propagates a request id through trailers:

```kotlin
grpcServer.callHandler { req ->
    val reqId = req.headers().get("x-request-id")
        ?: java.util.UUID.randomUUID().toString()
    MDC.put("requestId", reqId)
    req.response().trailers().add("x-request-id", reqId)
    log.info("grpc-start {} from={}", req.fullMethodName(),
        req.connection().remoteAddress())
}
```

Because there is no interceptor chain, the practical pattern is to put
cross-cutting logic *inside* your service methods (you already have the
coroutine bridge there) or in a thin wrapping handler that ultimately
calls into the generated dispatch. Don't reach for a framework feature
that doesn't exist in 5.x.

## 15.2 Auth check

For a real service you'll want JWT validation in front of every call.
Read the token off the request headers, and on failure set the status
and `end()` the response (which short-circuits the call):

```kotlin
grpcServer.callHandler { req ->
    val auth  = req.headers().get("authorization")
    val token = auth?.removePrefix("Bearer ")?.trim()
    if (token.isNullOrEmpty()) {
        req.response().status(GrpcStatus.UNAUTHENTICATED).end()
        return@callHandler
    }
    val sub = jwt.verify(token)
    if (sub == null) {
        req.response().status(GrpcStatus.UNAUTHENTICATED).end()
        return@callHandler
    }
    // Subject verified.  There is no per-call String-keyed context map on
    // GrpcServerRequest in Vert.x 5 (the old `context().put("k", v)` API is
    // gone).  Propagate the subject by reading the header again inside the
    // service method, or via a typed `ContextLocal<String>` if you need it
    // on the verticle's Vert.x context.
}
```

These handlers run on the event loop. Avoid blocking calls (use a
non-blocking JWT verifier such as `nimbus-jose-jwt` with a JWKS cache,
async-fetched).

## 15.3 Deadlines

A **deadline** is "this RPC must complete by absolute time T". Clients
set them; servers honour them.

### Client-side

The generated typed stub (`client.getUser(req): Future<UserReply>`) does
not expose a per-call timeout, so for a deadline you drop to the
low-level `GrpcClient` request API, which returns a `GrpcClientRequest`
with a `timeout(long, TimeUnit)` setter:

```kotlin
val addr = SocketAddress.inetSocketAddress(9090, "localhost")
val req  = grpcClient.request(addr, UsersGrpcClient.GetUser).coAwait()
req.timeout(200, TimeUnit.MILLISECONDS)
val resp = req.send(GetUserRequest.newBuilder().setId(1).build()).coAwait()
val reply = resp.last().coAwait()   // unary: single message then end
```

Vert.x maps the timeout to the standard `grpc-timeout` header. If the
server doesn't return in time, the client's Future fails (surfaced as an
`InvalidStatusException` with `actualStatus() == DEADLINE_EXCEEDED`) and
the stream is cancelled.

### Server-side

The framework surfaces the remaining time as `request.timeout()` on the
`GrpcServerRequest` — a `long` in milliseconds, `0` when the client set
no deadline. (Recall the corrected service builds its `Future` with
`vertxFuture(vertx, scope) { ... }`.) Honour the budget with a coroutine
timeout:

```kotlin
override fun getUser(request: GetUserRequest): Future<UserReply> = vertxFuture(vertx, scope) {
    val budgetMs = serverRequest.timeout().takeIf { it > 0 } ?: 200L
    withTimeout(budgetMs) {
        users.getById(request.id).toReply()
    }
}
```

`withTimeout` throws `TimeoutCancellationException` if elapsed; map it
to `GrpcStatus.DEADLINE_EXCEEDED` via `StatusException`. (To reach the
`GrpcServerRequest` from inside a generated method you keep a reference
to it; the generated abstract method only hands you the decoded request
message.)

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
