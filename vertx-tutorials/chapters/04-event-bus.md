# Chapter 4 — The Event Bus

> **You should finish this chapter able to:** pick between `send`, `publish`,
> and `request`; design address conventions that scale; register a custom
> `MessageCodec` for a Kotlin `data class`; explain why clustered Vert.x is
> rarely the right answer in 2026; and use `coConsumer` to write suspending
> consumers that integrate cleanly with the rest of the app.

The event bus is the *only* idiomatic way for two verticles in the same
process to talk to each other. It is also the most commonly misused part of
Vert.x. This chapter is long because most of the mistakes are subtle and
costly — silent codec failures, leaked replies, accidental cluster broadcasts.

## 4.1  The mental model

Forget for a moment that you are inside a JVM. Pretend you are looking at a
message broker that happens to run in your process:

```
        ┌────────────────────────────────────────────────────────┐
        │                       EVENT BUS                        │
        │                                                        │
        │   address: "users.created"                             │
        │      └── consumer A (verticle X, EL-1)                 │
        │      └── consumer B (verticle Y, EL-2)                 │
        │                                                        │
        │   address: "billing.charge.request"                    │
        │      └── consumer C (verticle Z, EL-3)  [point-to-pt]  │
        │                                                        │
        │   address: "audit.*"                                   │
        │      (no wildcards — addresses are exact strings)      │
        └────────────────────────────────────────────────────────┘
                ▲                ▲                  ▲
                │                │                  │
            send/publish     send/publish      send/publish
                │                │                  │
        ┌──────────────┐ ┌──────────────┐  ┌──────────────┐
        │   Verticle   │ │   Verticle   │  │   Verticle   │
        │      P       │ │      Q       │  │      R       │
        └──────────────┘ └──────────────┘  └──────────────┘
```

Three properties matter:

1. **Async.** Every send/publish/request returns immediately. The bus does
   the dispatching on the consumer's Context.
2. **Typed by codec, not by Kotlin type.** Each message has a body of some
   type the bus knows how to serialize. If the bus does not know your type,
   you register a codec. There is no compile-time check that the receiver
   expects the same type — runtime ClassCastException is on you.
3. **Same API local or clustered.** When you run `Vertx.clusteredVertx(...)`
   the bus extends across nodes via a cluster manager. The code that uses it
   does not change. (Whether you should run clustered Vert.x at all is a
   different question — see §4.10.)

The bus is provided by the `Vertx` instance: `vertx.eventBus()` returns the
singleton `EventBus`.

## 4.2  Three patterns: send, publish, request

```
SEND (point-to-point, round-robin between consumers)
   producer ──► [addr] ──► consumer A      (only ONE of A/B receives
                       └─► consumer B       each message; bus picks)

PUBLISH (fan-out, every consumer receives)
   producer ──► [addr] ──► consumer A
                       ├─► consumer B
                       └─► consumer C

REQUEST (point-to-point with a reply Future)
   producer ─send─► [addr] ─► consumer A
   producer ◄─reply─────────  consumer A   (msg.reply(...) on A's side)
```

### `send` — exactly one consumer gets it

```kotlin
vertx.eventBus().send("orders.placed", JsonObject().put("orderId", "o-42"))
```

If multiple verticles registered consumers on `"orders.placed"`, Vert.x
picks one in round-robin. If no consumer is registered, the message is
**dropped silently**. There is no DLQ, no exception, nothing in the log.
That is intentional — `send` is fire-and-forget — but you must build your
observability around it. We usually emit a metric counter alongside every
`send` call.

### `publish` — every consumer gets it (fan-out)

```kotlin
vertx.eventBus().publish("users.created", JsonObject().put("userId", "u-1"))
```

Every consumer registered on `"users.created"` receives its own delivery.
This is the right primitive for domain events: one publisher, N independent
subscribers (an audit verticle, a cache invalidator, an analytics shipper).

### `request` — exactly one consumer, reply expected

```kotlin
val reply: Message<JsonObject> = vertx.eventBus()
    .request<JsonObject>("billing.quote", JsonObject().put("sku", "X"))
    .coAwait()                                                // 1
val price: Double = reply.body().getDouble("price")
```

1. `request` returns a `Future<Message<R>>`. We `coAwait()` it to get the
   reply body wrapper. By default the reply must arrive within **30 seconds**
   or the future fails with `ReplyException(REPLY_TIMEOUT)`. You almost
   always want to lower this — see §4.6.

On the consumer side:

```kotlin
vertx.eventBus().consumer<JsonObject>("billing.quote") { msg ->
    val price = compute(msg.body().getString("sku"))
    msg.reply(JsonObject().put("price", price))
}
```

`request` is the equivalent of an in-process RPC. It is convenient but it
couples producer and consumer in time — the producer is suspended until the
reply arrives. Prefer `publish` for one-way notifications and reserve
`request` for genuine query/response.

## 4.3  Addresses are just strings

There is no schema, no registry, no namespace check. `"foo"` is a valid
address. So is `"   "`. The convention we follow throughout `full-app`:

```
   <domain>.<entity>.<verb>[.<qualifier>]
```

| Address | Pattern | Notes |
|---|---|---|
| `users.created` | event (publish) | past-tense verb, fanout |
| `users.get.request` | RPC (request) | imperative verb, point-to-point |
| `cache.invalidate` | command (send) | one worker handles it |
| `audit.event` | event (publish) | catch-all for audit shipping |
| `internal.metrics.tick` | timer | use a `private` prefix you never publish to externally |

Two rules that save pain later:

- **Lower-case, dot-separated.** It mirrors Kafka topic conventions and reads
  nicely in logs.
- **Stable.** Addresses are a public contract between verticles. Renaming
  one is a coordinated refactor. Pretend they are URL paths.

## 4.4  Codecs — what actually crosses the wire

This is the part most tutorials skip and most production incidents come from.

When you call `eventBus().send("addr", payload)`, the bus needs to turn
`payload` into bytes that the consumer can decode back. The decision tree:

```
                payload type?
                     │
   ┌─────────────────┼──────────────────┬────────────────┐
   ▼                 ▼                  ▼                ▼
String,           JsonObject,         Buffer,        anything else
primitives,       JsonArray                          (your class)
Char, byte[],                                              │
ReplyException                                             ▼
                                                  → register a MessageCodec
                                                    OR pass it as JsonObject
        (built-in codecs handle these)
```

Critical surprise: **for purely local delivery, Vert.x may still serialize
the body**. The rule is:

- If the message is delivered to a consumer registered on the *same*
  `Context` and the body is a built-in immutable type (String, primitives,
  JsonObject, Buffer), the bus passes the *reference* through without
  copying. Cheap.
- For any custom-codec body, Vert.x calls `encodeToWire` then `decodeFromWire`
  even locally, **unless** you flag the message with the LOCAL delivery
  option AND the codec opts into local pass-through. The default is "encode".

The defensive copy is intentional: it stops one consumer from mutating
another's view of the body. But it means a sloppy custom codec (e.g., one
that allocates a 50 KB byte array on every call) becomes a per-message tax
on local delivery too.

### Registering a codec for a Kotlin data class

Suppose we have:

```kotlin
data class UserCreatedEvent(
    val userId: String,
    val email: String,
    val createdAtEpochMs: Long
)
```

We want to publish it on `users.created` and have an audit verticle consume
it without going through `JsonObject`. We need a `MessageCodec`:

```kotlin
import io.vertx.core.buffer.Buffer
import io.vertx.core.eventbus.MessageCodec
import io.vertx.core.json.Json

class UserCreatedEventCodec : MessageCodec<UserCreatedEvent, UserCreatedEvent> {

    override fun encodeToWire(buffer: Buffer, body: UserCreatedEvent) {     // 1
        val payload = Json.encodeToBuffer(body)
        buffer.appendInt(payload.length())
        buffer.appendBuffer(payload)
    }

    override fun decodeFromWire(pos: Int, buffer: Buffer): UserCreatedEvent { // 2
        val length = buffer.getInt(pos)
        val slice = buffer.slice(pos + 4, pos + 4 + length)
        return Json.decodeValue(slice, UserCreatedEvent::class.java)
    }

    override fun transform(body: UserCreatedEvent): UserCreatedEvent = body  // 3

    override fun name(): String = "UserCreatedEventCodec"                    // 4

    override fun systemCodecID(): Byte = -1                                  // 5
}
```

**Line by line:**

1. `encodeToWire` is called when the body must be serialized — for clustered
   delivery, and for local delivery unless we opt into a fast path. We write
   a length-prefixed JSON buffer. Use any format you like (Protobuf,
   MessagePack); JSON is fine for sub-MB messages.
2. `decodeFromWire` reverses it. `pos` is where the body starts inside a
   larger buffer the bus owns; do not assume `pos == 0`.
3. `transform` is called on the *local* fast path, when the bus is allowed
   to deliver the in-memory object without serializing. Since our class is a
   `data class` and effectively immutable, returning `body` is safe. For a
   mutable type you would `copy()` here.
4. `name()` is the unique codec id you reference in `DeliveryOptions`.
5. `systemCodecID()` is `-1` for user codecs. Only the built-in codecs have
   real ids; do not invent one.

Register the codec once, at startup, on the bus:

```kotlin
vertx.eventBus().registerDefaultCodec(
    UserCreatedEvent::class.java,
    UserCreatedEventCodec()
)
```

`registerDefaultCodec` ties the codec to the Java class so subsequent
publishes do not need an explicit codec name. After this you can write:

```kotlin
vertx.eventBus().publish("users.created", UserCreatedEvent("u-1", "x@y", now))
```

If you skip `registerDefaultCodec` and just `registerCodec`, you must pass
`DeliveryOptions().setCodecName("UserCreatedEventCodec")` on every publish.
Forget it once and you get a runtime `IllegalArgumentException("No message
codec for type ...")` at the send site.

### When NOT to write a codec

For 90% of use cases, **just use `JsonObject`**. Codecs are real code with
real bugs (we just wrote 20 lines that allocate three buffers). `JsonObject`
is built-in, ships clustered for free, debug-prints sensibly. The case for a
custom codec is:

- You have a hot path (>10k msgs/s) where JSON parsing shows up in a profile.
- The body genuinely is a binary blob (already a Protobuf, an image, etc.).
- You want compile-time type safety on the consumer side and accept the
  maintenance cost.

In `full-app` we use `JsonObject` for the example in §4.11. Consider the
codec section a reference you will need eventually, not a default.

## 4.5  Consumer registration

A consumer is just a handler attached to an address. Three styles:

```kotlin
// 1. Future/callback style (works in AbstractVerticle).
vertx.eventBus().consumer<JsonObject>("users.created") { msg ->
    val body: JsonObject = msg.body()
    log.info("user created: {}", body.getString("userId"))
}

// 2. Suspending consumer via vertx-lang-kotlin-coroutines.
import io.vertx.kotlin.coroutines.coConsumer

vertx.eventBus().coConsumer<JsonObject>("users.created") { msg ->
    val user = userRepo.findById(msg.body().getString("userId"))   // suspends
    audit.write(user)                                              // suspends
}

// 3. Explicit registration result.
val consumer: MessageConsumer<JsonObject> =
    vertx.eventBus().consumer("users.created")
consumer.handler { msg -> /* ... */ }
consumer.endHandler { log.info("consumer unregistered") }
```

`coConsumer { }` is just sugar for `consumer { msg -> launch { handler(msg) } }`
on the verticle's dispatcher. It is the right default in a `CoroutineVerticle`.

Two properties worth knowing:

- **Consumer is bound to the registering Context.** Every delivery runs on
  the event loop of the verticle that called `.consumer(...)`. If you
  deployed `instances=4` and each instance registers a consumer, you have
  four consumers on one address, each on its own event loop. `send` will
  round-robin between them; `publish` will deliver to all four.
- **`unregister()` is a Future.** It is async because in clustered mode it
  must propagate to other nodes. Always `coAwait()` it in `stop()` if you
  registered consumers manually outside the verticle's auto-cleanup.

## 4.6  Reply semantics

`msg.reply(body)` on the consumer side completes the producer's `request`
future with a `Message<R>` wrapping `body`. `msg.fail(code, reason)` fails
it with a `ReplyException(ReplyFailure.RECIPIENT_FAILURE, code, reason)`.

```kotlin
vertx.eventBus().coConsumer<JsonObject>("users.get.request") { msg ->
    val id = msg.body().getString("id")
    val user = userRepo.findById(id)
    if (user == null) {
        msg.fail(404, "user not found")           // explicit business failure
    } else {
        msg.reply(JsonObject.mapFrom(user))
    }
}
```

On the producer:

```kotlin
val opts = DeliveryOptions().setSendTimeout(2_000)               // 2s, not 30s
try {
    val reply = vertx.eventBus()
        .request<JsonObject>("users.get.request",
            JsonObject().put("id", id), opts)
        .coAwait()
    return reply.body().mapTo(User::class.java)
} catch (e: ReplyException) {
    when (e.failureType()) {
        ReplyFailure.TIMEOUT             -> /* consumer too slow */
        ReplyFailure.NO_HANDLERS         -> /* no consumer registered */
        ReplyFailure.RECIPIENT_FAILURE   -> /* msg.fail was called: e.failureCode() */
        else                             -> throw e
    }
}
```

Three `ReplyFailure` types you should always distinguish:

| `failureType()` | Meaning | Likely cause |
|---|---|---|
| `TIMEOUT` | No reply within `sendTimeout`. | Consumer slow, dropped, or crashed. |
| `NO_HANDLERS` | No consumer registered. | Verticle not deployed yet, address typo, cluster partition. |
| `RECIPIENT_FAILURE` | `msg.fail(code, reason)` was called. | Business failure; inspect `code`. |

A common newcomer mistake is to treat any `ReplyException` as a server
error. `NO_HANDLERS` after restart is *expected* during boot ordering and
should usually retry, not 500.

### Replies do not echo codecs

The reply body uses **the codec of the reply value**, not the request's.
If your request body is a `UserCreatedEvent` with the custom codec and you
reply with a `JsonObject`, the JSON codec encodes the reply. This is fine
but worth knowing if you build a single typed codec and assume "replies are
symmetric."

## 4.7  DeliveryOptions

Most of the bus's knobs live on `DeliveryOptions`. The ones you will use:

```kotlin
val opts = DeliveryOptions()
    .setSendTimeout(2_000)                            // reply timeout (ms)
    .setCodecName("UserCreatedEventCodec")            // override default codec
    .setLocalOnly(true)                               // do NOT cross cluster
    .addHeader("traceId", "abc-123")                  // string-only multimap
    .addHeader("tenant", "acme")
```

A few notes:

- **Headers are `MultiMap<String, String>` only.** Do not stuff JSON in
  there. They are for routing and tracing; the body is for data.
- **`setLocalOnly(true)`** is a per-message switch. Use it for chatter that
  must never leak to other cluster nodes (e.g., per-node cache invalidation).
  There is also `consumer.setLocalOnly(true)` to ignore remote sends.
- **No "key" for ordered delivery.** Unlike Kafka, the bus does not preserve
  ordering between independent producers or across consumers. If you need
  ordering, send everything from one source verticle to one consumer.
- **`setTracingPolicy`** controls whether the OpenTelemetry tracer
  instruments this bus call. The default (`PROPAGATE`) is correct for most
  apps.

## 4.8  Clustered Vert.x — what it is, and why you usually don't want it

Drop in a cluster manager dependency (`vertx-hazelcast`, `vertx-infinispan`,
`vertx-ignite`, `vertx-zookeeper`) and boot with `Vertx.clusteredVertx(...)`.
Every event bus call is now potentially network-bound:

```
Node A                              Node B
┌──────────────┐                    ┌──────────────┐
│ Verticle X   │                    │ Verticle Y   │
│   publish    │                    │  consumer    │
│ "users.created"                   │ "users.created"
└──────┬───────┘                    └──────▲───────┘
       │                                   │
       ▼                                   │
┌─────────────────────────────┐            │
│   EventBus (clustered)      │            │
│      ┌────────────────┐     │            │
│      │ ClusterManager │     │            │
│      │  (membership,  │     │            │
│      │  addr → nodes) │     │            │
│      └────────┬───────┘     │            │
│               ▼             │            │
│      Netty TCP connection ─────────────► │
│      (per-node, multiplexed)│            │
└─────────────────────────────┘            │
```

The cluster manager owns *membership* and a distributed map of
`address → set of nodes`. The bus itself owns the TCP connections. Default
ports: 5701 (Hazelcast), 7800 (Infinispan/JGroups).

When the bus picks a remote consumer (round-robin for `send`, all of them
for `publish`), it serializes the body via the codec, ships it over a
Netty pipeline to the destination node, and re-publishes it on that node's
local bus.

### Why you probably want Kafka or NATS instead

In 2026, clustered Vert.x is a niche tool. The list of things it does not
give you:

- **Persistence.** Messages are in-memory; a consumer that was down misses
  events. No replay.
- **Backpressure.** None. A slow consumer drops events (publish) or makes
  producers time out (request). See §4.9.
- **Cross-language consumers.** Only JVM languages with Vert.x.
- **Schemas / versioning.** None. A field rename is a deploy-coordination
  problem.
- **Observability.** Cluster-manager-specific tools (JConsole on Hazelcast,
  the Infinispan console) rather than a real broker UI.

For inter-service messaging in a modern microservice deployment, run a
real broker:

| Need | Tool |
|---|---|
| Durable event log, replay, partitions | Kafka, Redpanda |
| Pub/sub with subjects, JetStream | NATS |
| Queues with ack/retry/DLQ | RabbitMQ, NATS JetStream |
| Lightweight in-process bus across N replicas of one service | Redis pub/sub |

Where clustered Vert.x *does* still fit:

- A single logical service deployed as N stateful nodes that need
  best-effort intra-service gossip — e.g., distributed cache invalidation,
  in-memory session affinity hints.
- Co-located processes already using Vert.x where you do not want to
  introduce a broker.

Default is to **not** cluster. Run one Vert.x per pod, talk between pods
over HTTP/gRPC, and use a broker for events.

## 4.9  Backpressure (there is none)

The event bus does no flow control. If a `publish` arrives faster than a
consumer can drain it:

- **Same Context.** Handlers queue on the event loop's task queue. The queue
  is unbounded — memory grows until you OOM.
- **Across nodes (clustered).** Netty's outbound buffer fills; eventually
  writes start failing with `ConnectionResetException` and messages are
  lost.

You can simulate backpressure manually with `request`/`reply`:

```kotlin
// Consumer: reply only after the work is done.
vertx.eventBus().coConsumer<JsonObject>("ingest.batch") { msg ->
    try {
        repo.insert(msg.body())     // suspends until DB ack
        msg.reply(JsonObject().put("ok", true))
    } catch (e: Throwable) {
        msg.fail(500, e.message ?: "ingest failed")
    }
}

// Producer: await the reply before sending the next.
for (item in items) {
    vertx.eventBus()
        .request<JsonObject>("ingest.batch", item)
        .coAwait()                 // throttles to consumer throughput
}
```

This turns the bus into a synchronous queue with a depth of one. If you
need more throughput, run N producers; if you need durability, do not use
the event bus.

## 4.10  `coConsumer` versus manual launch

You can write either:

```kotlin
// A: coConsumer
vertx.eventBus().coConsumer<JsonObject>("users.created") { msg ->
    auditService.write(msg.body())   // suspends
}

// B: consumer + launch (explicit)
vertx.eventBus().consumer<JsonObject>("users.created") { msg ->
    launch { auditService.write(msg.body()) }
}
```

A and B are *almost* the same. The differences:

- A propagates exceptions to `msg.fail(...)` (for replies) or to the bus's
  exception handler. B silently completes the inner job; exceptions become
  uncaught coroutine exceptions in the verticle scope. Prefer A.
- A uses the verticle's `CoroutineScope`. So does B when written inside a
  `CoroutineVerticle`. Outside one, B forces you to manage a scope manually.
- B lets you customize the launched coroutine (different dispatcher,
  supervisor job, name) — useful for audit-style "fire-and-forget that must
  outlive cancellation."

## 4.11  Worked example: `users.created` and an audit verticle

The flow we want:

```
HTTP POST /api/v1/users      gRPC CreateUser
       │                            │
       └─────────────┬──────────────┘
                     ▼
            UserService.create(...)
                     │
              insert into PG
                     │
              publish "users.created"
                     │
       ┌─────────────┼──────────────┐
       ▼             ▼              ▼
  AuditVerticle  CacheVerticle  AnalyticsVerticle
  (write audit)  (invalidate)   (ship to Kafka)
```

This is the canonical "domain event" shape. Let's wire just the publish
and one consumer — the rest is the same shape.

### The publisher (in `UserService`)

This snippet is not yet in `code/full-app/` — it would slot into
[`UserService.kt`](../code/full-app/src/main/kotlin/com/example/app/domain/UserService.kt)
after the insert:

```kotlin
class UserService(
    private val vertx: Vertx,
    private val repo: UserRepository,
) {
    suspend fun create(req: CreateUserRequest): User {
        val user = repo.insert(req)                                       // 1

        vertx.eventBus().publish(                                         // 2
            "users.created",
            JsonObject()
                .put("userId", user.id)
                .put("email", user.email)
                .put("createdAtEpochMs", user.createdAt.toEpochMilli()),
            DeliveryOptions().addHeader("traceId", currentTraceId())      // 3
        )
        return user
    }
}
```

1. The insert must succeed *before* we publish. The event semantics are
   "this user exists in the DB"; publishing before commit is a lie that
   downstream caches will believe.
2. `publish`, not `send` — every interested verticle should receive it.
3. We attach the trace id as a header so the audit verticle's spans link to
   the originating request. The body stays a clean domain event.

### The audit verticle (new file)

This file is not in the repo today; consider it the next exercise:

```kotlin
// code/full-app/src/main/kotlin/com/example/app/verticles/AuditVerticle.kt
package com.example.app.verticles

import io.vertx.core.json.JsonObject
import io.vertx.kotlin.coroutines.CoroutineVerticle
import io.vertx.kotlin.coroutines.coConsumer
import org.slf4j.LoggerFactory

class AuditVerticle : CoroutineVerticle() {
    private val log = LoggerFactory.getLogger(AuditVerticle::class.java)

    override suspend fun start() {
        vertx.eventBus().coConsumer<JsonObject>("users.created") { msg ->  // 1
            val traceId = msg.headers().get("traceId") ?: "-"              // 2
            val body = msg.body()
            log.info(
                "audit user.created userId={} email={} traceId={}",
                body.getString("userId"),
                body.getString("email"),
                traceId,
            )
            // Pretend this is a slow append-only DB.
            writeAudit(body, traceId)                                      // 3
        }
        log.info("AuditVerticle listening on users.created")
    }

    private suspend fun writeAudit(body: JsonObject, traceId: String) {
        // Real implementation: insert into an `audit_log` table.
        // For now, simulate IO.
    }
}
```

**Line by line:**

1. `coConsumer<JsonObject>` registers a suspending consumer on the
   verticle's event loop. Note that we do not need `msg.reply(...)` for a
   pub/sub event — replies are only meaningful for `request`.
2. Headers are pulled via `msg.headers()`. The map is empty (not null) when
   the producer passed no headers.
3. The suspending `writeAudit` runs on the same event loop. If the DB call
   suspends, the event loop continues serving other consumers / requests
   while we wait.

### Deploying it

In `Main.kt` (or in `AppVerticle.start()`), deploy alongside `AppVerticle`:

```kotlin
vertx.deployVerticle({ AuditVerticle() },
    DeploymentOptions().setInstances(1)).coAwait()
```

We use `instances=1` for audit because we want **exactly one** subscriber
per node — a domain event should be audited once, not per-event-loop. If
you deployed `instances=4`, all four would each fire on the publish (it is
fan-out), and you would record four audit rows per user creation. This is
the most common event-bus footgun in `publish`-style code.

### The mental flow

```
T0: POST /api/v1/users  arrives on EL-2
T1: handler suspends on PG INSERT (EL-2 keeps working)
T2: PG ACK, handler resumes on EL-2
T3: handler calls publish("users.created", ...)
        bus iterates registered consumers:
          - AuditVerticle on EL-7      → schedule task on EL-7
          - (any future subscriber)    → schedule on their EL
T4: handler returns 201 to the HTTP client (EL-2)
T5: EL-7 picks up the scheduled task, runs AuditVerticle handler
T6: writeAudit suspends on its own IO
T7: ... life goes on ...
```

Step T4 happens **before** step T5 in general. The publish is fire-and-forget;
the HTTP response does not wait for the audit. This is a feature: failures
or slowness in audit do not affect user-creation latency. If you need
"audited or we did not create the user," that is no longer an event —
that's a transaction, and the event bus is the wrong tool.

## 4.12  Things people get wrong

**"`send` is durable / reliable."** It is neither. No-consumer = silently
dropped, slow consumer = unbounded queue, node crash = lost. Use a broker
if you need durability.

**"I can `publish` to N instances of the same verticle to load-balance."**
That is the opposite of what `publish` does. `publish` is fan-out — every
instance receives. Use `send` for load-balance round-robin between
instances.

**"`request` from inside the consumer of the same address."** This deadlocks
in single-instance setups (your consumer is the only handler; it cannot
both wait for a reply and process the reply). Vert.x will not warn you. If
you need recursive RPC, use distinct addresses.

**Forgetting that `consumer` registration is async.** `eventBus.consumer(...)`
returns a `MessageConsumer` whose `completionHandler` fires when the
registration is acknowledged (immediately for local, after cluster
broadcast for clustered). In clustered mode, sending right after registering
can race — `NO_HANDLERS`. Inside a `CoroutineVerticle.start()`, use
`consumer.completion().coAwait()`.

**Mutating a `JsonObject` you published.** For built-in JSON types the bus
*may* pass references on the local fast path. If you `put` into the same
`JsonObject` after publishing, the consumer can see your mutation. Always
treat published payloads as immutable: build, publish, drop the reference.

**Using the bus for streams of millions of small messages.** It is fast
(low millions of msg/s/core for short JSON bodies) but the lack of
backpressure makes it brittle. For high-volume telemetry, use a real
queue.

**Custom codecs that throw inside `encodeToWire`.** The exception
propagates to the *producer*. If you wrapped the publish in
fire-and-forget code, the failure is invisible. Always log/test
encode/decode roundtrips.

**Assuming reply timeouts default to "infinite."** The default is **30
seconds**. For an in-process RPC where you expect single-digit-ms latency,
30s is far too patient — under load you fill up coroutines waiting on dead
consumers. Set `setSendTimeout` to a realistic value, e.g., 2× p99.

**Treating addresses as private to a verticle.** They are global to the
`Vertx` instance (and the whole cluster, if clustered). A typo can silently
match another team's address. Prefix with your service or domain.

## 4.13  Quick reference

```
send       eventBus.send(addr, body[, opts])               → Unit, one consumer
publish    eventBus.publish(addr, body[, opts])            → Unit, all consumers
request    eventBus.request<R>(addr, body[, opts])         → Future<Message<R>>
consume    eventBus.consumer<T>(addr) { msg -> ... }       → MessageConsumer<T>
co-consume eventBus.coConsumer<T>(addr) { msg -> ... }     → suspending handler
reply      msg.reply(body)                                 → completes producer's future
fail       msg.fail(code, reason)                          → fails producer's future
codec      registerDefaultCodec(Cls::class.java, codec)    → per-type default
options    DeliveryOptions().setSendTimeout(2000)
                            .setLocalOnly(true)
                            .addHeader("k", "v")
                            .setCodecName("MyCodec")
```

## 4.14  Try it

1. **`UserCreatedEvent` end to end.** Write `UserCreatedEventCodec` from
   §4.4, register it in `AppVerticle.start()`, modify `UserService.create`
   to publish a typed event (not `JsonObject`), and have `AuditVerticle`
   consume the typed event. Verify that omitting `registerDefaultCodec`
   produces a clear `IllegalArgumentException` at the send site, and that
   omitting the codec on a *clustered* bus would lose the message silently
   on the remote node.
2. **Backpressure experiment.** Spin up a consumer that sleeps 50 ms per
   message (use `delay(50)`, not `Thread.sleep`). Hammer it with
   `publish` from a `vertx.setPeriodic(1)` ticker. Watch heap grow.
   Now replace `publish` with `request().coAwait()` and observe the
   producer naturally throttles to the consumer's rate. Plot both.
3. **Address-collision audit.** Add a tiny `BusAuditVerticle` that registers
   a consumer on `"$" + UUID.randomUUID()` and uses Vert.x's internal
   `EventBusInternal` (Java reflection) to log every send/publish on the
   bus. (Hint: this is the foundation of a per-process bus tap. Vert.x has
   no first-class "interceptor on all addresses" yet.) Run the full app
   and inspect what fires during a single `GET /api/v1/users/123`.

[← Ch 3](03-coroutines.md) · [Next: Chapter 5 — Avoiding blocking →](05-avoiding-blocking.md)
