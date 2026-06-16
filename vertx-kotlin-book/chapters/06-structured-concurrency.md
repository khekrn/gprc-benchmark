# Chapter 6 — Structured concurrency, channels, flows

> By the end of this chapter you will have a working mental model for
> *scope*, will know when to reach for `Channel` vs `Flow` vs
> `SharedFlow`, and will be able to wire **end-to-end back-pressure**
> from a Postgres cursor through a gRPC server stream to a slow client.

## 6.1 Scopes: the parent owns the children

Every coroutine runs inside a **CoroutineScope**. A scope has a `Job`
that *owns* all coroutines started inside it. Cancel the scope's job →
all children are cancelled.

```
┌──── verticleScope (parent Job) ────────────────────────────┐
│                                                            │
│   start() ──── launch { … }   ◄─ child 1                   │
│         └──── launch { … }    ◄─ child 2                   │
│                  └── async { } ◄─ grandchild               │
│                                                            │
│   stop() → verticleScope.coroutineContext[Job].cancel()    │
│            → child 1, child 2, grandchild all cancelled    │
└────────────────────────────────────────────────────────────┘
```

`CoroutineVerticle` gives you a scope whose Job is cancelled on
`stop()`. That is structured concurrency you don't have to think about.

## 6.2 `coroutineScope { }` vs `supervisorScope { }`

```kotlin
coroutineScope {
    val a = async { riskyA() }
    val b = async { riskyB() }
    a.await() to b.await()
}
```

If `riskyA()` throws, `b` is cancelled and the whole `coroutineScope`
block re-throws. **Fail-fast semantics.**

```kotlin
supervisorScope {
    val a = async { riskyA() }
    val b = async { riskyB() }
    listOf(a, b).awaitAll()
}
```

If `riskyA()` throws, `b` keeps running. `a.await()` throws when called.
**Independent failures.**

Rule: use `coroutineScope` unless you have a specific reason for
children to be independent (a fan-out where partial success is OK).

## 6.3 `launch` vs `async` vs `withContext`

- `launch { … }` returns a `Job`. Fire-and-forget (still owned by scope).
- `async { … }` returns a `Deferred<T>`. Has a result you `.await()`.
- `withContext(ctx) { … }` is a *blocking* (suspending) call that
  switches dispatcher *without* spawning a separate coroutine.

The first two start *new* coroutines (and thus new continuations). The
third reuses the calling coroutine. For dispatcher switches, prefer
`withContext`.

## 6.4 Channels — hot pipes between coroutines

`Channel<T>` is a CSP-style pipe. You `send` from one coroutine,
`receive` in another. Variants:

- **Rendezvous** (`Channel.RENDEZVOUS`, capacity 0): sender suspends
  until receiver is ready. Backpressure by design.
- **Buffered** (`Channel(N)`): sender suspends only when full.
- **Conflated** (`Channel.CONFLATED`): only the latest value is kept.
- **Unlimited** (`Channel.UNLIMITED`): no backpressure. Memory bug
  waiting to happen.

We don't actually need a hand-rolled `Channel` for the Postgres stream:
`UserRepository.streamAll` is a cold `flow { }` over a server-side cursor,
and `emit` already suspends until the collector is ready (see §6.6). Where a
`Channel` *does* earn its place is in `UserRepository.listenForNewUsers`,
which fans LISTEN/NOTIFY events from a Vert.x callback into a
`Channel(Channel.BUFFERED)` for a coroutine consumer.

## 6.5 Flows — cold streams

```kotlin
val users: Flow<User> = repo.streamAll(null)
users.collect { println(it) }
```

A Flow is cold: nothing happens until you `collect`. Operators
(`map`, `filter`, `take`) are lazy and run on the collector's thread.

Flows are the cooperative-back-pressure type Kotlin coroutines push.
They map cleanly to Vert.x `ReadStream` (and Reactive Streams via
`asPublisher`).

Useful operators we use in this book:

| Operator                | Use                                                      |
|-------------------------|----------------------------------------------------------|
| `map { x -> g(x) }`     | per-item transform                                       |
| `filter { p }`          | drop items                                               |
| `take(n)`               | first n items                                            |
| `chunked(n)`            | group into batches of n (`@FlowPreview`, opt-in)         |
| `onEach { … }`          | side-effect (logging, metrics)                           |
| `flowOn(Dispatchers.X)` | switch dispatcher upstream                               |
| `buffer(n)`             | decouple producer and collector by an n-sized channel    |
| `conflate()`            | drop intermediate values, keep latest                    |

## 6.6 End-to-end back-pressure

This is the killer feature. In Chapter 13 we will serve a gRPC server
stream by reading rows from Postgres. If the client is slow, the gRPC
write buffer fills (`writeQueueFull()`), our handler awaits `drainHandler`,
and *that suspension stops `collect` from pulling the next item*. Because
`streamAll` is a cold `flow { }` over a server-side cursor, a stalled
collector means the next `emit` never runs, so the next
`cursor.read(fetchSize).coAwait()` is never issued. Postgres is never asked
for more rows; the cursor simply waits. The kernel's TCP buffers stop
draining and the network back-pressures *all the way back to disk*.

```
  Postgres ──TCP──► pg-client ──cursor.read()──► gRPC writer ──TCP──► slow client
       ▲                 ▲                            ▲                     │
       │                 │                            │                     │
       └─ no next read ◄─┴── emit suspends (no pull) ◄┴── writeQueueFull ◄──┘
```

The back-pressure is driven by *demand*, not by pausing a hot stream:
nothing fetches a batch it cannot hand off. Nothing in this chain "buffers
infinitely". A slow client costs the server only what one cursor batch + one
socket buffer cost. Memory stays flat.

## 6.7 SharedFlow and StateFlow

`SharedFlow` is **hot**: it emits to N subscribers. Useful for fanning
out a single source (LISTEN/NOTIFY events) to many handlers.
`StateFlow` is a `SharedFlow` with replay=1 that always has a current
value. Use it for "what is the latest config?" type signals.

We don't use them in the demo app but they pair well with Vert.x
event-bus pub/sub for in-process broadcasts.

## 6.8 Cancellation cooperation

A coroutine that never suspends can't be cancelled. Long CPU loops
should call `yield()` or check `currentCoroutineContext().isActive`.

```kotlin
for (row in bigList) {
    yield()                 // gives cancellation a chance
    process(row)
}
```

In I/O code you usually do not need this because each I/O `await` is a
suspension point, which is also a cancellation point.

## 6.9 The shape of our code

Look at `Routes.coHandler` and the `vertxFuture(vertx, scope) { }` calls in
`UserGrpcService`:

- We use a `CoroutineScope(SupervisorJob() + vertx.dispatcher())` owned by
  the component, and pass it explicitly to `scope.launch { }` and to
  `vertxFuture(vertx, scope) { }` → coroutines run on the event loop and are
  cancelled when we cancel that scope.
- We do not `GlobalScope.launch` anywhere.
- Long-running streams (`streamAll`, `chat`) are tied to their request's
  lifetime. When the client disconnects, the gRPC stream ends, the
  block returns, the coroutine completes.
- For inbound stream handlers we use Vert.x's own `handler { }` /
  `endHandler { }` callbacks because they fire on the same event loop —
  no scope hop needed.

## 6.10 Exercises

1. Add a `delay(50)` inside `Routes.handleStreamUsers` between rows.
   Hit the endpoint with `curl localhost:8080/api/users | pv > /dev/null`
   and use `pv -L 1k` to throttle the consumer. Observe DB CPU stay
   low. (Backpressure works.)
2. Remove the `writeQueueFull` / `drainHandler` block. Re-run with a
   slow consumer. Memory grows. Why exactly?
3. Replace the cursor-based `streamAll` with an eager version that buffers
   every row into a `List<User>` before returning (or feeds a
   `Channel.UNLIMITED`). Hit it under load against a large table. RSS climbs.
   Same lesson — demand-driven streaming and bounded buffers prevent runaway
   memory.

---

[← Chapter 5](05-coroutines-vertx.md) · [Next → Chapter 7: Config & logging](07-config-logging.md)
