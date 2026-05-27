# Chapter 12 — Testing

> **You should finish this chapter able to:** write unit tests for suspending
> Vert.x code, integration tests with `vertx-junit5` + Testcontainers (real
> Postgres and Redis), HTTP tests using `WebClient`, gRPC tests using the
> generated client stub, and parallelize tests safely.

The first time you test an async Vert.x app you'll hit two surprises:

1. **Your assertion runs before the handler did.** JUnit returns and JUnit
   thinks the test passed, while the verticle is still running. You need
   a way to wait.
2. **State leaks between tests** because Vertx, Postgres, and Redis are all
   shared global resources. The fix is per-test isolation — Testcontainers
   spin up fresh services and `Vertx.vertx()` per test.

This chapter walks through the right pattern for each layer.

## 12.1  The dependencies

In our `code/full-app/pom.xml` test scope:

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-junit5</artifactId>
  <scope>test</scope>
</dependency>
<dependency>
  <groupId>org.junit.jupiter</groupId>
  <artifactId>junit-jupiter</artifactId>
  <scope>test</scope>
</dependency>
<dependency>
  <groupId>org.jetbrains.kotlinx</groupId>
  <artifactId>kotlinx-coroutines-test</artifactId>
  <scope>test</scope>
</dependency>
<dependency>
  <groupId>org.testcontainers</groupId>
  <artifactId>junit-jupiter</artifactId>
  <scope>test</scope>
</dependency>
<dependency>
  <groupId>org.testcontainers</groupId>
  <artifactId>postgresql</artifactId>
  <scope>test</scope>
</dependency>
<dependency>
  <groupId>org.assertj</groupId>
  <artifactId>assertj-core</artifactId>
  <scope>test</scope>
</dependency>
```

## 12.2  Pure-coroutine unit test

For domain code that only needs coroutines (no Vert.x event loop):

```kotlin
class UserServiceTest {
    @Test
    fun `get returns null when neither cache nor DB has user`() = runTest {
        val cache = FakeCache()
        val repo  = FakeRepo()
        val svc   = UserService(repo, cache)

        val result = svc.get("u-missing")

        assertThat(result).isNull()
    }

    @Test
    fun `get populates cache on DB hit`() = runTest {
        val cache = FakeCache()
        val repo  = FakeRepo().withUser(User("u-1", "a@b.c", "Alice", 0))
        val svc   = UserService(repo, cache)

        svc.get("u-1")

        assertThat(cache.snapshot()).containsKey("user:u-1")
    }
}
```

`runTest { }` from `kotlinx-coroutines-test` provides a synthetic
`TestScheduler` and a `StandardTestDispatcher`. `delay(1.hours)` returns
immediately, advancing the scheduler. Tests don't sleep wall-clock time.

When your code uses Vert.x APIs (`vertx.dispatcher()`, `coAwait()`), `runTest`
isn't enough — see the next section.

## 12.3  `vertx-junit5` extension

For tests that touch a real `Vertx`, use the extension:

```kotlin
@ExtendWith(VertxExtension::class)
class AppVerticleTest {

    @Test
    fun `health endpoint returns 200`(vertx: Vertx, ctx: VertxTestContext) {
        vertx.deployVerticle(AppVerticle())
            .compose { _ ->
                WebClient.create(vertx).get(8080, "localhost", "/healthz/live").send()
            }
            .onSuccess { resp ->
                ctx.verify {
                    assertThat(resp.statusCode()).isEqualTo(200)
                }
                ctx.completeNow()
            }
            .onFailure(ctx::failNow)
    }
}
```

`VertxTestContext` is the bridge between async code and JUnit. The test only
*passes* when you call `ctx.completeNow()`. `ctx.verify { ... }` captures
assertion failures and fails the test cleanly even if they happen on the
event loop.

### With coroutines

Use the `VertxExtension` plus a small helper:

```kotlin
fun vertxTest(vertx: Vertx, block: suspend CoroutineScope.() -> Unit) =
    runBlocking(vertx.dispatcher()) { block() }

@Test
fun `cache miss populates redis`(vertx: Vertx) = vertxTest(vertx) {
    val cache = RedisCache.create(vertx, testConfig.redis)
    val repo  = UserRepository.create(vertx, testConfig.postgres)
    val svc   = UserService(repo, cache)

    repo.insert("a@b.c", "Alice")
    svc.get(insertedId)

    assertThat(cache.getJson<User>("user:$insertedId")).isNotNull
}
```

`runBlocking(vertx.dispatcher())` puts the test body on the event loop so
`coAwait()` works. **Don't** wrap with raw `runBlocking { }` — you'd be on
the JUnit thread, and Vert.x APIs would throw "no current Context".

## 12.4  Testcontainers — real Postgres & Redis

The cardinal rule of integration tests: **don't mock the database**. Mocks
diverge from reality. Run a real Postgres for a few seconds per test class:

```kotlin
@Testcontainers
@ExtendWith(VertxExtension::class)
class UserRepositoryIT {
    companion object {
        @Container
        @JvmStatic
        val pg = PostgreSQLContainer<Nothing>("postgres:17-alpine").apply {
            withDatabaseName("app")
            withUsername("app")
            withPassword("app")
            withInitScript("db/migration/V1__schema.sql")
        }
    }

    @Test
    fun `insert and findById round-trip`(vertx: Vertx) = vertxTest(vertx) {
        val cfg = PostgresConfig(
            host = pg.host, port = pg.firstMappedPort,
            database = pg.databaseName, user = pg.username, password = pg.password,
            maxSize = 4, pipeliningLimit = 64,
        )
        val repo = UserRepository.create(vertx, cfg)
        try {
            val u = repo.insert("alice@b.c", "Alice")
            val read = repo.findById(u.id)
            assertThat(read).isEqualTo(u)
        } finally {
            repo.close()
        }
    }
}
```

`@Container` + `@JvmStatic` reuses the same Postgres for every test in the
class. For maximum isolation use `@Container` *without* `@JvmStatic` (one
container per test) — much slower.

For Redis:

```kotlin
@Container
@JvmStatic
val redis = GenericContainer<Nothing>("redis:8-alpine").apply {
    withExposedPorts(6379)
    waitingFor(Wait.forListeningPort())
}
```

Read its URI as `"redis://${redis.host}:${redis.firstMappedPort}"`.

### Reuse and parallelization

Testcontainers' `reuse` feature keeps containers running between Gradle/Maven
invocations:

```kotlin
val pg = PostgreSQLContainer<Nothing>("postgres:17-alpine").apply {
    withReuse(true)            // experimental but very fast
    withLabel("project", "vertx-tutorial")
}
```

Enable it in `~/.testcontainers.properties`:

```
testcontainers.reuse.enable=true
```

For parallel tests, give each test its own *database* in the shared container:

```kotlin
@BeforeEach
fun isolate() {
    val dbName = "test_${UUID.randomUUID().toString().replace("-", "")}"
    pg.execInContainer("psql", "-U", "app", "-c", "CREATE DATABASE $dbName")
    config = config.copy(database = dbName)
}
```

Postgres handles per-DB isolation with virtually zero cost compared to
spinning up new containers.

## 12.5  HTTP endpoint tests

`WebClient` is Vert.x's HTTP client; use it to hit your own server:

```kotlin
@Test
fun `POST users 201 then GET 200`(vertx: Vertx, ctx: VertxTestContext) {
    val client = WebClient.create(vertx)
    deployApp(vertx).compose {
        client.post(8080, "localhost", "/api/v1/users")
            .sendJson(JsonObject().put("email", "a@b.c").put("name", "Alice"))
    }.compose { post ->
        ctx.verify {
            assertThat(post.statusCode()).isEqualTo(201)
        }
        val id = post.bodyAsJsonObject().getString("id")
        client.get(8080, "localhost", "/api/v1/users/$id").send()
    }.onSuccess { get ->
        ctx.verify {
            assertThat(get.statusCode()).isEqualTo(200)
            assertThat(get.bodyAsJsonObject().getString("email")).isEqualTo("a@b.c")
        }
        ctx.completeNow()
    }.onFailure(ctx::failNow)
}
```

Or the coroutine-friendly version:

```kotlin
@Test
fun `POST then GET`(vertx: Vertx) = vertxTest(vertx) {
    val client = WebClient.create(vertx)
    deployApp(vertx).coAwait()

    val post = client.post(8080, "localhost", "/api/v1/users")
        .sendJson(JsonObject().put("email", "a@b.c").put("name", "Alice"))
        .coAwait()
    assertThat(post.statusCode()).isEqualTo(201)

    val id = post.bodyAsJsonObject().getString("id")
    val get = client.get(8080, "localhost", "/api/v1/users/$id").send().coAwait()
    assertThat(get.statusCode()).isEqualTo(200)
}
```

Bind to **port 0** in tests; let the OS pick. Read the actual port with
`server.actualPort()`. This lets tests run in parallel without port collisions:

```kotlin
val server = vertx.createHttpServer(opts.setPort(0)).requestHandler(router).listen().coAwait()
val port = server.actualPort()
```

## 12.6  gRPC endpoint tests

The codegen produces a Vert.x client stub. Use it directly:

```kotlin
@Test
fun `gRPC GetUser returns user`(vertx: Vertx) = vertxTest(vertx) {
    deployApp(vertx).coAwait()
    val inserted = createUserViaRest(vertx)

    val client = GrpcClient.client(vertx)
    val stub = VertxUserServiceGrpcClient(client,
        SocketAddress.inetSocketAddress(9090, "localhost"))

    val resp = stub.getUser(GetUserRequest.newBuilder().setId(inserted.id).build())
        .coAwait()

    assertThat(resp.user.email).isEqualTo(inserted.email)
}
```

For error paths:

```kotlin
@Test
fun `gRPC GetUser returns NOT_FOUND`(vertx: Vertx) = vertxTest(vertx) {
    deployApp(vertx).coAwait()
    val stub = VertxUserServiceGrpcClient(GrpcClient.client(vertx),
        SocketAddress.inetSocketAddress(9090, "localhost"))

    val ex = assertThrows<StatusRuntimeException> {
        runBlocking { stub.getUser(GetUserRequest.newBuilder().setId("missing").build()).coAwait() }
    }
    assertThat(ex.status.code).isEqualTo(Status.Code.NOT_FOUND)
}
```

## 12.7  Per-test isolation pattern

A small `BaseIT` for the whole project:

```kotlin
@Testcontainers
@ExtendWith(VertxExtension::class)
abstract class BaseIT {
    companion object {
        @Container @JvmStatic val pg = PostgreSQLContainer<Nothing>("postgres:17-alpine")
        @Container @JvmStatic val redis = GenericContainer<Nothing>("redis:8-alpine")
            .withExposedPorts(6379)
    }

    protected lateinit var vertx: Vertx
    protected lateinit var config: AppConfig
    protected lateinit var deploymentId: String

    @BeforeEach
    fun setup() = runBlocking {
        vertx = Vertx.vertx()
        config = AppConfig(
            http = HttpServerConfig(port = 0),
            grpc = GrpcServerConfig(port = 0),
            postgres = PostgresConfig(
                host = pg.host, port = pg.firstMappedPort,
                database = pg.databaseName, user = pg.username, password = pg.password,
                maxSize = 4, pipeliningLimit = 64,
            ),
            redis = RedisConfig(uri = "redis://${redis.host}:${redis.firstMappedPort}", maxPoolSize = 4),
        )
        deploymentId = vertx.deployVerticle({ AppVerticle(config) }, DeploymentOptions().setInstances(1))
            .coAwait()
    }

    @AfterEach
    fun teardown() = runBlocking {
        vertx.close().coAwait()
        truncateTables(pg)
        redis.execInContainer("redis-cli", "FLUSHALL")
    }
}
```

Two key choices:

- **Fresh `Vertx` per test.** Verticle state, event-loop pools, contexts all
  reset. Tests can't bleed.
- **Truncate / flush after each test.** Faster than dropping the DB.

## 12.8  Test pyramid for a Vert.x service

```
                       ┌────────────────┐
                       │  e2e: smoke    │   ← 1-2 tests, against
                       │  & contract    │     real deployed service
                       └────────────────┘
                  ┌────────────────────────┐
                  │  Integration (per IT)  │   ← happy path + edge cases for
                  │  full stack, real DB   │     each REST/gRPC endpoint
                  └────────────────────────┘
        ┌──────────────────────────────────────────┐
        │  Unit tests                              │   ← bulk of tests
        │  pure coroutines, fakes for I/O          │     domain logic
        └──────────────────────────────────────────┘
```

The unit layer is where you have **dozens of tests per service**. Integration
tests cover wiring (validation, error mapping, DB constraints, cache
semantics). E2E is for production sanity — usually just 1-2 smoke tests.

## 12.9  Reliability and flake avoidance

**Don't use wall-clock waits.** `Thread.sleep(500)` in a test means either
you're hiding a race condition or you're going to wait 500ms for nothing.
Wait on an event:

```kotlin
suspend fun waitForCondition(timeoutMs: Long = 5_000, check: suspend () -> Boolean) {
    val deadline = System.nanoTime() + timeoutMs.toMs(NANOS)
    while (System.nanoTime() < deadline) {
        if (check()) return
        delay(50)
    }
    fail("condition not met within ${timeoutMs}ms")
}
```

**Pin all versions.** Testcontainers versioning is sensitive; lock it.

**Always close `Vertx`, `WebClient`, `GrpcClient` in teardown.** Leaks
manifest as "test passes the first time, fails on second run" because the
prior run still holds a port.

**Don't share the test data set.** A test that inserts users and another
that lists users will interact. Use distinct IDs per test (UUID at setup).

## 12.10  Performance: parallel test execution

JUnit 5 supports parallel by config:

```properties
# junit-platform.properties in src/test/resources
junit.jupiter.execution.parallel.enabled=true
junit.jupiter.execution.parallel.mode.default=concurrent
junit.jupiter.execution.parallel.config.strategy=dynamic
```

For this to work, *every test must be isolated*. Combine with:

- Port 0 for servers.
- UUID-suffixed table prefixes per test (advanced) or per-test DB.
- Independent `Vertx` instances (we already do this in `BaseIT`).

In our experience, even on a 10-core dev laptop, you get a 4-5× speedup with
parallelization on a medium test suite. CI usually limits concurrency
explicitly because of Testcontainers' Docker socket fan-in.

## 12.11  Common pitfalls

**`runBlocking` inside a Vert.x event loop.** It deadlocks. Use
`runBlocking(vertx.dispatcher())` if you really need to block at the test
boundary; otherwise convert to `VertxTestContext.completeNow()`.

**`@BeforeAll` to deploy the app, `@AfterEach` to clear DB.** Then tests
share an event loop pool. Hard to reason about. Prefer per-test deployment.

**Forgetting to consume the response body in `WebClient`.** The connection
stays in the pool half-open. After 100 tests you've exhausted file
descriptors.

**Asserting on log output.** Logs are noisy. Assert on actual behavior
(HTTP status, DB rows, cache entries) instead.

**Comparing `Instant.now()` strict-equals.** Tests of "createdAt is correct"
fail because nanoseconds differ. Compare with tolerance: `within(1, SECONDS)`.

## 12.12  Try it

1. Add a parameterized test (`@ParameterizedTest @CsvSource`) that hits
   `POST /api/v1/users` with 10 different `(email, name)` pairs and verifies
   the returned ID is a valid UUID.
2. Write a test that simulates Redis being down: stop the Redis container
   mid-test, hit `GET /api/v1/users/:id`, verify the service still responds
   (degraded — DB lookup only).
3. Implement contract testing: snapshot one `openapi.yaml` request/response
   against the live server and fail the build if the response shape drifts.

[← Ch 11](11-configuration.md) · [Next: Chapter 13 — Docker & JVM tuning →](13-docker-jvm.md)
