# Chapter 9 — Redis with `vertx-redis-client`

> **You should finish this chapter able to:** wire `RedisAPI` correctly,
> apply caching patterns (cache-aside, write-through, distributed lock),
> use pipelining and `MULTI/EXEC`, subscribe to pub/sub, talk to a cluster
> or sentinel topology, and avoid the classic cache pitfalls (thundering
> herd, stale invalidation, key-name collisions).

Redis is the **default operational cache** for low-latency services. It's a
single-threaded in-memory server you can pretend is a hashmap with a network
in front, except it also does pub/sub, streams, sorted sets, geospatial,
HyperLogLog, and Lua scripts. We use it for *exactly one thing in this
project*: per-key cache of `User` reads to take pressure off Postgres.

This chapter assumes you have chapter 8 fresh — many of the same async
patterns apply.

## 9.1  Why redis here

In our app, `UserService.get(id)` does this:

```
                        ┌──────────────┐
       client GET /...  │  RedisCache  │  ── if hit:  return
            ─────────►  │              │
                        │  GET user:42 │
                        └──────┬───────┘
                               │ miss
                               ▼
                        ┌──────────────┐
                        │ UserRepo (PG)│  ── on cache miss
                        │  SELECT ...  │     fetch & repopulate
                        └──────┬───────┘
                               │
                               ▼
                       cache.put(user, ttl=60s)
                               ▼
                            return
```

Why a cache:

- **Latency.** Redis is ~0.5 ms on a LAN; Postgres on a non-trivial query is
  3–30 ms. Hot reads stay fast.
- **Throughput.** Redis can do 100k+ ops/sec on a small instance. Postgres
  reads, even with pooling and pipelining, top out earlier.
- **Pressure relief.** A traffic spike doesn't immediately load up Postgres.

When *not* to cache:

- **Writes you can't tolerate as stale.** Pricing, balances. Bypass the cache
  for these, or invalidate explicitly.
- **Personalized data with low hit rate.** If each user gets 1 fetch and
  never again, cache churn loses to direct DB.

## 9.2  The client and the API

`vertx-redis-client` gives you three levels:

| API | When to use |
|---|---|
| `Redis.createClient(vertx, opts).connect()` | Raw bidirectional client. Pub/sub, scripts, low-level. |
| `RedisAPI.api(client)` | High-level wrapper with `get(...)`, `set(...)`, `hmget(...)`, etc. **What we use.** |
| `RedisCluster`, `RedisSentinel` (under `Redis.createClient(...)` with right `RedisOptions`) | Cluster / sentinel topologies. |

[`RedisCache.kt`](../code/full-app/src/main/kotlin/com/example/app/cache/RedisCache.kt):

```kotlin
class RedisCache private constructor(
    private val client: Redis,
    private val api: RedisAPI,
) {
    suspend inline fun <reified T : Any> getJson(key: String): T? {
        val resp = api.get(key).coAwait() ?: return null
        val s = resp.toString() ?: return null
        return Json.decodeValue(s, T::class.java)
    }

    suspend fun <T : Any> putJson(key: String, value: T, ttlSeconds: Long) {
        val s = Json.encode(value)
        api.set(listOf(key, s, "EX", ttlSeconds.toString())).coAwait()
    }

    suspend fun delete(key: String) {
        api.del(listOf(key)).coAwait()
    }

    suspend fun ping(): Boolean = runCatching { api.ping(emptyList()).coAwait() }.isSuccess

    companion object {
        fun create(vertx: Vertx, cfg: RedisConfig): RedisCache {
            val options = RedisOptions()
                .setConnectionString(cfg.uri)
                .setMaxPoolSize(cfg.maxPoolSize)
                .setMaxWaitingHandlers(2048)
            val client = Redis.createClient(vertx, options)
            return RedisCache(client, RedisAPI.api(client))
        }
    }
}
```

### Options that matter

```kotlin
RedisOptions()
    .setConnectionString("redis://localhost:6379/0")
    .setMaxPoolSize(8)             // # of TCP connections in the pool
    .setMaxPoolWaiting(8)          // queue depth before back-pressure
    .setMaxWaitingHandlers(2048)   // in-flight requests per connection
    .setNetClientOptions(NetClientOptions().setTcpKeepAlive(true))
    .setPoolRecycleTimeout(15 * 60_000)  // connection age limit
    .setPoolCleanerInterval(60_000)
    .setEndpoints(listOf("redis://a:6379", "redis://b:6379"))  // for cluster
```

| Option | Effect |
|---|---|
| `maxPoolSize` | Connections in the pool. Redis is single-threaded so 4–16 is plenty. |
| `maxWaitingHandlers` | Per-connection pipelining depth (analog to PG pipelining limit). |
| `setConnectionString` | Includes DB index (`/0`) and optional `?password=...`. |

Redis is single-threaded server-side, so **adding connections does not
parallelize the server**. Pool exists for *client* concurrency and head-of-
line-blocking avoidance. 8 connections × 256 pipelined per connection is
already 2048 in-flight commands.

## 9.3  Cache-aside in code

Concretely, the read path in `UserService`:

```kotlin
class UserService(
    private val repo: UserRepository,
    private val cache: RedisCache,
) {
    private val log = LoggerFactory.getLogger(UserService::class.java)

    suspend fun get(id: String): User? {
        cache.getJson<User>(userKey(id))?.let { return it }
        val u = repo.findById(id) ?: return null
        cache.putJson(userKey(id), u, ttlSeconds = 60)
        return u
    }

    suspend fun create(email: String, name: String): User {
        val u = repo.insert(email, name)
        cache.putJson(userKey(u.id), u, ttlSeconds = 60)
        return u
    }

    private fun userKey(id: String) = "user:$id"
}
```

Notes on the key naming convention:

- **Prefix every key with the entity type.** `user:42` not `42`. Prevents
  collisions and makes `SCAN MATCH user:*` work.
- **Versioning prefix on schema changes.** When you change the User shape
  (rename a field), bump the prefix to `user:v2:42` and old entries
  expire on their own without a flush.

## 9.4  TTL strategy

The TTL choice is a tradeoff:

| TTL | Pro | Con |
|---|---|---|
| Very short (≤5 s) | Fresher data. | Mostly miss; cache barely helps. |
| Short (30–60 s) | Good hit rate, bounded staleness. **Default for read-heavy CRUD.** | Stale reads up to TTL. |
| Long (10+ min) | Highest hit rate. | Need explicit invalidation on writes. |
| Forever | DB load minimal once warm. | Hot keys live forever; eviction comes from `maxmemory-policy`. |

Our 60-second default fits "user profile that changes rarely". If you can
absorb up to 60 s of staleness, this is great.

## 9.5  Explicit invalidation

For write paths, two options:

**(a) Refresh the cache.** Easier mental model:

```kotlin
suspend fun update(id: String, name: String) {
    val u = repo.update(id, name)
    cache.putJson(userKey(id), u, 60)
}
```

**(b) Invalidate (delete).** Less network if many fields change at once:

```kotlin
suspend fun update(id: String, name: String) {
    repo.update(id, name)
    cache.delete(userKey(id))
}
```

Pattern (b) lets the next reader populate. But beware: it allows a *small
window* where:

```
T0  read u:42 → miss, fetch from DB (slow)
T1  write u:42 → DB updated, cache.delete()
T2  read T0 finishes → cache.put(STALE_VALUE)
```

If you really care, use a **versioning marker**: store `(version, value)` in
the cache; on miss, fetch and `SET IF version > cached.version`. For most
apps, pattern (a) is enough.

## 9.6  Thundering herd ("cache stampede")

If a popular key expires and 1000 concurrent requests all miss simultaneously,
they all hit the DB. The classic fix is **a single-flight lock**:

```kotlin
suspend fun getWithSingleFlight(id: String): User? {
    cache.getJson<User>("user:$id")?.let { return it }

    // Try to acquire a short Redis lock with NX + EX.
    val lockKey = "lock:user:$id"
    val lockToken = UUID.randomUUID().toString()
    val acquired = api.set(listOf(lockKey, lockToken, "NX", "EX", "5")).coAwait() != null

    return if (acquired) try {
        repo.findById(id)?.also { cache.putJson("user:$id", it, 60) }
    } finally {
        // Release the lock with a Lua script (compare-and-delete)
        val script = """
            if redis.call("get", KEYS[1]) == ARGV[1] then
              return redis.call("del", KEYS[1])
            else return 0 end
        """.trimIndent()
        api.eval(listOf(script, "1", lockKey, lockToken)).coAwait()
    } else {
        // Lost the race — small backoff, then read cache (will be populated)
        delay(50)
        cache.getJson<User>("user:$id") ?: repo.findById(id)
    }
}
```

For lower-stakes caches, a simpler trick is **jittered TTLs**: vary the
expiry by ±10% so 1000 keys don't all expire at the same second.

```kotlin
val ttl = (60 + Random.nextInt(-6, 6)).coerceAtLeast(30).toLong()
cache.putJson(userKey(u.id), u, ttl)
```

## 9.7  Pipelining and `MULTI/EXEC`

Redis pipelining (multiple commands without waiting for replies) is
transparent in `vertx-redis-client` — when you call several APIs in the
same coroutine, they get written back-to-back on the same connection.

For atomic groups, use `MULTI`/`EXEC`:

```kotlin
suspend fun bumpCounterAndLog(key: String) {
    val conn = client.connect().coAwait()
    try {
        conn.send(Request.cmd(Command.MULTI)).coAwait()
        conn.send(Request.cmd(Command.INCR).arg(key)).coAwait()
        conn.send(Request.cmd(Command.LPUSH).arg("log").arg("inc:$key")).coAwait()
        conn.send(Request.cmd(Command.EXEC)).coAwait()
    } finally {
        conn.close()
    }
}
```

`MULTI/EXEC` is *not* a "DB transaction" — Redis won't roll back. It's an
*atomic batch*. Once `EXEC` runs, all commands execute back-to-back without
other clients' commands sneaking in between.

For conditional logic, prefer **Lua scripts**:

```kotlin
private val incrIfBelow = """
    local v = tonumber(redis.call('get', KEYS[1]) or '0')
    if v < tonumber(ARGV[1]) then
      return redis.call('incr', KEYS[1])
    end
    return -1
""".trimIndent()

suspend fun rateLimit(key: String, limit: Int): Boolean {
    val ret = api.eval(listOf(incrIfBelow, "1", key, limit.toString())).coAwait()
    return (ret?.toLong() ?: -1) >= 0
}
```

Lua runs atomically *and* server-side, no extra round-trips.

## 9.8  Pub/Sub

Redis pub/sub is fire-and-forget; subscribers must be connected when the
publish happens. We use a *separate* client for subscriptions — a subscribed
client cannot send other commands.

```kotlin
class UserChangeStream(vertx: Vertx, opts: RedisOptions) {
    private val sub = Redis.createClient(vertx, opts)

    suspend fun start(handler: suspend (UserChange) -> Unit) {
        val conn = sub.connect().coAwait()
        conn.handler { resp ->
            // resp is a Push frame: ["message", channel, payload]
            val payload = resp[2].toString()
            // launch on the calling coroutine context
            launch { handler(Json.decodeValue(payload, UserChange::class.java)) }
        }
        conn.send(Request.cmd(Command.SUBSCRIBE).arg("user-changes")).coAwait()
    }
}
```

For higher reliability (delivery guarantees, replay) use **Streams**:

```kotlin
api.xadd(listOf("user-events", "*", "type", "user.created", "id", u.id)).coAwait()

// Consumer
val resp = api.xread(listOf("COUNT", "100", "BLOCK", "1000", "STREAMS", "user-events", lastId)).coAwait()
```

Streams persist and support consumer groups (think Kafka-lite). Excellent for
audit trails and async-job dispatch within a single Redis cluster.

## 9.9  Cluster, Sentinel, and HA

**Sentinel** = leader-election in front of a master/replica pair. You connect
to a sentinel, get the master address, and reconnect on failover:

```kotlin
RedisOptions()
    .setType(RedisClientType.SENTINEL)
    .setRole(RedisRole.MASTER)
    .addConnectionString("redis://sentinel-1:26379")
    .addConnectionString("redis://sentinel-2:26379")
    .addConnectionString("redis://sentinel-3:26379")
    .setMasterName("mymaster")
```

**Cluster** = sharded across N masters with hash-slot routing. Keys are
hashed (CRC16) into 16384 slots, slots are owned by masters. A `GET key`
might land on master B — the cluster returns a `MOVED` redirect and the
client retries. `vertx-redis-client` handles redirects transparently:

```kotlin
RedisOptions()
    .setType(RedisClientType.CLUSTER)
    .addConnectionString("redis://node-1:6379")
    .addConnectionString("redis://node-2:6379")
    .addConnectionString("redis://node-3:6379")
    .setMaxRedirects(8)
    .setUseReplicas(RedisReplicas.SHARE)        // optional: distribute GET to replicas
```

For multi-key commands across slots, use **hash tags**: keys wrapped in
`{...}` hash by the wrapped portion, so `{user-42}:profile` and
`{user-42}:tokens` live on the same slot.

## 9.10  Health checks

```kotlin
suspend fun ping(): Boolean =
    runCatching { api.ping(emptyList()).coAwait() }.isSuccess
```

Health check registration mirrors Postgres (chapter 8.11). One thing to
note: the Redis driver auto-reconnects on disconnect, so a transient
network blip will show as `false` for one health check tick, then recover.

## 9.11  Eviction and `maxmemory-policy`

Redis on its own won't grow unbounded if you set `maxmemory`. Configure the
eviction policy:

| Policy | Behavior |
|---|---|
| `noeviction` | Reject writes once full. Use for primary stores. |
| `allkeys-lru` | Evict any LRU key. **Default for caches.** |
| `volatile-lru` | Evict LRU among TTL-bearing keys only. |
| `allkeys-lfu` | Evict least-frequently-used. Best for skewed access patterns. |
| `volatile-ttl` | Evict shortest-TTL first. |

On the application side, set TTLs even with `allkeys-lru`. TTL is your
correctness backstop; LRU is your memory backstop. They're complementary.

## 9.12  Common pitfalls

**Holding a subscribed connection in the main pool.** A subscriber blocks
that connection from regular commands. Always a separate `Redis.createClient`
for subscriptions.

**Using `KEYS pattern` in production.** `KEYS user:*` scans the entire DB,
blocking Redis. Use `SCAN MATCH user:* COUNT 100` and iterate.

**Storing huge values.** Redis works best with values < 100 KB. Larger
payloads slow everything down (single-threaded server!). Store the small
mutable bit in Redis and a pointer to S3/CDN for the big blob.

**Forgetting that `INCR` is atomic across clients.** It is. You don't need
a lock to do a counter. Counters in Redis are the textbook example.

**Caching personalized data with a public key.** `cache.put("recommendations",
list)` overwrites every user's recommendations with the last one's. Always
namespace by `user_id` (or session, or whatever the identity is).

**Treating Redis as your source of truth.** Redis has persistence, but
network partitions, AOF rewrites, and replication lag can lose data on
crash. Use Postgres (or another durable store) as the system of record.

## 9.13  A practical "write-through" pattern (when you need fresher reads)

```kotlin
suspend fun update(id: String, name: String): User {
    return pool.txCoroutine { conn ->
        // 1. write to DB inside a transaction
        val u = conn.preparedQuery(UPDATE).execute(Tuple.of(name, id)).coAwait()
            .first().toUser()
        // 2. before committing, refresh the cache
        cache.putJson(userKey(id), u, 60)
        u
    }
}
```

Write-through *can* still race (cache updated before DB commit succeeds), so
the proper version uses a *post-commit* hook. For most apps the
straightforward "DB then cache" sequence is sufficient — accept the rare
stale window.

## 9.14  Try it

1. Add a `DELETE /api/v1/users/:id` route. Inside the handler, delete from
   Postgres then invalidate the cache key. Verify with a follow-up GET that
   the cache is bypassed.
2. Implement single-flight (`§9.6`) for `UserService.get`. Use a 1000-RPS
   load test against a single hot ID with TTL=1s and observe how DB QPS
   behaves before and after.
3. Build a small `EventBusBridge` that listens on Redis stream `user-events`
   and re-publishes to the Vert.x event bus. This lets non-Redis-aware
   verticles (chapter 4) react to cache invalidations.

[← Ch 8](08-postgresql.md) · [Next: Chapter 10 — gRPC unary service →](10-grpc.md)
