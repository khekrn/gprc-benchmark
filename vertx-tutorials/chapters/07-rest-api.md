# Chapter 7 — REST API with Vert.x Web

> **You should finish this chapter able to:** structure a production REST API
> with `Router`, validate inputs with `vertx-web-validation`, expose an
> OpenAPI 3.1 contract, return RFC 7807 problem+json errors, negotiate content
> types, version your API, and add middleware in the right order.

Vert.x Web is the routing/middleware layer on top of `vertx-core`'s
`HttpServer`. It is small (about 50 KB), opinionated where it needs to be,
and stays out of the way otherwise. There is **no annotation scanning, no
classpath magic, no `@RestController`** — you wire routes explicitly in code.

This chapter walks through everything you need for a real-world REST service:
routing, body parsing, validation, OpenAPI contract-first / code-first, error
mapping, pagination, content negotiation, security headers, and CORS.

## 7.1  Anatomy of a Vert.x Web request

```
TCP byte    →  Netty pipeline (decode bytes → HttpRequest)
              ↓
            Vert.x HttpServer
              ↓
            Router
              ↓
            ┌──── matched route(s) ────┐
            │   handler 1 (logger)     │
            │   handler 2 (body)       │
            │   handler 3 (auth)       │
            │   handler 4 (validation) │
            │   handler 5 (your code)  │      → suspending business logic
            └──────────────────────────┘
              ↓
            ResponseWriter → Netty pipeline (encode → bytes) → TCP
```

A `Route` is a *pattern* matched against the request. Each route can register
one or more handlers. Handlers form a chain: a handler must call `ctx.next()`
(to pass through), `ctx.json(...)` / `ctx.response().end(...)` (to write a
response), or `ctx.fail(...)` (to delegate to an error handler). Failing to
call any of these leaves the request hanging — `TimeoutHandler` saves you.

## 7.2  Building the router (the production pattern)

Our actual REST router lives in
[`HttpServerFactory.kt`](../code/full-app/src/main/kotlin/com/example/app/http/HttpServerFactory.kt#L37).
For this chapter, expand it to the structure a real service uses:

```kotlin
fun buildRestRouter(vertx: Vertx, users: UserService): Router {
    val router = Router.router(vertx)

    // ===== 1. Global middleware (order matters!) =====
    router.route()
        .handler(ResponseTimeHandler.create())                // adds X-Response-Time
        .handler(accessLog())                                  // our JSON access log
        .handler(requestIdHandler())                           // sets MDC / response header
        .handler(securityHeaders())                            // X-Content-Type-Options, etc.
        .handler(corsHandler())                                // permissive for /api/*
        .handler(BodyHandler.create()
            .setBodyLimit(256 * 1024)                          // 256 KB max body
            .setHandleFileUploads(false))                      // we don't accept multipart
        .handler(TimeoutHandler.create(5_000))                 // hard 5s per request

    // ===== 2. Operational endpoints (no auth, no body parsing) =====
    router.get("/metrics").handler(PrometheusScrapingHandler.create())
    router.get("/healthz/live").handler { ctx -> ctx.response().setStatusCode(200).end("OK") }
    router.get("/healthz/ready").handler(readinessHandler(users))

    // ===== 3. Versioned API =====
    val v1 = Router.router(vertx)
    router.mountSubRouter("/api/v1", v1)
    UsersRoutes(users).register(v1)
    OrdersRoutes(orders).register(v1)

    // ===== 4. Error handling =====
    router.errorHandler(404) { problemJson(it, 404, "not_found", "no route matches ${it.request().path()}") }
    router.errorHandler(500) { problemJson(it, 500, "internal", "see x-request-id") }

    return router
}
```

Key choices:

1. **Global middleware before sub-routers.** A sub-router does not re-enter
   the parent's handlers. Anything you want for *every* request goes on the
   parent `router.route()`.
2. **`mountSubRouter` per version.** When you ship `/api/v2`, you can mount a
   completely separate router (different validators, different DTOs) without
   touching v1.
3. **Operational endpoints outside the sub-router.** `/metrics`,
   `/healthz/*` should not require auth and should not parse bodies. Putting
   them on the parent router before the sub-router mount accomplishes this.

## 7.3  Splitting routes into modules

For anything beyond a single endpoint, group routes per resource:

```kotlin
class UsersRoutes(private val users: UserService) {
    fun register(router: Router) {
        router.coroutineRouter {
            route(HttpMethod.GET,    "/users/:id").coHandler(::get)
            route(HttpMethod.POST,   "/users").coHandler(::create)
            route(HttpMethod.GET,    "/users").coHandler(::list)
            route(HttpMethod.DELETE, "/users/:id").coHandler(::delete)
        }
    }

    private suspend fun get(ctx: RoutingContext) {
        val id = ctx.pathParam("id")
        val user = users.get(id) ?: return ctx.notFound("user $id")
        ctx.json(JsonObject.mapFrom(user))
    }

    private suspend fun create(ctx: RoutingContext) {
        val body = ctx.body().asJsonObject()
        val u = users.create(
            email = body.getString("email"),
            name  = body.getString("name"),
        )
        ctx.response()
            .setStatusCode(201)
            .putHeader("Location", "/api/v1/users/${u.id}")
            .end(JsonObject.mapFrom(u).encode())
    }
    // ... list, delete
}

// Extension to make 404 ergonomic
private fun RoutingContext.notFound(detail: String) =
    problemJson(this, 404, "not_found", detail)
```

Each resource module owns *its* routes; the central `buildRestRouter` only
sees one line per module. This scales to hundreds of endpoints without the
single huge router file you see in many Vert.x projects.

## 7.4  Validation: `vertx-web-validation`

Catching `email is null` in your handler is too late. Use the validation
module to short-circuit invalid requests *before* they reach business code:

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-web-validation</artifactId>
</dependency>
```

```kotlin
import io.vertx.json.schema.*
import io.vertx.ext.web.validation.builder.ValidationHandlerBuilder
import io.vertx.ext.web.validation.builder.Bodies.json

val schemas = SchemaRepository.create(JsonSchemaOptions()
    .setDraft(Draft.DRAFT202012)
    .setBaseUri("https://example.com/api/v1"))

val createUserSchema = JsonSchema.of(
    JsonObject()
        .put("type", "object")
        .put("required", JsonArray().add("email").add("name"))
        .put("additionalProperties", false)
        .put("properties", JsonObject()
            .put("email", JsonObject().put("type", "string").put("format", "email"))
            .put("name",  JsonObject().put("type", "string").put("minLength", 1).put("maxLength", 100)))
)

val createUserValidator = ValidationHandlerBuilder.create(schemas)
    .body(json(createUserSchema))
    .build(vertx)

router.coroutineRouter {
    route(HttpMethod.POST, "/users").handler(createUserValidator).coHandler { ctx ->
        val params = ctx.get<RequestParameters>("parsedParameters")
        val body = params.body().jsonObject
        // body is GUARANTEED valid here
        val u = users.create(body.getString("email"), body.getString("name"))
        ctx.response().setStatusCode(201).end(JsonObject.mapFrom(u).encode())
    }
}
```

When validation fails, the handler chain is short-circuited and a
`BodyProcessorException` is thrown. Map it to a 400:

```kotlin
router.errorHandler(400) { ctx ->
    val cause = ctx.failure()
    val detail = when (cause) {
        is BodyProcessorException -> cause.message
        is BadRequestException    -> cause.message
        else                      -> "bad request"
    }
    problemJson(ctx, 400, "validation_failed", detail ?: "")
}
```

For larger schemas, keep them in `src/main/resources/schemas/*.json` and load
once at startup. Don't re-parse the schema on every request.

## 7.5  OpenAPI 3.1 — contract first

`vertx-web-openapi-router` builds routers *directly from an OpenAPI 3.1
document*. The flow:

```
openapi.yaml  →  ContractRouterBuilder  →  Router with all routes registered
                                            and validation auto-wired
```

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-web-openapi-router</artifactId>
</dependency>
```

Place your spec at `src/main/resources/openapi.yaml`:

```yaml
openapi: "3.1.0"
info:
  title: full-app
  version: "1.0.0"
paths:
  /users/{id}:
    get:
      operationId: getUser
      parameters:
        - { name: id, in: path, required: true, schema: { type: string } }
      responses:
        "200":
          content: { application/json: { schema: { $ref: "#/components/schemas/User" } } }
        "404":
          content: { application/problem+json: { schema: { $ref: "#/components/schemas/Problem" } } }
components:
  schemas:
    User: { type: object, required: [id, email, name], properties: { ... } }
    Problem: { type: object, properties: { type: { type: string }, title: { type: string }, status: { type: integer } } }
```

Wire it up:

```kotlin
val contract = OpenAPIContract.from(vertx, "openapi.yaml").coAwait()
val routerBuilder = RouterBuilder.create(vertx, contract)

routerBuilder.operation("getUser").handler { ctx ->
    val id = ctx.pathParam("id")
    vertxFuture { users.get(id) }
        .onSuccess { user ->
            if (user == null) problemJson(ctx, 404, "not_found", "user $id")
            else ctx.json(JsonObject.mapFrom(user))
        }
        .onFailure(ctx::fail)
}

val router = routerBuilder.createRouter()
```

The benefits:

- **Contract is the source of truth.** Frontend teams generate clients from
  the same `openapi.yaml`. Code can't drift.
- **Built-in validation.** Path params, query params, request bodies are all
  validated against the spec. You implement only `operation("...").handler`.
- **Static documentation.** Mount a Swagger UI in front of the spec and you
  have docs for free.

When *not* to use it: bigger services with many bespoke routes (auth, file
uploads, server-sent events) where the OpenAPI dialect is awkward. We use a
hybrid: contract-first for the public surface, code-first internal.

## 7.6  RFC 7807 problem+json — proper error responses

REST error bodies are an underspecified disaster. RFC 7807 fixes that with a
small standard schema:

```json
{
  "type":   "https://example.com/errors/not_found",
  "title":  "Resource not found",
  "status": 404,
  "detail": "user u-42 does not exist",
  "instance": "/api/v1/users/u-42",
  "trace_id": "01HXY9..."
}
```

A reusable helper:

```kotlin
fun problemJson(ctx: RoutingContext, status: Int, type: String, detail: String) {
    val title = when (status) {
        400 -> "Bad request"
        401 -> "Unauthorized"
        403 -> "Forbidden"
        404 -> "Not found"
        409 -> "Conflict"
        429 -> "Too many requests"
        500 -> "Internal server error"
        else -> "Error"
    }
    val body = JsonObject()
        .put("type", "https://example.com/errors/$type")
        .put("title", title)
        .put("status", status)
        .put("detail", detail)
        .put("instance", ctx.request().path())
        .put("trace_id", ctx.request().getHeader("x-request-id"))
    ctx.response()
        .setStatusCode(status)
        .putHeader("Content-Type", "application/problem+json")
        .end(body.encode())
}
```

Use it for *every* non-2xx response. Once your error format is consistent,
clients can render meaningful messages without N format-specific branches.

## 7.7  Content negotiation

```kotlin
router.route("/api/v1/users/:id").produces("application/json").produces("application/cbor")
    .coHandler { ctx ->
        val user = users.get(ctx.pathParam("id")) ?: return@coHandler ctx.notFound("…")
        when (ctx.getAcceptableContentType()) {
            "application/cbor" -> ctx.response().putHeader("content-type", "application/cbor")
                                  .end(Buffer.buffer(CborMapper.encode(user)))
            else -> ctx.json(JsonObject.mapFrom(user))
        }
    }
```

`produces(...)` populates the route's matcher. If the client's `Accept`
header doesn't match any of them, Vert.x returns 406 Not Acceptable for free.
Almost no public APIs need this — but if you serve mobile clients with binary
payloads (Protobuf, CBOR, MessagePack), it's a clean pattern.

## 7.8  Pagination and filtering

A trivial pagination pattern that compose well with `Pool`:

```kotlin
data class Page<T>(val items: List<T>, val nextCursor: String?)

private suspend fun list(ctx: RoutingContext) {
    val cursor = ctx.queryParam("cursor").firstOrNull()
    val limit  = ctx.queryParam("limit").firstOrNull()?.toIntOrNull()?.coerceIn(1, 100) ?: 25
    val page   = users.list(cursor, limit)
    val body   = JsonObject()
        .put("items", JsonArray(page.items.map { JsonObject.mapFrom(it) }))
        .put("next_cursor", page.nextCursor)
    ctx.json(body)
}
```

Use cursor-based pagination ("keyset"), not offset/limit. Postgres LIMIT 100
OFFSET 100000 is O(N+offset), gets slower per page. Cursor stays O(N).
Chapter 8 has the SQL.

## 7.9  CORS, security headers, compression

```kotlin
private fun corsHandler() = CorsHandler.create()
    .addOrigin("https://app.example.com")
    .allowedMethod(HttpMethod.GET)
    .allowedMethod(HttpMethod.POST)
    .allowedMethod(HttpMethod.DELETE)
    .allowedHeader("authorization")
    .allowedHeader("content-type")
    .allowedHeader("x-request-id")
    .allowCredentials(true)
    .maxAgeSeconds(3600)

private fun securityHeaders() = Handler<RoutingContext> { ctx ->
    ctx.response()
        .putHeader("X-Content-Type-Options", "nosniff")
        .putHeader("X-Frame-Options", "DENY")
        .putHeader("Referrer-Policy", "no-referrer")
        .putHeader("Strict-Transport-Security", "max-age=63072000")
    ctx.next()
}
```

Compression is one line on the *server*:

```kotlin
HttpServerOptions().setCompressionSupported(true)   // see HttpServerFactory.kt
```

Vert.x will gzip / deflate / brotli responses based on `Accept-Encoding`.
Compression *is* CPU work — fine for JSON >1 KB, possibly harmful for tiny
payloads. Set `setCompressionContentSizeThreshold(1024)` to skip small ones.

## 7.10  Authentication: JWT example

Auth is application-specific, so just one example:

```kotlin
val jwt = JWTAuth.create(vertx, JWTAuthOptions()
    .addPubSecKey(PubSecKeyOptions()
        .setAlgorithm("RS256")
        .setBuffer(jwtPublicKey)))

v1.route("/users/*").handler(JWTAuthHandler.create(jwt))   // any sub-route requires JWT
```

`JWTAuthHandler` populates `ctx.user()` with verified claims. In your
business handler:

```kotlin
val userId = ctx.user().principal().getString("sub")
```

Health and metrics endpoints are deliberately outside this sub-router, so they
remain accessible to load balancers and Prometheus.

## 7.11  Connection limits and back-pressure

Vert.x doesn't apply request-level back-pressure automatically. If a client
sends 1 GB of body, BodyHandler stores it in memory unless you cap it. Two
defenses:

```kotlin
HttpServerOptions()
    .setMaxFormAttributeSize(8 * 1024)
    .setMaxHeaderSize(16 * 1024)
    .setMaxInitialLineLength(4 * 1024)
    .setIdleTimeout(30)                        // close idle conns after 30s
    .setIdleTimeoutUnit(TimeUnit.SECONDS)

BodyHandler.create().setBodyLimit(256 * 1024)  // per-route
```

For per-IP rate limits, use `vertx-throttling` or front the service with an
ingress that does it (Envoy, nginx, AWS ALB).

## 7.12  Putting it all together — a complete handler

```kotlin
private suspend fun create(ctx: RoutingContext) {
    val body = ctx.body().asJsonObject() ?: return ctx.problem(400, "missing_body")
    val email = body.getString("email") ?: return ctx.problem(400, "email_required")
    val name  = body.getString("name")  ?: return ctx.problem(400, "name_required")
    try {
        val u = users.create(email, name)
        ctx.response()
            .setStatusCode(201)
            .putHeader("Location", "/api/v1/users/${u.id}")
            .putHeader("Content-Type", "application/json")
            .end(JsonObject.mapFrom(u).encode())
    } catch (e: DuplicateEmailException) {
        ctx.problem(409, "duplicate_email", "email $email already exists")
    }
}

private fun RoutingContext.problem(status: Int, type: String, detail: String = "") =
    problemJson(this, status, type, detail)
```

Each handler is 15 lines, top-to-bottom readable, no nested callbacks, and
clean error mapping at the boundary.

## 7.13  Common mistakes

**Forgetting `ctx.next()`.** A handler that doesn't end the response *and*
doesn't call `next()` hangs forever. Always do one or the other.

**Re-creating `Router` per request.** `Router.router(vertx)` is *expensive*.
Build it once in `start()`. The handler is what runs per request.

**Putting Auth on `router.route()` instead of a sub-router.** Now `/metrics`
demands an auth token and Prometheus is broken. Always mount auth on the
*right* sub-router.

**Mutating the `RoutingContext` data after `next()`.** Once you call `next`,
the next handler may already have started — or finished. Treat handlers as
sequential, but don't assume "still alive" semantics outside your own scope.

**Using `BodyHandler` for streaming uploads.** It buffers the entire body
into memory. For uploads, use the lower-level
`HttpServerRequest.handler { buf -> ... }` and stream to a file or upstream.

## 7.14  Try it

1. Add a `PUT /api/v1/users/:id` route that performs a partial update via
   `JSON Merge Patch` (RFC 7396). Validate the body with a schema.
2. Mount Swagger UI at `/openapi/` so curl-less developers can play with the
   contract.
3. Implement an idempotency-key middleware: if `Idempotency-Key: xyz` is sent
   and we have a stored response for it, replay; otherwise capture the
   response and store it in Redis with a TTL.

[← Ch 6](06-logging.md) · [Next: Chapter 8 — PostgreSQL with vertx-pg-client →](08-postgresql.md)
