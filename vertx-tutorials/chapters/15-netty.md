# Chapter 15 — Netty Deep Dive

> **You should finish this chapter able to:** explain how Netty's
> EventLoop / Channel / Pipeline / ChannelHandler / ByteBuf compose, navigate
> the inbound vs outbound pipeline, write a custom decoder/encoder, manage
> ByteBuf reference counts, configure native transports, and understand
> exactly what Vert.x is doing on top of Netty.

Vert.x is a *thin* abstraction over Netty. Most of the time you don't need
to think about Netty — Vert.x APIs are sufficient. But understanding what
sits beneath you pays off when:

- You need a custom protocol (binary, line-based, length-prefixed).
- You hit memory leak warnings (`LEAK: ByteBuf.release() was not called...`).
- You need to optimize the very hottest paths.
- You build a driver from scratch (chapter 16).

## 15.1  Netty's mental model

```
       Channel  ←─ wraps a socket (or pipe, or local endpoint)
          │
          ├── ChannelPipeline  ←─ ordered list of ChannelHandlers
          │       │
          │       └── ChannelHandler N
          │           ChannelHandler N-1
          │           ...
          │           ChannelHandler 1
          │
          ├── ChannelConfig    ←─ socket options
          └── EventLoop        ←─ the thread serving this channel
```

Concretely, when bytes arrive on a TCP socket:

1. The OS notifies via `epoll_wait` (or selector).
2. The `EventLoop` thread reads bytes into a `ByteBuf`.
3. Bytes are pushed *up* the pipeline (inbound), each handler decoding /
   transforming as needed.
4. The terminal inbound handler (e.g., `HttpServerHandler`) invokes user
   code (your Vert.x handler).
5. Your code writes a response — that propagates *down* the pipeline
   (outbound), each handler encoding / framing as needed.
6. The terminal outbound handler writes bytes to the socket.

## 15.2  The pipeline — inbound and outbound

```
       Inbound (read)                  Outbound (write)
        ┌──────┐                          ┌──────┐
        │ tail │  ← head of pipeline      │ head │
        └──┬───┘                          └──┬───┘
           │                                  │
   bytes  │                                  │  bytes
   from   ▼                                  ▼   to
   socket │ HttpRequestDecoder    HttpResponseEncoder
         ▼ HttpObjectAggregator   ...
         ▼ HttpServerHandler  ←→  HttpServerHandler   ← endpoints meet here
         ▼ YourHandler (read)  YourHandler (write)
```

Note: a single `ChannelHandler` can be inbound, outbound, or both. The base
classes are:

- `ChannelInboundHandlerAdapter` for read-side.
- `ChannelOutboundHandlerAdapter` for write-side.
- `ChannelDuplexHandler` for both.

Vert.x's HTTP server has roughly this pipeline:

```
inbound:  HttpRequestDecoder → HttpObjectAggregator → ChunkedWriteHandler → Vert.x request handler
outbound: HttpResponseEncoder ← (same handlers in reverse)
```

## 15.3  `ByteBuf` — Netty's buffer

`java.nio.ByteBuffer` has two big sins: positional reads vs writes share
state (you must `flip()`), and it's allocated either on heap (slow for
zero-copy) or off-heap (manual lifecycle). `ByteBuf` fixes both:

```java
ByteBuf buf = ByteBufAllocator.DEFAULT.buffer(64);
buf.writeInt(42);
buf.writeBytes("hello".getBytes(StandardCharsets.UTF_8));

// Read side has independent reader index
int n = buf.readInt();
byte[] hello = new byte[5];
buf.readBytes(hello);

buf.release();    // ← REFERENCE COUNTING
```

Key properties:

- **Reader index / writer index** are independent: no flip().
- **Reference counted.** `retain()` increments, `release()` decrements,
  `release()` on count 0 frees the buffer.
- **Pooled allocator by default.** `ByteBufAllocator.DEFAULT` is a pooled
  allocator that reuses freed buffers. Fast.
- **Direct vs heap.** Direct buffers live outside the Java heap. Faster
  for zero-copy I/O; cost is manual lifecycle.

### Reference counting in practice

The most common Netty bug: forgetting to `release()`. The Netty leak
detector logs warnings like:

```
LEAK: ByteBuf.release() was not called before it's garbage-collected.
```

Rules:

- **If you read it, you own it.** Default behavior: an inbound handler that
  forwards the buf via `ctx.fireChannelRead(buf)` does *not* need to release;
  the *next* handler is responsible.
- **If you stop reading, you must release.** A terminating handler must
  `release()` or use `ReferenceCountUtil.release(msg)`.
- **If you write, the framework releases for you.** `ctx.write(buf)` causes
  the framework to release after the bytes are flushed.

Vert.x's HTTP handlers manage this for you. **You only deal with refcounting
if you write custom Netty handlers.**

### `Unpooled` for one-off buffers

```java
ByteBuf buf = Unpooled.copiedBuffer("hi", StandardCharsets.UTF_8);
```

`Unpooled` skips the pool — slower but you don't have to worry about
contaminating the pool with corrupt state on a bug.

## 15.4  Channels

A `Channel` wraps an OS socket-like resource:

- `NioSocketChannel`, `NioServerSocketChannel` — portable NIO selector-based.
- `EpollSocketChannel`, `EpollServerSocketChannel` — Linux native, faster.
- `KQueueSocketChannel`, `KQueueServerSocketChannel` — macOS/BSD native.
- `IOUringSocketChannel` — Linux 5.1+ native io_uring.
- `LocalChannel` — in-JVM bidirectional channel (for unit tests).

Vert.x's `setPreferNativeTransport(true)` picks the right native channel
based on what's on the classpath. Default fallback: NIO.

Each `Channel` is bound to an `EventLoop` for its lifetime. **All handlers
for that channel run on that one thread**, which is why we say "single-
threaded" inside a verticle's Context.

## 15.5  EventLoopGroup — the threading model

```java
// One acceptor group (1 thread) + one worker group (N threads)
EventLoopGroup boss = new EpollEventLoopGroup(1);
EventLoopGroup workers = new EpollEventLoopGroup();

ServerBootstrap b = new ServerBootstrap();
b.group(boss, workers)
 .channel(EpollServerSocketChannel.class)
 .childHandler(new ChannelInitializer<>() { ... });
```

- **`boss`** runs the `accept()` calls.
- **`workers`** run reads/writes for accepted connections.

Vert.x simplifies: it has one `EventLoopGroup` of size `2 × cores`, used for
both. There's no separate "boss group" because the OS-level accept is fast
enough on a worker thread.

## 15.6  Writing a custom decoder

Let's build a simple length-prefixed protocol decoder. The framing:

```
| 4-byte length (BE) | length bytes of payload | next message...
```

```kotlin
import io.netty.buffer.ByteBuf
import io.netty.channel.ChannelHandlerContext
import io.netty.handler.codec.ByteToMessageDecoder

class LengthPrefixedDecoder : ByteToMessageDecoder() {
    override fun decode(ctx: ChannelHandlerContext, input: ByteBuf, out: MutableList<Any>) {
        // 1. Need at least 4 bytes for the length
        if (input.readableBytes() < 4) return

        // 2. Peek at the length without advancing reader index
        input.markReaderIndex()
        val length = input.readInt()
        if (length < 0 || length > MAX_FRAME) {
            ctx.close()
            return
        }

        // 3. If full payload isn't here yet, rewind and wait
        if (input.readableBytes() < length) {
            input.resetReaderIndex()
            return
        }

        // 4. Slice the payload (zero-copy view)
        val payload = input.readSlice(length).retain()
        out.add(payload)
    }

    companion object { const val MAX_FRAME = 16 * 1024 * 1024 }
}
```

Key points:

- `ByteToMessageDecoder` buffers partial messages between calls.
- `readSlice(length)` returns a view of the original buffer — zero-copy.
  Must `retain()` because the slice shares the parent's refcount.
- `out.add(payload)` passes the payload to the next inbound handler.

### Pairing encoder

```kotlin
class LengthPrefixedEncoder : MessageToByteEncoder<ByteBuf>() {
    override fun encode(ctx: ChannelHandlerContext, msg: ByteBuf, out: ByteBuf) {
        out.writeInt(msg.readableBytes())
        out.writeBytes(msg)
    }
}
```

Plug into a pipeline:

```kotlin
val pipeline = ch.pipeline()
pipeline.addLast(LengthPrefixedDecoder())
pipeline.addLast(LengthPrefixedEncoder())
pipeline.addLast(MyAppHandler())
```

## 15.7  Accessing Netty from Vert.x

Sometimes you need to install a custom handler in front of Vert.x's pipeline.
`HttpServer.connectionHandler` gives you the underlying `Channel`:

```kotlin
vertx.createHttpServer(opts)
    .connectionHandler { conn ->
        val ch = (conn as HttpConnectionInternal).channel()
        ch.pipeline().addFirst("audit", AuditHandler())
    }
    .requestHandler(router)
```

But this is rare. 95% of needs are met by Vert.x APIs. Use Netty handlers
only when:

- You need a custom non-HTTP protocol (then build a Vert.x driver — chapter 16).
- You need pipeline-level metrics (latency at the syscall boundary).
- You need TLS engine customization not exposed by `SSLEngineOptions`.

## 15.8  ChannelOptions and socket-level tuning

```kotlin
HttpServerOptions()
    .setReceiveBufferSize(64 * 1024)
    .setSendBufferSize(64 * 1024)
    .setSoLinger(0)
    .setTcpNoDelay(true)
    .setTcpKeepAlive(true)
    .setAcceptBacklog(1024)
```

Underneath, Vert.x translates these to Netty `ChannelOption.SO_*`. They map
to standard POSIX socket options.

| Option | What it does |
|---|---|
| `SO_RCVBUF` / `SO_SNDBUF` | OS-side socket buffer sizes. Default 16-64 KB; bigger for bulk transfers. |
| `SO_LINGER=0` | On close, immediately RST instead of TIME_WAIT. Use carefully. |
| `TCP_NODELAY` | Disable Nagle. Always on for low-latency RPC. |
| `SO_KEEPALIVE` | OS-level dead-connection detection. Usually on. |
| `SO_BACKLOG` | Pending-accept queue depth. Raise to 1024+ for bursty traffic. |

## 15.9  Native transports — what changes

With `EpollServerSocketChannel`, you also get:

- **`EpollChannelOption.SO_REUSEPORT`** — multiple sockets bound to the same
  port; kernel hashes incoming SYNs across them.
- **`EpollChannelOption.TCP_FASTOPEN`** — saves 1 RTT for repeat clients.
- **Edge-triggered epoll** — fewer redundant wake-ups than NIO's level-triggered.

For io_uring (Linux 5.1+, Netty 4.2+ experimental):

```xml
<dependency>
  <groupId>io.netty.incubator</groupId>
  <artifactId>netty-incubator-transport-native-io_uring</artifactId>
</dependency>
```

io_uring eliminates most syscalls by batching ops in a ring buffer shared
with the kernel. Real-world win: 10-15% on syscall-bound paths.

## 15.10  Direct-memory and `Pooled` allocator

```java
ByteBufAllocator.DEFAULT     // default; usually PooledByteBufAllocator
PooledByteBufAllocator.DEFAULT
UnpooledByteBufAllocator.DEFAULT
```

The pooled allocator maintains thread-local arenas of off-heap memory. When
you `release()` a buffer, it goes back to the arena. Result: virtually no
GC pressure even at millions of allocations per second.

Vert.x defaults to `PooledByteBufAllocator`. You can verify:

```kotlin
log.info("allocator: ${ByteBufAllocator.DEFAULT::class.java.simpleName}")
```

If you ever need to debug allocation:

```
-Dio.netty.leakDetectionLevel=PARANOID
```

Reports a leak the moment a buffer is dropped without `release()`. Slow,
but invaluable when chasing leaks.

## 15.11  Vert.x's pipeline (annotated)

For a Vert.x HTTP/1.1 server, the pipeline looks roughly like:

```
NioSocketChannel
   ↓
SslHandler                       (if TLS)
HttpServerCodec                  (decode/encode HTTP/1.1 messages)
HttpServerKeepAliveHandler       (Connection: keep-alive)
IdleStateHandler                 (close on inactivity)
HandlerCollection                (chunking, expect-continue, decompression)
   ↓
VertxHttp1ServerConnection       (terminal; calls user `requestHandler`)
```

For HTTP/2:

```
NioSocketChannel
   ↓
SslHandler + ALPN
Http2FrameCodec
Http2MultiplexHandler            (one virtual channel per stream)
   ↓ per stream
VertxHttp2ServerConnection
```

You can inspect any channel's pipeline at runtime:

```kotlin
log.info("pipeline: {}", channel.pipeline().names())
```

Useful when debugging "why isn't gzip happening" or "where does TLS slot in".

## 15.12  Performance: avoid these patterns

**Allocating a `Buffer.buffer()` per request when a static would do.**
JIT can sometimes elide it; counting on it is risky.

**Blocking inside a handler.** A custom Netty handler runs on the event
loop — same rules as Vert.x. No JDBC calls!

**Synchronizing on the Channel.** Don't. Each Channel belongs to one
event-loop thread; lock-free by construction.

**Catching `Throwable` and not releasing on the exception path.** If a buf
arrives, you start work, then throw before forwarding — `finally { buf.release() }`
or you leak.

**Large `ByteBufHolder` chains in memory.** When proxying, drain
output back-pressure-aware: write, wait for `Channel.isWritable() == true`,
write again. Don't queue arbitrarily.

## 15.13  Debugging tips

- **`-Dio.netty.leakDetectionLevel=PARANOID`** — finds leaks fast.
- **`ChannelPipeline.toString()`** prints names + classes.
- **`channel.eventLoop().inEventLoop()`** — handy assertion in tests.
- **`Channel.isActive()`** — true if connected.
- **`Channel.unsafe()`** — internal; only for tooling, never user code.

## 15.14  Why this matters for Vert.x users

You don't have to write Netty code to use Vert.x. But:

1. When you see a `LEAK` warning, you know what it means and where to look.
2. When you need a non-HTTP protocol (chapter 16 builds DynamoDB), the Netty
   pieces are familiar.
3. When you read Vert.x source code (and you should!), the Netty layer
   becomes legible.
4. When you tune performance, you understand what `setReusePort(true)` is
   actually doing.

## 15.15  Try it

1. Add `-Dio.netty.leakDetectionLevel=PARANOID` to `JAVA_OPTS` in dev and
   run a load test. Verify no leaks. If any appear, trace them.
2. Write a small Netty server (no Vert.x) using
   `ServerBootstrap` + `LengthPrefixedDecoder/Encoder`. Test it with `nc`.
3. Inspect the pipeline of a running Vert.x HTTP server using
   `connectionHandler` and log its handler names. Compare HTTP/1.1 vs HTTP/2.

[← Ch 14](14-performance.md) · [Next: Chapter 16 — Build an async DynamoDB driver →](16-dynamodb-driver.md)
