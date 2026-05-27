# Chapter 11 — Configuration

> **You should finish this chapter able to:** layer config from defaults,
> files, env vars, and secret stores; hot-reload safely; build typed
> data-class configs; validate at startup; and avoid the classic 12-factor
> pitfalls in containerized deployments.

A service without proper config has a string of `System.getenv("PORT") ?: "8080"`
calls scattered through the code, secrets baked into JAR manifests, and a
yaml file no-one knows where lives. The fix is the `vertx-config` module,
which gives you **layered, async, hot-reloadable, multi-source** config.

## 11.1  The 12-factor mandate (recap)

Per [12factor.net](https://12factor.net/config), config should be **strict
separation from code**. A few practical rules:

1. **Environment vars beat files beat defaults.** Containers inject env;
   files survive between deployments; defaults handle dev-on-laptop.
2. **Secrets are never on disk in the image.** Mount them at runtime
   (Kubernetes Secret, Vault, AWS Secrets Manager).
3. **One source of truth per property.** Don't read `DB_HOST` from a
   property file AND from `System.getenv`.
4. **Fail at startup on missing required config.** Don't lazily NPE on the
   first request.
5. **Configs are values, not constants.** Don't bake them into singletons —
   pass them as data into your wiring code.

`vertx-config` gives you all of these for free.

## 11.2  The dependency

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-config</artifactId>
</dependency>
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-config-yaml</artifactId>
</dependency>
```

`vertx-config` is the orchestrator. Each *store* is a plugin:

| Store | Module | Use for |
|---|---|---|
| `file` | core | YAML / JSON / properties on classpath or disk |
| `env` | core | Environment variables |
| `sys` | core | `-D` system properties |
| `directory` | `vertx-config-directory` | Drop-in config dir (Kubernetes mounts) |
| `consul` | `vertx-config-consul` | Consul KV |
| `vault` | `vertx-config-vault` | HashiCorp Vault |
| `git` | `vertx-config-git` | Git-stored config |
| `redis` | `vertx-config-redis` | Hot config in Redis |
| `kubernetes-configmap` | `vertx-config-kubernetes-configmap` | K8s ConfigMaps / Secrets |
| `yaml` | `vertx-config-yaml` | Enables YAML format |

## 11.3  Layered config: defaults → file → env

The classic layering: **right-most store wins**. Our
[`AppConfig.kt`](../code/full-app/src/main/kotlin/com/example/app/config/AppConfig.kt#L20):

```kotlin
suspend fun load(vertx: Vertx): AppConfig {
    val defaults = ConfigStoreOptions()
        .setType("file")
        .setFormat("yaml")
        .setConfig(JsonObject().put("path", "config/application.yaml"))
        .setOptional(true)

    val env = ConfigStoreOptions()
        .setType("env")
        .setConfig(JsonObject()
            .put("hierarchical", true)
            .put("keys", listOf(
                "APP_HTTP_PORT", "APP_GRPC_PORT",
                "APP_PG_HOST", "APP_PG_PORT", "APP_PG_DB", "APP_PG_USER", "APP_PG_PASSWORD",
                "APP_REDIS_URI",
            )))

    val retriever = ConfigRetriever.create(
        vertx,
        ConfigRetrieverOptions().addStore(defaults).addStore(env)
    )
    val json = retriever.config.coAwait()
    return fromJson(json)
}
```

Reading order: `defaults` (YAML) first, then `env`. The retriever merges
them; env wins on overlapping keys.

`setOptional(true)` on the file means startup doesn't fail if the file is
missing — useful for ephemeral pods where the YAML may not be mounted.

### YAML layout

[`config/application.yaml`](../code/full-app/src/main/resources/config/application.yaml):

```yaml
APP_HTTP_PORT: 8080
APP_GRPC_PORT: 9090
APP_PG_HOST: localhost
APP_PG_PORT: 5432
APP_PG_DB: app
APP_PG_USER: app
APP_PG_PASSWORD: app
APP_PG_POOL_MAX: 16
APP_PG_PIPELINING: 256
APP_REDIS_URI: redis://localhost:6379
```

We deliberately use **UPPER_SNAKE_CASE keys that match the env var names**.
This collapses one mental layer — what you set in YAML, you also set as
`export APP_PG_HOST=...`. No "snake_case in YAML, kebab-case in env"
gymnastics.

If you prefer a hierarchical layout in YAML:

```yaml
app:
  http:
    port: 8080
  postgres:
    host: localhost
    port: 5432
```

Then map env via `"hierarchical": true` + dotted env var names
(`APP_POSTGRES_HOST` ↔ `app.postgres.host`). Both styles work; the flat style
is easier in our experience because it makes the env override grep-able.

## 11.4  Typed config — data class with validation

Don't pass `JsonObject` around your codebase. Map it once, into typed
records:

```kotlin
data class AppConfig(
    val http: HttpServerConfig,
    val grpc: GrpcServerConfig,
    val postgres: PostgresConfig,
    val redis: RedisConfig,
) {
    init {
        require(http.port in 1..65535) { "http.port out of range: ${http.port}" }
        require(grpc.port in 1..65535) { "grpc.port out of range" }
        require(postgres.maxSize in 1..200) { "pg pool size silly: ${postgres.maxSize}" }
    }
}
```

Constructor validation runs at startup. If a value is bogus, your process
exits before serving traffic. **This is the entire point of typed config.**

For Kotlin-friendly Jackson deserialization (if you have many fields and want
auto-mapping), add `jackson-module-kotlin`:

```kotlin
val mapper = JsonMapper.builder().addModule(kotlinModule()).build()
val cfg = mapper.readValue<AppConfig>(json.encode())
```

Pros: no manual `j.getString(...)`. Cons: less control over default values.
For ≤20 properties, the manual mapper is fine and readable.

## 11.5  Where each property comes from — debugging

When config goes wrong, you need to know which store provided which value.
Drop a debug-only logger:

```kotlin
retriever.getConfig.coAwait().let { json ->
    json.forEach { entry ->
        log.atInfo()
            .setMessage("config")
            .addKeyValue("key", entry.key)
            .addKeyValue("value", maskIfSecret(entry.key, entry.value))
            .log()
    }
}

private fun maskIfSecret(k: String, v: Any?): Any? =
    if (k.lowercase().contains("password") || k.lowercase().contains("secret")) "****" else v
```

Run with `APP_PG_PASSWORD=hunter2 ./mvnw exec:java` and verify that
`pg.password=****` (masked) ends up in the merged config. Don't ever log the
unmasked value.

## 11.6  Hot reload

`ConfigRetriever` can re-read on an interval and fire listeners:

```kotlin
val retriever = ConfigRetriever.create(vertx, ConfigRetrieverOptions()
    .setScanPeriod(5_000)         // every 5s
    .addStore(file)
    .addStore(env))

retriever.listen { change ->
    log.info("config changed: {} → {}", change.previousConfiguration, change.newConfiguration)
    if (change.newConfiguration.getInteger("APP_PG_POOL_MAX") != current.postgres.maxSize) {
        // pool max changed — recreate the pool? bounce the verticle?
    }
}
```

Hot reload is dangerous if your code doesn't *actually* support it. Many
options are **start-time only** — the Postgres pool can't grow without
recreating it, the HTTP server bind port can't change without re-binding.
Reload only the things you've designed to be reloadable (log levels, feature
flags, rate-limit thresholds). For everything else, treat config as immutable
post-startup and roll the process.

## 11.7  Secrets — separate from configs

Secrets deserve their own store, ideally one that:

1. Doesn't write the secret to disk in the container image.
2. Doesn't put the secret in the process environment of *every* sibling
   process (an env var is visible to all children).
3. Rotates without restart.

### Pattern A: Kubernetes Secret as a file mount

K8s mounts `Secret` resources as files under `/etc/secrets/`. Use
`vertx-config-directory`:

```kotlin
val secrets = ConfigStoreOptions()
    .setType("directory")
    .setConfig(JsonObject()
        .put("path", "/etc/secrets")
        .put("filesets", JsonArray()
            .add(JsonObject().put("pattern", "*").put("format", "raw"))))
```

Each file becomes one key. K8s rotation is signaled by inode change; with
`setScanPeriod(...)` you'll re-read on next tick.

### Pattern B: HashiCorp Vault

```xml
<dependency>
  <groupId>io.vertx</groupId>
  <artifactId>vertx-config-vault</artifactId>
</dependency>
```

```kotlin
val vault = ConfigStoreOptions()
    .setType("vault")
    .setConfig(JsonObject()
        .put("host", "vault.internal")
        .put("port", 8200)
        .put("path", "secret/data/myapp")
        .put("token", System.getenv("VAULT_TOKEN")))
```

The vault store handles **lease renewal** for short-lived tokens. Pair it
with hot reload so DB credentials rotate seamlessly.

### Pattern C: AWS Secrets Manager / GCP Secret Manager

There's no first-party Vert.x module yet for AWS Secrets Manager, but it
takes 30 lines: fetch JSON at startup, merge into the config via
`addStore(ConfigStoreOptions().setType("json").setConfig(jsonFromAws))`.

## 11.8  Per-environment override

Three layers of YAML:

```
src/main/resources/
  config/
    application.yaml          # defaults
    application-dev.yaml      # local dev
    application-prod.yaml     # production
```

Then in `load`:

```kotlin
val profile = System.getenv("APP_PROFILE") ?: "dev"
val defaults = fileStore("config/application.yaml")
val perEnv   = fileStore("config/application-$profile.yaml").setOptional(true)
retriever = ConfigRetriever.create(vertx, ConfigRetrieverOptions()
    .addStore(defaults)
    .addStore(perEnv)
    .addStore(env))
```

Same precedence: env > perEnv > defaults.

## 11.9  Feature flags

Don't blur feature flags with config. A flag is mutable state with traffic
behavior; config is static-at-deploy infrastructure. Use a dedicated flag
service (LaunchDarkly, Flagsmith, Unleash) and consult it per request.

If you must lump them in, prefix the keys and document expectations:

```yaml
APP_FEATURE_FANOUT_V2: true
APP_FEATURE_NEW_PRICING: false
```

Combined with hot-reload + `listen`, you can flip them at runtime. Useful
for emergency kill-switches.

## 11.10  Boot-time validation cheat list

Validate the *whole* config at startup. The list to actually check:

```kotlin
init {
    // Ports
    require(http.port in 1..65535)
    require(grpc.port in 1..65535)
    require(http.port != grpc.port)

    // Postgres
    require(postgres.host.isNotBlank())
    require(postgres.user.isNotBlank())
    require(postgres.password.isNotBlank())   // catch empty-string passwords
    require(postgres.maxSize in 1..200)
    require(postgres.pipeliningLimit in 1..1024)

    // Redis URI looks vaguely correct
    require(redis.uri.startsWith("redis://") || redis.uri.startsWith("rediss://"))
}
```

Fail-fast at startup is *infinitely* better than a `NullPointerException` at
3 AM during incident triage.

## 11.11  Test-time config

Don't load real YAML files in unit tests. Build the config directly:

```kotlin
val testConfig = AppConfig(
    http = HttpServerConfig(port = 0),     // 0 = let OS pick a free port
    grpc = GrpcServerConfig(port = 0),
    postgres = postgresContainer.toConfig(),
    redis = redisContainer.toConfig(),
)
```

Port 0 telling the OS to assign means you can run tests in parallel without
collisions. The Testcontainers helpers (chapter 12) take care of providing
the right DB/redis endpoints.

## 11.12  Pitfalls

**Reading `System.getenv` inside a handler.** Slow (`getenv` is a syscall on
some platforms), and you bypass the layered store. Read once at startup.

**Re-creating `ConfigRetriever` per request.** It's not free — it spins up
file watchers and HTTP clients. Create once per Vertx instance.

**Allowing missing required config to default to a sensible-looking value.**
"Default" for `APP_PG_HOST` should not be `localhost` in production. Either
make it required or split production-required keys from dev-defaultable keys.

**Mixing trailing whitespace into env vars.** `export PG_HOST="prod-db.example.com "`
(trailing space, easy to do in shell). DNS resolution fails confusingly.
Trim every string config value at parse time.

**`setOptional(true)` on the env store.** Doesn't apply. Env is always
available. Use it on file stores that might not be mounted.

## 11.13  Try it

1. Run the app with `APP_HTTP_PORT=18080 APP_PG_HOST=foo.example.com mvn exec:java`.
   Verify the merged config logs show the env values winning.
2. Add a new top-level config (`APP_LOG_LEVEL`) and wire it to Logback at
   startup. On hot reload, update the root logger level using
   `LoggerContext.getLoggerList().forEach { ... }`. Test that flipping
   `INFO` ↔ `DEBUG` doesn't require a restart.
3. Move `APP_PG_PASSWORD` to `vertx-config-directory` reading from
   `/etc/secrets/pg-password`. Start a container with that file mounted and
   nothing in env. Verify it works.

[← Ch 10](10-grpc.md) · [Next: Chapter 12 — Testing →](12-testing.md)
