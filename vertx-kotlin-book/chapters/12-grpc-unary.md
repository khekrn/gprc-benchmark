# Chapter 12 — gRPC fundamentals & unary RPC

> By the end of this chapter you will have written a `.proto` file from
> scratch with idiomatic conventions, generated server + client code
> with Maven, implemented two unary RPCs with the modern
> `vertxFuture { }` style, and called them with `grpcurl` and a
> Kotlin client.

## 12.1 What gRPC actually is

gRPC is **HTTP/2** + **Protocol Buffers** + a few conventions on top:

- **HTTP/2** for the transport: streams, multiplexing, binary framing.
- **Protobuf** for the message format: schema-first, language-neutral,
  small wire bytes.
- **Status codes** for errors (`NOT_FOUND`, `UNAUTHENTICATED`, …).
- **Metadata** for headers/trailers.
- **Four call shapes**: unary, server streaming, client streaming, bidi.

If REST is "URL paths + JSON + status codes", gRPC is "service methods +
protobuf + status codes". gRPC wins on:

- Schema enforced by tooling.
- Smaller wire format.
- Built-in streaming and back-pressure semantics.
- Strong, generated client stubs.

REST wins on:

- Browser-native, cache-friendly, observable in any HTTP tool.
- Easier debugging by hand.
- No proto-plugin Maven configuration.

For service-to-service, gRPC is usually the better choice.

## 12.2 Composing a `.proto` file

`code/full-app/src/main/proto/users.proto` is the single source of truth
for our wire contract:

```proto
syntax = "proto3";

package com.example.app.grpc;

option java_multiple_files = true;
option java_package = "com.example.app.grpc.proto";
option java_outer_classname = "UsersProto";

service Users {
  rpc GetUser   (GetUserRequest)    returns (UserReply);
  rpc CreateUser(CreateUserRequest) returns (UserReply);
  rpc ListUsers (ListUsersRequest)  returns (stream UserReply);
  rpc ImportUsers(stream CreateUserRequest) returns (ImportSummary);
  rpc Chat(stream ChatMessage) returns (stream ChatMessage);
}

message GetUserRequest { int64 id = 1; }
message CreateUserRequest { string email = 1; string full_name = 2; }
message UserReply {
  int64  id = 1;
  string email = 2;
  string full_name = 3;
  string created_at = 4;
}
message ListUsersRequest { int32 page_size = 1; string email_prefix = 2; }
message ImportSummary { int64 imported = 1; int64 skipped = 2; repeated string errors = 3; }
message ChatMessage { string from = 1; string text = 2; int64 ts_millis = 3; }
```

Conventions worth committing to:

1. **`syntax = "proto3";`** — proto2 is legacy.
2. **Top-of-file `package`** — used for fully-qualified service name
   `com.example.app.grpc.Users/GetUser`. Pick it once, never rename.
3. **`option java_multiple_files = true;`** — one Java class per
   message, easier to import.
4. **`option java_package = "…"`** — controls where files land.
5. **`option java_outer_classname = "UsersProto"`** — name of the
   metadata class. Keep it boring.
6. **`message` fields are numbered** (`= 1`, `= 2`, …) and **the
   numbers are forever**. Adding a field uses a new number; never
   re-use an old one (Section 12.5).
7. **`snake_case` field names** → generated Kotlin/Java methods are
   `camelCase` automatically.
8. **`stream` keyword** turns either side of an RPC into a stream
   (Chapter 13–14).

### Things to avoid

- **`repeated` of `bytes`** for files larger than a few KB — use
  streaming.
- **`oneof`** when a sum type would do — yes, it works, but it adds
  client code complexity. Use it when truly mutually-exclusive.
- **`required`** — removed in proto3 for good reason. Every field is
  optional on the wire. Validate on the server.
- **Re-using field numbers** — see 12.5.

## 12.3 Maven codegen

Two pieces wire the build:

1. **`protobuf-maven-plugin`** runs `protoc` against `src/main/proto`,
   generating Java messages into `target/generated-sources/protobuf/java`.
2. The **`vertx-grpc-protoc-plugin2`** is registered as a custom
   `<protocPlugin>` and emits the Vert.x service interfaces and
   server / client stubs.

```xml
<protocPlugin>
    <id>vertx-grpc</id>
    <groupId>io.vertx</groupId>
    <artifactId>vertx-grpc-protoc-plugin2</artifactId>
    <version>${vertx.version}</version>
    <mainClass>io.vertx.grpc.plugin.VertxGrpcGenerator</mainClass>
    <args>
        <arg>--grpc-service</arg>
        <arg>--grpc-client</arg>
    </args>
</protocPlugin>
```

Build it:

```bash
mvn -pl full-app -am -DskipTests compile
ls target/generated-sources/protobuf/java/com/example/app/grpc/proto/
```

You should see, for our `.proto`:

- `GetUserRequest.java`, `CreateUserRequest.java`, `UserReply.java`, …
- `UsersService.java` — an abstract **class** you extend on the server
  (`--grpc-service`); override one method per RPC.
- `UsersGrpcService.java` — the binder: `UsersGrpcService.of(impl).bind(server)`,
  plus the `ServiceMethod` descriptors and service name.
- `UsersClient.java` / `UsersGrpcClient.java` — typed client (`--grpc-client`).

### Why Kotlin sees these

In our `pom.xml` the `kotlin-maven-plugin` is configured to look at *both*
`src/main/kotlin` and `target/generated-sources/protobuf/java`:

```xml
<sourceDirs>
    <sourceDir>${project.basedir}/src/main/kotlin</sourceDir>
    <sourceDir>${project.build.directory}/generated-sources/protobuf/java</sourceDir>
</sourceDirs>
```

That lets Kotlin compile *after* protoc has run, and resolve the
generated Java classes. The `kotlin-maven-plugin` runs in
`process-sources` (before `compile`) so the order is:

```
generate-sources  → protobuf-maven-plugin (protoc + vertx-grpc gen)
process-sources   → kotlin-maven-plugin   (kotlinc, sees generated Java)
compile           → maven-compiler-plugin (javac for the generated Java)
```

## 12.4 Implementing unary

Two RPCs in our service are unary:

```proto
rpc GetUser   (GetUserRequest)    returns (UserReply);
rpc CreateUser(CreateUserRequest) returns (UserReply);
```

Our class extends the generated abstract `UsersService` and overrides the
two unary methods, which return `Future<UserReply>`. We implement them with
the modern coroutine bridge:

```kotlin
override fun getUser(request: GetUserRequest): Future<UserReply> = vertxFuture(vertx, scope) {
    try {
        users.getById(request.id).toReply()
    } catch (e: UserError.NotFound) {
        throw StatusException(GrpcStatus.NOT_FOUND, e.message)
    }
}

override fun createUser(request: CreateUserRequest): Future<UserReply> = vertxFuture(vertx, scope) {
    try {
        users.create(NewUser(request.email, request.fullName)).toReply()
    } catch (e: UserError.DuplicateEmail) {
        throw StatusException(GrpcStatus.ALREADY_EXISTS, e.message)
    } catch (e: IllegalArgumentException) {
        throw StatusException(GrpcStatus.INVALID_ARGUMENT, e.message)
    }
}
```

Walk through `getUser`:

- The method signature is dictated by the generator: `GetUserRequest`
  in, `Future<UserReply>` out. We can't change that — it's the contract.
- `vertxFuture(vertx, scope) { ... }` is the top-level coroutine builder from
  `io.vertx.kotlin.coroutines`. It runs the suspending block on the Vert.x
  context's dispatcher and returns the `Future` the generator wants. (`vertx`
  is the instance; `scope` is a `CoroutineScope` the service owns so the work
  is cancelled with the service. The `scope` argument defaults to `GlobalScope`,
  but tying it to a real scope is the production-correct choice.)
- Inside the block, we call our suspending `users.getById(id)`. If the
  service throws `UserError.NotFound`, we re-throw as a
  `io.vertx.grpc.server.StatusException` with `NOT_FOUND` status. The
  framework sets the wire trailer accordingly. (Vert.x 5 renamed/relocated
  the old `GrpcException`; on the server you throw `StatusException`.)
- Any other thrown exception bubbles up and the framework returns
  `UNKNOWN`/`INTERNAL`. We log the real one server-side.

That's the entire unary handler. Compare to:

```kotlin
// Pre-coroutine, callback-style equivalent
override fun getUser(req: GetUserRequest): Future<UserReply> =
    users.findByIdFuture(req.id)
        .compose { u ->
            if (u == null) Future.failedFuture(StatusException(GrpcStatus.NOT_FOUND, "missing"))
            else Future.succeededFuture(u.toReply())
        }
```

That works. The coroutine version is shorter and dialect-free.

## 12.5 Wire compatibility rules (you *will* hit these)

These are the contracts you implicitly sign by deploying a proto:

1. **Don't change field numbers.** Old binaries map fields by number.
2. **Don't change field types.** `int32` → `int64` is *partially* safe;
   string → bytes is not.
3. **Don't re-use a removed field's number.** Either keep it as
   `reserved` or never re-use:
   ```proto
   message User {
       reserved 4, 7;            // ids previously used
       reserved "old_field";     // names previously used
   }
   ```
4. **Adding new fields is fine.** Old clients ignore unknown fields.
5. **Removing fields is fine, with caution.** Old code paths that read
   the field will see the default value (0, empty string).
6. **Don't change a `singular` field to `repeated` or vice versa.**
7. **Don't change `oneof` group composition.**

Treat your `.proto` files like a public API. Version by package
(`v1`, `v2`) if you need a hard break.

## 12.6 Wiring the server

```kotlin
// code/full-app/src/main/kotlin/com/example/app/verticles/AppVerticle.kt
val grpcServer = GrpcServer.server(vertx)
UserGrpcService(vertx, service).bindTo(grpcServer)
grpcHttp = vertx.createHttpServer()
    .requestHandler(grpcServer)
    .listen(cfg.grpc.port).coAwait()
```

`GrpcServer` is itself a `Handler<HttpServerRequest>`, so we hand it
to a plain HTTP/2 server. No special protocol setup. HTTP/2 is
negotiated by ALPN over TLS or via prior-knowledge over cleartext (we
use cleartext locally on `:9090`).

## 12.7 Calling unary RPCs

### From `grpcurl`

```bash
# CreateUser
grpcurl -plaintext -d '{"email":"a@x.io","fullName":"Alice"}' \
    localhost:9090 com.example.app.grpc.Users/CreateUser
# {"id":"1","email":"a@x.io","fullName":"Alice","createdAt":"2026-…"}

# GetUser
grpcurl -plaintext -d '{"id":1}' \
    localhost:9090 com.example.app.grpc.Users/GetUser

# NOT_FOUND example
grpcurl -plaintext -d '{"id":99999}' \
    localhost:9090 com.example.app.grpc.Users/GetUser
# ERROR: Code: NotFound  Message: user 99999 not found
```

grpcurl with `-plaintext` skips TLS. In production you'd hand it a
cert.

### From a Kotlin client

```kotlin
import com.example.app.grpc.proto.GetUserRequest
import com.example.app.grpc.proto.UsersGrpcClient
import io.vertx.core.Vertx
import io.vertx.grpc.client.GrpcClient
import io.vertx.kotlin.coroutines.coAwait
import kotlinx.coroutines.runBlocking

fun main(): Unit = runBlocking {
    val vertx = Vertx.vertx()
    val client = UsersGrpcClient.create(GrpcClient.client(vertx), io.vertx.core.net.SocketAddress.inetSocketAddress(9090, "localhost"))
    val reply = client.getUser(GetUserRequest.newBuilder().setId(1).build()).coAwait()
    println("got ${reply.email}")
    vertx.close().coAwait()
}
```

The generated `UsersGrpcClient` exposes each RPC with a suspending-friendly
`Future<UserReply>` return type. Awaiting with `coAwait()` makes the call
sequential.

## 12.8 Errors over the wire

When the server throws `StatusException(GrpcStatus.NOT_FOUND, msg)`, the
client's Future fails with an `io.vertx.grpc.client.InvalidStatusException`
that carries the status the server actually returned:

```kotlin
try {
    val reply = client.getUser(req).coAwait()
} catch (e: InvalidStatusException) {
    when (e.actualStatus()) {                 // expectedStatus() is always OK here
        GrpcStatus.NOT_FOUND       -> /* 404 equivalent */
        GrpcStatus.ALREADY_EXISTS  -> /* 409 equivalent */
        else                       -> /* generic */
    }
}
```

Don't return `Future.succeededFuture(null)` to signal "not found". Use
status codes. Clients can switch on them; humans understand them; tools
display them.

## 12.9 Reflection and discovery

Locally, you can enable gRPC server reflection so `grpcurl` doesn't need
the `.proto`:

```kotlin
import io.vertx.grpc.server.GrpcServer
import io.vertx.grpc.reflection.ReflectionService

val server = GrpcServer.server(vertx)
UserGrpcService(vertx, service).bindTo(server)   // your service
ReflectionService.v1().bind(server)              // + reflection
```

`ReflectionService` is itself a `Service`; `v1()` builds the v1 reflection
service and `bind(server)` registers it. You'll need the
`vertx-grpc-reflection` artifact. Useful for dev; usually off in production.

## 12.10 Exercises

1. Add a unary RPC `UpdateUser(UpdateUserRequest) returns (UserReply)`
   with a `mask` of which fields to update. Implement it with
   `vertxFuture { }`.
2. Translate every domain error in `UserError` to a `GrpcStatus`. Write
   a single `mapDomainError(t: Throwable): Nothing` helper that throws
   `StatusException`.
3. Write a Kotlin client that pings GetUser, measures p50/p99 latency
   over 10,000 requests, and prints the histogram.

---

[← Chapter 11](11-repository-patterns.md) · [Next → Chapter 13: Server-streaming RPC](13-grpc-server-streaming.md)
