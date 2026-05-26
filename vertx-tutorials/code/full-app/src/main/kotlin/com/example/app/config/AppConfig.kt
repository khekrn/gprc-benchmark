package com.example.app.config

import io.vertx.config.ConfigRetriever
import io.vertx.config.ConfigRetrieverOptions
import io.vertx.config.ConfigStoreOptions
import io.vertx.core.Vertx
import io.vertx.core.json.JsonObject
import io.vertx.kotlin.coroutines.coAwait

/**
 * Three-tier config: classpath defaults -> file -> env vars (env wins).
 * Env vars beat file because containerized deployments always inject env.
 */
data class AppConfig(
    val http: HttpServerConfig,
    val grpc: GrpcServerConfig,
    val postgres: PostgresConfig,
    val redis: RedisConfig,
) {
    companion object {
        suspend fun load(vertx: Vertx): AppConfig {
            val defaults = ConfigStoreOptions()
                .setType("file")
                .setFormat("yaml")
                .setConfig(JsonObject().put("path", "config/application.yaml"))
                .setOptional(true)

            // Env store maps APP_HTTP_PORT -> http.port automatically with the right config.
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

        private fun fromJson(j: JsonObject): AppConfig = AppConfig(
            http = HttpServerConfig(port = j.getInteger("APP_HTTP_PORT", 8080)),
            grpc = GrpcServerConfig(port = j.getInteger("APP_GRPC_PORT", 9090)),
            postgres = PostgresConfig(
                host = j.getString("APP_PG_HOST", "localhost"),
                port = j.getInteger("APP_PG_PORT", 5432),
                database = j.getString("APP_PG_DB", "app"),
                user = j.getString("APP_PG_USER", "app"),
                password = j.getString("APP_PG_PASSWORD", "app"),
                maxSize = j.getInteger("APP_PG_POOL_MAX", 16),
                pipeliningLimit = j.getInteger("APP_PG_PIPELINING", 256),
            ),
            redis = RedisConfig(
                uri = j.getString("APP_REDIS_URI", "redis://localhost:6379"),
                maxPoolSize = j.getInteger("APP_REDIS_POOL_MAX", 16),
            ),
        )
    }
}

data class HttpServerConfig(val port: Int)
data class GrpcServerConfig(val port: Int)
data class PostgresConfig(
    val host: String, val port: Int, val database: String,
    val user: String, val password: String,
    val maxSize: Int, val pipeliningLimit: Int,
)
data class RedisConfig(val uri: String, val maxPoolSize: Int)
