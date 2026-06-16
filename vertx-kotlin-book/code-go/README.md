# users-go — Go port of the book's Vert.x/Kotlin users service

A 1:1 port of `code/full-app` (the Vert.x 5 + Kotlin coroutines users service)
to Go, using **jackc/pgx** for PostgreSQL and **gRPC generated with buf**.
Same wire contract, same REST surface, same four gRPC streaming styles.

## Parity with the Kotlin app

| Concern            | Kotlin (`code/full-app`)                    | Go (`code-go`)                                        |
|--------------------|---------------------------------------------|-------------------------------------------------------|
| HTTP framework     | Vert.x Web                                  | Fiber v3 (fasthttp); `/metrics` via the Fiber adaptor |
| DB driver          | vertx-pg-client (reactive)                  | jackc/pgx v5 + pgxpool                                |
| gRPC               | vertx-grpc (+ vertx-grpc-protoc-plugin2)    | google.golang.org/grpc (+ buf → protoc-gen-go[-grpc]) |
| Unary              | `getUser` / `createUser`                    | `GetUser` / `CreateUser`                              |
| Server streaming   | `listUsers` (cursor Flow → WriteStream)     | `ListUsers` (server cursor → `stream.Send`)           |
| Client streaming   | `importUsers`                               | `ImportUsers` (`Recv` loop → `SendAndClose`)          |
| Bidi streaming     | `chat`                                      | `Chat` (`Recv`/`Send` echo loop)                      |
| Row streaming      | server-side cursor, tx held for stream life | `DECLARE CURSOR` + `FETCH FORWARD n` in a tx          |
| Batch insert       | `executeBatch` (pipelined)                  | `pgx.Batch` + `SendBatch` (pipelined)                 |
| LISTEN/NOTIFY      | PgSubscriber (dedicated conn)               | dedicated `pgx.Conn` + `WaitForNotification`          |
| Back-pressure      | Flow `emit` suspends                         | `stream.Send` / socket write blocks → cursor pauses   |
| Metrics            | Micrometer + Prometheus `/metrics`          | prometheus/client_golang `/metrics` + interceptors    |
| Config             | vertx-config (yaml + env)                   | `config.yaml` + env overrides                         |
| Graceful shutdown  | shutdown hook, undeploy + close             | signal ctx, `GracefulStop` + `http.Shutdown`          |

The proto keeps the wire package `com.example.app.grpc`, so the **same
grpcurl invocations** work against either server.

## Layout

```
code-go/
├── proto/usersv1/users.proto      # wire contract (mirrors the Kotlin users.proto)
├── buf.yaml, buf.gen.yaml         # buf config (local protoc-gen-go / -go-grpc)
├── gen/usersv1/                   # generated: users.pb.go, users_grpc.pb.go
├── internal/
│   ├── domain/                    # User, NewUser (validation), typed errors
│   ├── config/                    # YAML + env config loader
│   ├── db/                        # pgx pool + repository (cursor/batch/LISTEN)
│   ├── service/                   # application-service layer
│   ├── grpcserver/                # UsersServer impl (all 4 RPC styles)
│   ├── httpserver/                # Fiber v3 REST + NDJSON streaming + /metrics
│   ├── observability/             # Prometheus registry + HTTP/gRPC timing
│   └── migrate/                   # idempotent schema (embedded schema.sql)
└── main.go                        # composition root
```

## Prerequisites

- Go 1.25+
- `buf` (1.5x+) and the local codegen plugins on `PATH`:
  ```bash
  go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
  go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
  ```
- PostgreSQL (the committed `config.yaml` points at `proddb` / user `postgresql` / password `sam`
  on `localhost:5432`; override with `DB_*` env vars, e.g. `DB_DATABASE=appdb DB_USER=app DB_PASSWORD=app`)

## Generate, build, run

```bash
buf generate          # regenerate gen/ from proto/
go build ./...         # compile everything
go run .               # start HTTP :8080 and gRPC :9090
```

Config via `config.yaml` or env (`DB_HOST`, `DB_PORT`, `DB_USER`, `DB_PASSWORD`,
`DB_DATABASE`, `HTTP_PORT`, `GRPC_PORT`, `DB_POOL_MAX_SIZE`).

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
