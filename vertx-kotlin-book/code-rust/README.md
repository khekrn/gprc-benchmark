# users-rust — Rust port of the book's Vert.x/Kotlin users service

A 1:1 port of `code/full-app` (the Vert.x 5 + Kotlin coroutines users service)
and its Go twin (`code-go`) to **Rust**, using **tonic/prost** for gRPC, **Axum**
on **Tokio** for REST, and **tokio-postgres** (pooled with **deadpool**) for
PostgreSQL. Same wire contract, same REST surface, same four gRPC streaming
styles.

## Parity with the Kotlin / Go apps

| Concern            | Kotlin (`code/full-app`)         | Go (`code-go`)                  | Rust (`code-rust`)                              |
|--------------------|----------------------------------|---------------------------------|-------------------------------------------------|
| HTTP framework     | Vert.x Web                       | Fiber v3 (fasthttp)             | Axum 0.7 (Tokio + hyper)                        |
| DB driver          | vertx-pg-client (reactive)       | jackc/pgx v5 + pgxpool          | tokio-postgres + deadpool-postgres              |
| gRPC               | vertx-grpc                       | google.golang.org/grpc (buf)    | tonic 0.12 + prost (`protoc` vendored at build) |
| Unary              | `getUser` / `createUser`         | `GetUser` / `CreateUser`        | `get_user` / `create_user`                      |
| Server streaming   | `listUsers` (cursor Flow)        | `ListUsers` (server cursor)     | `list_users` (portal → `impl Stream`)           |
| Client streaming   | `importUsers`                    | `ImportUsers`                   | `import_users` (`Streaming` recv loop)          |
| Bidi streaming     | `chat`                           | `Chat`                          | `chat` (`async_stream` echo loop)               |
| Row streaming      | server-side cursor, tx for life  | `DECLARE CURSOR` + `FETCH`      | tx + `bind` portal + `query_portal(n)`          |
| Batch insert       | `executeBatch` (pipelined)       | `pgx.Batch` + `SendBatch`       | pipelined `try_join_all` over one connection    |
| LISTEN/NOTIFY      | PgSubscriber (dedicated conn)    | dedicated conn + WaitForNotif   | dedicated conn + `poll_message`                 |
| Back-pressure      | Flow `emit` suspends             | `Send` blocks → cursor pauses   | consumer pull → next `query_portal` waits       |
| Metrics            | Micrometer + Prometheus          | prometheus + interceptors       | `prometheus` crate + Axum mw / tower layer      |
| Config             | vertx-config (yaml + env)        | `config.yaml` + env             | `config.yaml` + env (serde)                     |
| Graceful shutdown  | shutdown hook                    | signal ctx + GracefulStop       | `CancellationToken` + graceful shutdown         |

The proto keeps the wire package `com.example.app.grpc`, so the **same
grpcurl invocations** work against any of the three servers.

## Layout

```
code-rust/
├── proto/usersv1/users.proto       # wire contract (mirrors the Kotlin/Go proto)
├── build.rs                        # tonic-build codegen (vendored protoc)
├── migrations/schema.sql           # idempotent schema, embedded via include_str!
├── src/
│   ├── main.rs                     # binary entry: tracing init → run()
│   ├── lib.rs                      # composition root (run): wires + serves both
│   ├── config.rs                   # YAML + env config loader (serde)
│   ├── domain.rs                   # User, NewUser (validation), UserError
│   ├── db/                         # pool.rs + repository.rs (cursor/batch/LISTEN)
│   ├── service.rs                  # application-service layer
│   ├── observability/             # metrics.rs + grpc_metrics.rs (tower layer)
│   ├── transport/                 # grpc.rs (4 RPC styles) + http.rs (Axum)
│   └── pb.rs                        # generated prost/tonic include
└── config.yaml                     # default config
```

## Prerequisites

- Rust 1.80+ (`cargo`). No system `protoc` needed — `protoc-bin-vendored`
  supplies it at build time.
- PostgreSQL. The committed `config.yaml` points at `proddb` / user
  `postgresql` / password `sam` on `localhost:5432`; override with `DB_*` env
  vars (e.g. `DB_DATABASE=appdb DB_USER=app DB_PASSWORD=app`).

## Build & run

```bash
cargo build           # compiles, generating gRPC stubs from proto/ via build.rs
cargo run             # start HTTP :8080 and gRPC :9090
```

Config via `config.yaml` or env (`HTTP_HOST`, `HTTP_PORT`, `GRPC_PORT`,
`DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`, `DB_DATABASE`,
`DB_POOL_MAX_SIZE`, `CONFIG_FILE`). Log level via `RUST_LOG` (default `info`).

## REST

```bash
curl -s -XPOST localhost:8080/api/users \
  -H 'content-type: application/json' \
  -d '{"email":"a@x.io","fullName":"Alice"}'
curl -sN localhost:8080/api/users          # NDJSON stream
curl -s localhost:8080/api/users/1
curl -s localhost:8080/metrics | head
```

## gRPC (grpcurl)

```bash
grpcurl -plaintext -d '{"email":"a@x.io","fullName":"Alice"}' \
    localhost:9090 com.example.app.grpc.Users/CreateUser
grpcurl -plaintext -d '{"id":1}' localhost:9090 com.example.app.grpc.Users/GetUser
grpcurl -plaintext -d '{}'       localhost:9090 com.example.app.grpc.Users/ListUsers
# reflection is enabled, so grpcurl needs no .proto
grpcurl -plaintext localhost:9090 list
```

## Docker

```bash
docker build -t users-rust .
docker run --rm -p 8080:8080 -p 9090:9090 \
  -e DB_HOST=host.docker.internal users-rust
```
