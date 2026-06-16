# Chapter 18 — Testing strategies

> You will know how to test a coroutine-based handler without spinning
> up a Vert.x instance, how to integration-test the repository against
> a real Postgres via Testcontainers, and how to drive REST + gRPC
> end-to-end with a single test harness.

## 18.1 The pyramid we actually use

```
        ┌──────────────────────────────┐
        │   1× end-to-end smoke test   │     full app, REST + gRPC
        ├──────────────────────────────┤
        │     N× integration tests     │     repo + real PG (Testcontainers)
        ├──────────────────────────────┤
        │      M× unit tests           │     service + fake repo
        └──────────────────────────────┘
```

- **Unit** is cheap and fast. Run on every change.
- **Integration** catches the SQL bugs unit tests can't.
- **End-to-end** catches wiring bugs (config, ports, codegen). Run on
  every PR, not every save.

## 18.2 Unit testing a service with a fake repo

`UserService` only knows about `UserRepository`. Make a small in-memory
fake:

```kotlin
class FakeUserRepository : UserRepository {
    private val byId = ConcurrentHashMap<Long, User>()
    private val idGen = AtomicLong()
    override suspend fun findById(id: Long): User? = byId[id]
    override suspend fun findByEmail(email: String): User? = byId.values.find { it.email == email }
    override suspend fun create(input: NewUser): User =
        User(idGen.incrementAndGet(), input.email, input.fullName, OffsetDateTime.now())
            .also { byId[it.id] = it }
    override fun streamAll(prefix: String?): Flow<User> = byId.values.toList().asFlow()
    override suspend fun createMany(inputs: List<NewUser>) = inputs.map { create(it) }
}
```

(For this to work make `UserRepository` an `interface` with the same
method signatures. We left it concrete — promote when you need a fake.)

```kotlin
class UserServiceTest {
    @Test fun `createIfMissing returns existing`() = runTest {
        val repo = FakeUserRepository()
        repo.create(NewUser("a@x.io", "Alice"))
        val svc = UserService(repo)
        assertThatThrownBy { runBlocking { svc.create(NewUser("a@x.io", "Bob")) } }
            .isInstanceOf(UserError.DuplicateEmail::class.java)
    }
}
```

`runTest` is from `kotlinx-coroutines-test`. It uses a virtual time
dispatcher that skips `delay` and reports unfinished coroutines.

## 18.3 Repository integration test with Testcontainers

```kotlin
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class UserRepositoryIT {
    private val pg = PostgreSQLContainer("postgres:17-alpine")
        .withDatabaseName("appdb").withUsername("app").withPassword("app")
    private lateinit var vertx: Vertx
    private lateinit var pool: Pool
    private lateinit var repo: UserRepository

    @BeforeAll fun setUp() = runTest {
        pg.start()
        vertx = Vertx.vertx()
        val opts = PgConnectOptions()
            .setHost(pg.host).setPort(pg.firstMappedPort)
            .setDatabase("appdb").setUser("app").setPassword("app")
        pool = PgBuilder.pool().with(PoolOptions().setMaxSize(4))
            .connectingTo(opts).using(vertx).build()
        DbMigrator.migrate(pool)
        repo = UserRepository(pool)
    }
    @AfterAll fun tearDown() = runTest {
        pool.close().coAwait(); vertx.close().coAwait(); pg.stop()
    }
    // tests …
}
```

This is `code/full-app/src/test/kotlin/com/example/app/UserRepositoryIT.kt`.

Key choices:

- **One container per test class.** Starting Postgres per test method
  is too slow.
- **`PER_CLASS` lifecycle** so `@BeforeAll`/`@AfterAll` can be
  instance methods.
- **`runTest`** to drive the coroutine; we don't use the virtual time
  here because real DB calls happen on real time.

## 18.4 End-to-end testing the REST API

Use **REST Assured** (we added it as a test dep):

```kotlin
@TestInstance(TestInstance.Lifecycle.PER_CLASS)
class AppE2ETest {
    private val pg = PostgreSQLContainer("postgres:17-alpine")…
    private lateinit var vertx: Vertx

    @BeforeAll fun setUp() {
        pg.start()
        System.setProperty("db.host", pg.host)
        System.setProperty("db.port", pg.firstMappedPort.toString())
        vertx = Vertx.vertx()
        runBlocking {
            val cfg = AppConfig.load(vertx).coAwait()
            vertx.deployVerticle(AppVerticle(), DeploymentOptions().setConfig(cfg.raw)).coAwait()
        }
        RestAssured.baseURI = "http://localhost:${cfg.http.port}"
    }
    @AfterAll fun tearDown() = runBlocking { vertx.close().coAwait(); pg.stop() }

    @Test fun `create and get`() {
        val id = RestAssured.given().contentType("application/json")
            .body("""{"email":"a@x.io","fullName":"Alice"}""")
            .post("/api/users").then().statusCode(201).extract().path<Int>("id")
        RestAssured.get("/api/users/$id").then().statusCode(200)
            .body("email", Matchers.equalTo("a@x.io"))
    }
}
```

A single end-to-end test like this catches: routing typos, JSON
encoding mismatches, status code regressions, header bugs.

## 18.5 End-to-end testing a gRPC RPC

```kotlin
@Test fun `gRPC unary GetUser`() = runBlocking {
    val client = UsersGrpcClient.create(
        GrpcClient.client(vertx),
        SocketAddress.inetSocketAddress(cfg.grpc.port, "localhost")
    )
    val reply = client.getUser(GetUserRequest.newBuilder().setId(1).build()).coAwait()
    assertThat(reply.email).isEqualTo("a@x.io")
}
```

The Kotlin client is the cleanest way; `grpcurl` works for ad-hoc.

## 18.6 Streaming tests

Pick one test per shape:

```kotlin
@Test fun `gRPC server-streaming ListUsers ends`() = runBlocking {
    val out = mutableListOf<UserReply>()
    val stream = client.listUsers(ListUsersRequest.getDefaultInstance()).coAwait()
    val done = CompletableDeferred<Unit>()
    stream.handler { out.add(it) }
    stream.endHandler { done.complete(Unit) }
    stream.exceptionHandler { done.completeExceptionally(it) }
    done.await()
    assertThat(out).isNotEmpty
}
```

## 18.7 Things to avoid

- **Real network sleeps.** Tests should not rely on `Thread.sleep(100)`.
- **Hidden ordering.** Don't assume rows come back in insert order
  unless `ORDER BY`.
- **Shared mutable state across tests.** Either reset the DB between
  tests or use a unique email per test.
- **Coverage targets >80 % on integration**. Coverage is a smell, not
  a goal. Focus on important paths.

## 18.8 Exercises

1. Add a flaky test by reading rows without `ORDER BY` and asserting
   the first. Watch it flake. Fix it.
2. Wrap the `AppE2ETest` rig in a JUnit 5 `Extension` so a second test
   class doesn't restart Postgres.
3. Add a "client-streaming" e2e test that uploads 1000 users.

---

[← Chapter 17](17-performance.md) · [Next → Chapter 19: Vert.x vs Virtual Threads](19-vertx-vs-virtual-threads.md)
