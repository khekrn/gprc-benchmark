# Chapter 14 — Client streaming & bidirectional streaming

> You will implement `ImportUsers` (client streaming) and `Chat`
> (bidi streaming), understand half-close semantics, and apply
> backpressure on both directions.

## 14.1 Client streaming: the wire shape

```proto
rpc ImportUsers(stream CreateUserRequest) returns (ImportSummary);
```

The client opens an HTTP/2 stream, sends N requests, then "half-closes"
(sends an `END_STREAM` on its half). The server reads to end-of-stream
and replies with a single `ImportSummary`, then closes the response.

```
Client                        Server
  │   HEADERS, then N×DATA
  ├──────────────────────────►
  │     (client END_STREAM)
  ├──────────────────────────►
  │                            ── processes ──
  │     DATA(ImportSummary)
  │◄──────────────────────────┤
  │     HEADERS grpc-status:0 END_STREAM
  │◄──────────────────────────┤
```

The generator gives us:

```kotlin
fun importUsers(request: ReadStream<CreateUserRequest>): Future<ImportSummary>
```

We **receive** a Vert.x `ReadStream` and **return** a `Future`.

## 14.2 Implementation

```kotlin
override fun importUsers(request: ReadStream<CreateUserRequest>): Future<ImportSummary> = vertxFuture(vertx, scope) {
    val inbox = java.util.concurrent.ConcurrentLinkedQueue<CreateUserRequest>()
    Future.future<Void> { p ->
        request.handler { req -> inbox.add(req) }
        request.endHandler { p.complete() }
        request.exceptionHandler { t -> p.fail(t) }
    }.coAwait()    // suspend until client sends its half-close

    val imported = AtomicLong()
    val skipped  = AtomicLong()
    val errors   = mutableListOf<String>()
    for (req in inbox) {
        try {
            users.create(NewUser(req.email, req.fullName))
            imported.incrementAndGet()
        } catch (e: UserError.DuplicateEmail) {
            skipped.incrementAndGet()
        } catch (t: Throwable) {
            skipped.incrementAndGet()
            errors.add(t.message ?: "unknown")
        }
    }
    ImportSummary.newBuilder()
        .setImported(imported.get())
        .setSkipped(skipped.get())
        .addAllErrors(errors)
        .build()
}
```

Why a queue? The `handler { }` callback fires on the event loop
synchronously as bytes arrive. Inside that callback we can't easily
`coAwait()` a long DB insert without quickly losing backpressure
correctness. We buffer first, then process. This is *fine* for small
batches; for batches in the millions you would use a `Channel` and
process *while* inbound bytes arrive — see exercise 1.

### A back-pressured streaming variant (preview)

```kotlin
override fun importUsers(request: ReadStream<CreateUserRequest>): Future<ImportSummary> = vertxFuture(vertx, scope) {
    val ch = Channel<CreateUserRequest>(Channel.RENDEZVOUS)
    request.handler { req ->
        val ok = ch.trySend(req).isSuccess
        if (!ok) request.pause()
    }
    request.endHandler { ch.close() }
    request.exceptionHandler { t -> ch.close(t) }
    val imported = AtomicLong()
    val skipped  = AtomicLong()
    for (req in ch) {
        try {
            users.create(NewUser(req.email, req.fullName))
            imported.incrementAndGet()
            // Resume reading when we know the consumer is free
            request.fetch(1)
        } catch (e: UserError.DuplicateEmail) {
            skipped.incrementAndGet()
        }
    }
    ImportSummary.newBuilder()
        .setImported(imported.get())
        .setSkipped(skipped.get())
        .build()
}
```

Now we apply *real* backpressure to the client: if our DB is slow, we
stop reading and the HTTP/2 receive window tightens.

## 14.3 Calling client streaming with `grpcurl`

```bash
cat <<EOF | grpcurl -plaintext -d @ \
    localhost:9090 com.example.app.grpc.Users/ImportUsers
{"email":"a@x.io","fullName":"A"}
{"email":"b@x.io","fullName":"B"}
{"email":"c@x.io","fullName":"C"}
EOF
```

`-d @` reads newline-delimited JSON messages from stdin.

## 14.4 Bidirectional streaming: `Chat`

```proto
rpc Chat(stream ChatMessage) returns (stream ChatMessage);
```

Both directions independent. Each side can send as fast or slow as it
wants. Examples in the wild:

- Real-time chat.
- Live inference (client sends frames, server returns predictions).
- Real-time control plane.

### Implementation

Like server streaming, the bidi override returns **`Unit`** and writes to a
`WriteStream`. For a stateless echo we don't even need a coroutine — plain
stream handlers suffice, and event-loop ordering guarantees in-order writes:

```kotlin
override fun chat(
    request: ReadStream<ChatMessage>,
    response: WriteStream<ChatMessage>,
) {
    request.handler { msg ->
        val reply = ChatMessage.newBuilder()
            .setFrom("server")
            .setText("echo: ${msg.text}")
            .setTsMillis(System.currentTimeMillis())
            .build()
        response.write(reply)
    }
    request.endHandler { response.end() }
    request.exceptionHandler { t ->
        log.warn("chat error", t)
        (response as? GrpcServerResponse<*, *>)?.status(GrpcStatus.INTERNAL)?.end()
    }
}
```

Walk through:

- We don't queue inbound — for echo, we can write the response on the
  spot. The event-loop ordering guarantees in-order writes.
- `request.endHandler { response.end() }` mirrors the client's
  half-close, sending the final `grpc-status: 0`.
- On a stream error we end the response with a non-OK status (the
  `response` is a `GrpcServerResponse` at runtime). If you needed to suspend
  inside the handler — e.g. to persist each message — you would `scope.launch { }`
  the body and `coAwait()` inside, exactly as `listUsers` does.

For chat, you might fan out across many subscribers via a Vert.x event
bus address or a `SharedFlow`. For our demo, the single-pair echo
demonstrates the wire mechanics.

## 14.5 Backpressure on both sides

We have not added a `writeQueueFull` check on `response.write` here.
For a slow client, the gRPC framer will buffer some `UserReply` bytes
in Netty's outbound buffer. Add the same `drainHandler`-await pattern
from Chapter 13 for production code with large messages or a
high-fanout server.

## 14.6 Calling bidi from `grpcurl`

```bash
grpcurl -plaintext -d @ \
    localhost:9090 com.example.app.grpc.Users/Chat <<EOF
{"from":"alice","text":"hi"}
{"from":"alice","text":"how are you"}
EOF
```

Each line in flicks back an `echo: ...` reply before the next is sent.

## 14.7 Cancellation semantics

In bidi, either side can cancel by sending `RST_STREAM`. On the server,
`request.exceptionHandler` fires; we end the response with a non-OK
status (and any in-flight coroutine work is cancelled with the scope).

On the client (Vert.x gRPC), call `request.cancel()` to send the RST.
On the server, `request.exceptionHandler` fires and we end the response;
for a coroutine-driven handler the launched job is cancelled when the
service `scope` is cancelled on shutdown.

Use cancellation for **client timeouts** and **idle streams**. Don't
rely on it for "I changed my mind" — design protocols so clients can
naturally end their half.

## 14.8 Exercises

1. Convert `importUsers` to the streaming version (use the channel
   pattern). Compare memory under a 100 k-row push.
2. Modify `chat` to broadcast every message to all currently connected
   sessions (use a `SharedFlow<ChatMessage>` shared across calls).
3. Write a client that opens two bidi streams concurrently and
   verifies they're independent (no cross-talk).

---

[← Chapter 13](13-grpc-server-streaming.md) · [Next → Chapter 15: Interceptors, deadlines, errors](15-grpc-interceptors.md)
