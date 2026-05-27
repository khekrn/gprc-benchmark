# Chapter 10 — gRPC Unary Services

> **You should finish this chapter able to:** write a `.proto` file with the
> right conventions, configure Maven to generate Vert.x-flavored stubs, bind
> a server on HTTP/2 (`h2c` or TLS), write a client that pools connections,
> handle deadlines and cancellation, map gRPC status codes to domain errors,
> and reason about the wire-protocol cost of fields.

gRPC is **the** RPC protocol for service-to-service communication: it is
fast (HTTP/2 binary framing), strongly typed (Protobuf), language-agnostic
(15+ official codegens), supports streaming, and gives you out-of-the-box
deadlines, cancellation, and metadata.

Vert.x 5 ships **`vertx-grpc-server`** and **`vertx-grpc-client`** — a
native, idiomatic implementation that runs *directly on Vert.x's HTTP/2
stack*. This is **not** the `grpc-java` compatibility shim. It is faster
(no thread hops between the gRPC layer and Vert.x), smaller (no Netty
duplication), and exposes Vert.x `Future<T>` everywhere.

## 10.1  What is gRPC, really?

The TL;DR of the wire protocol:

```
HTTP/2 stream (one per RPC call)
   │
   ├── HEADERS  :path=/UserService/GetUser, content-type=application/grpc
   ├── HEADERS  grpc-timeout=5S, x-trace=...
   ├── DATA     [1-byte compression flag][4-byte length][protobuf bytes]
   └── HEADERS  grpc-status=0, grpc-message=
```

For *unary* calls (one request, one response):

1. Client opens an HTTP/2 stream.
2. Client sends HEADERS with the RPC name in `:path`.
3. Client sends DATA with the serialized request.
4. Server sends DATA with the serialized response.
5. Server sends trailing HEADERS with `grpc-status`.

For *server streaming*: server sends multiple DATA frames before the trailer.
For *client streaming*: multiple DATA from the client. For *bidi*: both.

In Vert.x, every RPC is one *coroutine* on an event loop's Context — same
shape as an HTTP request.

## 10.2  Protobuf — the contract

Our [`user.proto`](../code/full-app/src/main/proto/user.proto):

```protobuf
syntax = "proto3";

package com.example.app.grpc.v1;

option java_multiple_files = true;
option java_package = "com.example.app.grpc.v1";
option java_outer_classname = "UserProto";

service UserService {
    rpc GetUser(GetUserRequest)       returns (GetUserResponse);
    rpc CreateUser(CreateUserRequest) returns (CreateUserResponse);
}

message GetUserRequest {
    string id = 1;
}

message GetUserResponse {
    User user = 1;
}

message CreateUserRequest {
    string email = 1;
    string name  = 2;
}

message CreateUserResponse {
    User user = 1;
}

message User {
    string id    = 1;
    string email = 2;
    string name  = 3;
    int64  created_at_epoch_millis = 4;

    // reserved 5;     // example of removing a field
    // reserved "age"; // example of removing a field name
}
```

### Proto conventions — the rules you must internalize

1. **Field numbers are eternal.** Once you ship a `.proto`, never reuse a
   number for a different purpose. Add `reserved 5;` to lock the slot if you
   remove a field. Servers that don't know the new field skip it; servers
   that don't know the *old* field at that slot would misinterpret bytes.
2. **Field numbers 1–15 use one byte on the wire.** 16–2047 use two. So
   reserve 1–15 for the most-frequent fields.
3. **`syntax = "proto3"`** means scalar fields don't distinguish "unset"
   from "default" (a `string` is `""` if unset). Use `optional` (yes, it's
   back in proto3) when you need *presence semantics*:
   ```
   message User { optional string nickname = 7; }
   ```
4. **`java_multiple_files = true`** generates one Java class per
   message/service instead of a giant outer class. Required for sane IDE
   navigation.
5. **Wrap messages in a package**, prefix with `v1`. When v2 ships, you can
   live them side-by-side: `com.example.app.grpc.v2`.
6. **No primitives in public APIs.** Use a wrapper or a richer message when
   the field might mean "unknown" vs "zero". `google.protobuf.Int64Value`
   distinguishes "0" from "absent".
7. **Use `google.protobuf.Timestamp`** for absolute times. Don't roll
   epoch-millis types unless you know every consumer accepts them. (Our
   demo uses `int64` for simplicity.)
8. **Use `Empty` for void-like RPCs**, not custom empty messages:
   ```
   import "google/protobuf/empty.proto";
   rpc Ping(google.protobuf.Empty) returns (google.protobuf.Empty);
   ```

### Backward compatibility cheat sheet

| Change | Safe? |
|---|---|
| Add new field with a new number | ✅ |
| Remove a field and `reserved` it | ✅ |
| Rename a field (same number, same type) | ✅ (on wire) — but breaks any code generated from it |
| Change a field's type | ❌ |
| Reuse a freed field number | ❌ (silent data corruption) |
| Make `optional` field required | ❌ |
| Change `repeated` to scalar | ❌ |

## 10.3  Maven codegen with `protobuf-maven-plugin`

In our [`code/full-app/pom.xml`](../code/full-app/pom.xml):

```xml
<plugin>
  <groupId>org.xolstice.maven.plugins</groupId>
  <artifactId>protobuf-maven-plugin</artifactId>
  <version>0.6.1</version>
  <configuration>
    <protocArtifact>com.google.protobuf:protoc:${protoc.version}:exe:${os.detected.classifier}</protocArtifact>
    <pluginId>vertx-grpc</pluginId>
    <pluginArtifact>io.vertx:vertx-grpc-protoc-plugin2:${vertx.version}</pluginArtifact>
    <outputDirectory>${project.build.directory}/generated-sources/protobuf</outputDirectory>
    <clearOutputDirectory>false</clearOutputDirectory>
  </configuration>
  <executions>
    <execution>
      <goals>
        <goal>compile</goal>
        <goal>compile-custom</goal>
      </goals>
    </execution>
  </executions>
</plugin>
```

The two goals:

- **`compile`** runs `protoc` against `src/main/proto/*.proto` and emits
  the standard Java message classes (`User`, `GetUserRequest`, etc.).
- **`compile-custom`** runs `protoc` again with the
  `vertx-grpc-protoc-plugin2` plugin, generating Vert.x-flavored stubs:
  - `VertxUserServiceGrpcServer` — server-side helpers and `UserServiceApi`
    interface.
  - `VertxUserServiceGrpcClient` — client-side typed `Future<T>` stub.

We also need `os-maven-plugin` as an `extension` so the `${os.detected.classifier}`
property resolves (the right native `protoc` binary for the build machine):

```xml
<extensions>
  <extension>
    <groupId>kr.motd.maven</groupId>
    <artifactId>os-maven-plugin</artifactId>
    <version>1.7.1</version>
  </extension>
</extensions>
```

Generated sources land in `target/generated-sources/protobuf/{java,grpc-vertx-grpc}`,
which `build-helper-maven-plugin` (also wired in `pom.xml`) adds as a source
root so Kotlin's compiler sees the generated Java classes.

After `mvn compile`, you can navigate to:

```
target/generated-sources/protobuf/grpc-vertx-grpc/com/example/app/grpc/v1/
  VertxUserServiceGrpcServer.java
  VertxUserServiceGrpcClient.java
```

## 10.4  The server implementation

[`UserGrpcService.kt`](../code/full-app/src/main/kotlin/com/example/app/grpc/UserGrpcService.kt):

```kotlin
class UserGrpcService(private val users: UserService) : UserServiceApi {

    fun bind(server: GrpcServer) {
        VertxUserServiceGrpcServer.bindAll(server, this)
    }

    override fun getUser(request: GetUserRequest): Future<GetUserResponse> = vertxFuture {
        val user = users.get(request.id)
            ?: throw Status.NOT_FOUND.withDescription("user ${request.id}").asRuntimeException()
        GetUserResponse.newBuilder().setUser(user.toProto()).build()
    }

    override fun createUser(request: CreateUserRequest): Future<CreateUserResponse> = vertxFuture {
        if (request.email.isBlank())
            throw Status.INVALID_ARGUMENT.withDescription("email required").asRuntimeException()
        val u = users.create(email = request.email, name = request.name)
        CreateUserResponse.newBuilder().setUser(u.toProto()).build()
    }

    private fun com.example.app.domain.User.toProto(): User = User.newBuilder()
        .setId(id)
        .setEmail(email)
        .setName(name)
        .setCreatedAtEpochMillis(createdAtEpochMillis)
        .build()
}
```

### `UserServiceApi` is generated

The codegen produces:

```java
public interface UserServiceApi {
    Future<GetUserResponse> getUser(GetUserRequest request);
    Future<CreateUserResponse> createUser(CreateUserRequest request);
}
```

Each method returns a `Future<T>`. We bridge to suspending code with
`vertxFuture { ... }`, which launches a coroutine on the *current Context*
(the event loop that received the RPC) and completes the Future with the
result.

This is **the same pattern as REST handlers in chapter 7**. The only
difference is the input/output is Protobuf bytes instead of JSON.

### Server binding

In `AppVerticle.start()`:

```kotlin
val grpcServer = GrpcServer.server(vertx)
UserGrpcService(userService).bind(grpcServer)

vertx.createHttpServer(HttpServerFactory.grpcOptions(config.grpc))
    .requestHandler(grpcServer)
    .listen(config.grpc.port)
    .coAwait()
```

`GrpcServer` is itself a `Handler<HttpServerRequest>`, so we pass it as the
HTTP/2 request handler. Vert.x routes `application/grpc` requests through
the gRPC dispatcher; everything else can fall through to a 404 or share the
same port with REST if you like.

### Server options

[`HttpServerFactory.grpcOptions`](../code/full-app/src/main/kotlin/com/example/app/http/HttpServerFactory.kt#L30):

```kotlin
HttpServerOptions()
    .setPort(cfg.port)
    .setReusePort(true)
    .setUseAlpn(true)
    .setHttp2ClearTextEnabled(true)
```

- **`setUseAlpn(true)`** — ALPN-negotiated HTTP/2 over TLS.
- **`setHttp2ClearTextEnabled(true)`** — also accept h2c (HTTP/2 cleartext)
  for service-to-service traffic on a private network.
- Other tunables you'll want to know about:
  - `setHttp2MultiplexingLimit(N)` — max concurrent streams per connection.
  - `setInitialSettings(Http2Settings().setMaxConcurrentStreams(...))`.

## 10.5  The client

```kotlin
val client = GrpcClient.client(vertx, GrpcClientOptions()
    .setUseAlpn(true))

val stub = VertxUserServiceGrpcClient(client, SocketAddress.inetSocketAddress(9090, "users-service"))

// Unary call
suspend fun fetch(id: String): GetUserResponse =
    stub.getUser(GetUserRequest.newBuilder().setId(id).build())
        .coAwait()
```

`GrpcClient` keeps a pool of HTTP/2 connections to the target. Configure:

```kotlin
GrpcClientOptions()
    .setKeepAlive(true)
    .setKeepAliveTimeout(60)
    .setHttp2ConnectionWindowSize(2 * 1024 * 1024)
```

The generated client returns `Future<T>` for unary RPCs. For server
streaming, the generated method returns `ReadStream<T>`; for client
streaming, `WriteStream<T>`; bidirectional returns a duplex.

## 10.6  Deadlines and cancellation

gRPC sends a `grpc-timeout` header from client → server. The server must
honor it. With Vert.x:

```kotlin
override fun getUser(request: GetUserRequest): Future<GetUserResponse> = vertxFuture {
    val timeoutMs = currentDeadlineMs()
        ?: 5_000L                                            // default
    withTimeout(timeoutMs) {
        users.get(request.id)
            ?.let { GetUserResponse.newBuilder().setUser(it.toProto()).build() }
            ?: throw Status.NOT_FOUND.asRuntimeException()
    }
}
```

`withTimeout` is a Kotlin coroutine builder; on timeout, all in-flight
suspending calls receive a `CancellationException` and the coroutine
unwinds. Vert.x converts that to `grpc-status=4 DEADLINE_EXCEEDED` for the
client.

For client-side deadlines:

```kotlin
stub.getUser(req).coAwait()      // no deadline by default

// With a deadline
val withTimeout = stub.getUser(req)
    .timeout(2, TimeUnit.SECONDS)
    .coAwait()
```

## 10.7  Metadata, interceptors, and auth

gRPC headers are just HTTP/2 headers. To add metadata server-side:

```kotlin
override fun getUser(request: GetUserRequest): Future<GetUserResponse> = vertxFuture {
    val callCtx: GrpcServerRequest<*, *> = ... // injected via thread-local or context
    val auth = callCtx.headers().get("authorization")
    // ...
}
```

For now, the simpler pattern is **a router-style filter** registered on the
GrpcServer:

```kotlin
grpcServer.callHandler { call ->
    val token = call.headers().get("authorization")
    if (token == null || !validate(token)) {
        call.response().status(GrpcStatus.UNAUTHENTICATED).end()
    } else {
        // dispatch normally
        UserServiceGrpcService.dispatch(grpcServer, call)
    }
}
```

Or use `vertx-grpc-server`'s middleware support if your version exposes it
(API has evolved — check the docs you depend on).

## 10.8  Mapping status codes

gRPC has a fixed set of statuses. Map them to your domain at the boundary:

| `grpc-status` | When |
|---|---|
| `0 OK` | Success |
| `1 CANCELLED` | Client cancelled (closed stream) |
| `2 UNKNOWN` | Catch-all for unexpected exceptions |
| `3 INVALID_ARGUMENT` | Bad request format/payload |
| `4 DEADLINE_EXCEEDED` | Timeout |
| `5 NOT_FOUND` | Logical not-found |
| `6 ALREADY_EXISTS` | Duplicate create |
| `7 PERMISSION_DENIED` | Not authorized for this resource |
| `8 RESOURCE_EXHAUSTED` | Rate-limit / quota exceeded |
| `9 FAILED_PRECONDITION` | State doesn't allow this op |
| `10 ABORTED` | Concurrency conflict (use for opt-locking) |
| `13 INTERNAL` | Internal bug |
| `14 UNAVAILABLE` | Transient — backend down, retry OK |
| `16 UNAUTHENTICATED` | No / invalid credentials |

The pattern in our service:

```kotlin
override fun createUser(request: CreateUserRequest): Future<CreateUserResponse> = vertxFuture {
    try {
        if (request.email.isBlank())
            throw Status.INVALID_ARGUMENT.withDescription("email required").asRuntimeException()
        val u = users.create(email = request.email, name = request.name)
        CreateUserResponse.newBuilder().setUser(u.toProto()).build()
    } catch (e: DuplicateEmailException) {
        throw Status.ALREADY_EXISTS.withDescription("email ${request.email}").asRuntimeException()
    }
}
```

## 10.9  Streaming RPC (brief)

For server streaming, the codegen produces something like:

```kotlin
override fun listUsers(request: ListUsersRequest, response: WriteStream<User>): Future<Void> {
    return vertxFuture {
        userRepo.list().forEach { response.write(it.toProto()).coAwait() }
        response.end()
    }
}
```

You yield bytes incrementally. The HTTP/2 `WINDOW_UPDATE` flow-control
pauses you if the client is slow, which back-pressures cleanly.

For client streaming and bidi, the pattern is symmetrical — `request` is a
`ReadStream<T>` you can `await()` items on.

## 10.10  Reflection (server-side introspection)

If you want `grpcurl` / `evans` to discover your service without distributing
the `.proto`, enable reflection:

```kotlin
val server = GrpcServer.server(vertx)
GrpcServerReflection.register(server, listOf(VertxUserServiceGrpcServer.SERVICE))
```

Reflection is typically *off* in production for an external service (it
leaks your API). For internal admin services, it's a debugging lifesaver.

## 10.11  Compression

gRPC supports per-call compression negotiated via the `grpc-encoding` header
(`identity` / `gzip` / `deflate`). On the server:

```kotlin
HttpServerOptions().setCompressionSupported(true)
```

On the client:

```kotlin
GrpcClientOptions().setRequestCompression("gzip")
```

For payloads >2 KB on a network-bound connection, gzip earns its keep. For
small payloads (< 200 B), gzip adds latency and CPU without saving bytes.

## 10.12  Best practices recap

- **Versioned package** (`v1`, `v2`) so you can run multiple versions side-by-side.
- **Never reuse field numbers**; `reserved` them when removed.
- **Use `repeated`** for lists, never a JSON-encoded string in a `bytes` field.
- **Use `google.protobuf.Timestamp`** for absolute times, **`Duration`** for
  intervals.
- **`google.protobuf.FieldMask`** for partial updates (think `PATCH`).
- **Plan for streaming** even if you start unary; switching v2 to streaming
  is a breaking change.
- **Set explicit deadlines** on every client call. Don't rely on default
  timeouts; a deadline-less RPC can hang for hours.
- **Map status codes consistently** at the server boundary; don't leak
  internal stack traces in `grpc-message`.
- **Don't put auth-sensitive data in `grpc-message`** (gets logged).

## 10.13  Try it

1. Use `grpcurl` to call your service:
   ```bash
   grpcurl -plaintext -d '{"id":"u-42"}' localhost:9090 com.example.app.grpc.v1.UserService/GetUser
   ```
2. Build a small client verticle that calls `GetUser` in a loop with a
   `withTimeout(50)`. Observe `DEADLINE_EXCEEDED` errors when the DB is
   slow.
3. Add a `ListUsers` server-streaming RPC. Verify back-pressure: a slow
   client should not blow up the server.

[← Ch 9](09-redis.md) · [Next: Chapter 11 — Configuration →](11-configuration.md)
