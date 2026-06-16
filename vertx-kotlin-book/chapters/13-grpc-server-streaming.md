# Chapter 13 — Server-streaming RPC

> By the end of this chapter you will understand the wire shape of a
> server-streaming RPC, implement `ListUsers` with backpressure that
> reaches into Postgres, and call it from `grpcurl` and Kotlin.

## 13.1 What does "stream" mean over HTTP/2?

A gRPC stream is just an **HTTP/2 stream**. The client opens it, sends
one request body, then reads N response messages framed as length-prefixed
protobufs over `DATA` frames. The server ends with a `HEADERS` frame
containing `grpc-status: 0` (or another code) and `END_STREAM`.

```
Client                        Server
  │   HEADERS (req method)
  ├──────────────────────────►
  │   DATA (ListUsersRequest)
  ├──────────────────────────►
  │
  │     DATA (UserReply 1)
  │◄──────────────────────────┤
  │     DATA (UserReply 2)
  │◄──────────────────────────┤
  │     ...
  │     HEADERS grpc-status:0 END_STREAM
  │◄──────────────────────────┤
```

HTTP/2 flow control means the server **can be told to slow down** by the
client's `WINDOW_UPDATE` frames. Netty surfaces this as
`channel.isWritable` / `writeQueueFull`. Vert.x surfaces it on the
gRPC response as `writeQueueFull` and `drainHandler`. Same back-pressure
plumbing as a chunked HTTP response.

## 13.2 The contract

```proto
rpc ListUsers (ListUsersRequest) returns (stream UserReply);
```

Single request in, many replies out. Generated Kotlin signature on
`UsersService`:

```kotlin
fun listUsers(
    request: ListUsersRequest,
    response: WriteStream<UserReply>,
): Future<Void>
```

`WriteStream` is the standard Vert.x sink with `write`, `end`,
`writeQueueFull`, `drainHandler`. Returns `Future<Void>` so the
framework knows when we're done writing.

## 13.3 Implementation

```kotlin
override fun listUsers(
    request: ListUsersRequest,
    response: WriteStream<UserReply>,
): Future<Void> = vertxFuture {
    var n = 0L
    try {
        users.streamAll(request.emailPrefix.ifBlank { null }).collect { u ->
            if (response.writeQueueFull()) {
                Future.future<Void> { p -> response.drainHandler { p.complete() } }.coAwait()
            }
            response.write(u.toReply())
            n++
        }
        response.end().coAwait()
        log.debug("listUsers sent {} rows", n)
        null
    } catch (t: Throwable) {
        log.error("listUsers failed after {} rows", n, t)
        throw t
    }
}
```

Step by step:

1. **`users.streamAll(prefix)`** returns a cold `Flow<User>` backed by a
   PG cursor.
2. **`.collect { u -> ... }`** pulls one user at a time. The coroutine
   suspends if the channel is empty (PG hasn't delivered the next
   batch).
3. **`response.writeQueueFull()`** is HTTP/2 flow control telling us
   the peer's window is closed. We suspend on `drainHandler` until the
   client `WINDOW_UPDATE`s.
4. **`response.write(u.toReply())`** writes one reply.
5. **`response.end().coAwait()`** sends the final `HEADERS` with
   `grpc-status: 0`.

Why this matters: the *entire* chain — from PG socket through the
driver, through our channel-backed Flow, through the gRPC framer, out
to TCP — applies backpressure. The slow consumer is the load on the
fast database, no buffer ever explodes.

## 13.4 Calling it

### grpcurl

```bash
grpcurl -plaintext -d '{}' \
    localhost:9090 com.example.app.grpc.Users/ListUsers
```

Streams JSON one line at a time. With `-format=text`:

```bash
grpcurl -plaintext -format text -d '' \
    localhost:9090 com.example.app.grpc.Users/ListUsers
```

To see backpressure in action, throttle the client:

```bash
grpcurl -plaintext -d '{}' \
    localhost:9090 com.example.app.grpc.Users/ListUsers | pv -L 1k > /dev/null
```

In another terminal watch `pg_stat_activity` — you'll see the query
`State: active` but no CPU spike on Postgres because it's parked
waiting for the network.

### Kotlin client

```kotlin
val client = UsersGrpcClient.create(GrpcClient.client(vertx), socketAddr)
client.listUsers(ListUsersRequest.newBuilder().build()).onSuccess { stream ->
    stream.handler { u -> println("${u.id} ${u.email}") }
    stream.endHandler { println("done") }
    stream.exceptionHandler { t -> println("err: $t") }
}
```

Or, more idiomatically, adapt the inbound stream to a Flow with our
`asFlow` extension and collect.

## 13.5 Why not just return `List<UserReply>` and `RepeatedReply`?

You could put `repeated UserReply users = 1;` in a single message and
return it from a unary RPC. Two problems:

1. **Memory.** The server materialises the whole list. So does the
   client. Memory cost scales with row count.
2. **Time to first byte.** The client waits for the whole list. For a
   slow-starting query that's seconds of latency.

Server streaming gives you O(1) memory and TTFB ≈ first row latency.

Use repeated fields when you have a *known small* count (page of 50).
Use streaming when N is open-ended.

## 13.6 Common bugs

- **Forgetting `response.end()`.** The client hangs. There's no
  timeout by default.
- **Throwing without `response.end()`.** The framework sees the failed
  Future and writes `grpc-status: INTERNAL` for you — fine for us, but
  log the cause server-side.
- **Writing on `writeQueueFull = true`.** Vert.x buffers internally
  until you exceed `setWriteQueueMaxSize`. Not crash but heap grows.
  Always check.
- **Pulling from the Flow without ever yielding to the event loop.**
  Won't happen with `Flow.collect` because every `await` is a
  suspension point.

## 13.7 Cancellation

If the client disconnects, the gRPC stream is cancelled and our
collector eventually fails with a `CancellationException` on the next
`write`. Because we're inside `vertxFuture`, the exception bubbles
up and the framework completes the response Future with the failure.
The Flow's collector exits; its `invokeOnClose` closes the PG cursor;
the connection returns to the pool. Clean.

## 13.8 Exercises

1. Add a `delay(20)` between writes. Run with `grpcurl … | pv -L 1k`.
   The server is no longer CPU-bound. Confirm the PG query is paused
   (no rows consumed).
2. Add a `maxRows` request field. Honour it server-side; treat
   `maxRows == 0` as "no limit". Add a test.
3. Modify `streamAll` to NOT call `stream.pause()` on `trySend` fail.
   Re-run with throttled client. Memory grows. Why exactly?

---

[← Chapter 12](12-grpc-unary.md) · [Next → Chapter 14: Client streaming & bidi](14-grpc-bidi.md)
