package com.example.app.config

import io.vertx.config.ConfigRetriever
import io.vertx.config.ConfigRetrieverOptions
import io.vertx.config.ConfigStoreOptions
import io.vertx.core.Future
import io.vertx.core.Vertx
import io.vertx.core.json.JsonObject

/**
 * Strongly-typed config loaded once at startup.  We pass it down to verticles
 * via DeploymentOptions so each verticle sees the same snapshot.
 *
 * Layers (later overrides earlier):
 *   1. classpath:config/application.yaml
 *   2. environment variables (DB_HOST → db.host)
 *   3. system properties
 *
 * Sensitive values (DB password) should come from env in production.
 */
data class AppConfig(
    val raw: JsonObject,
    val http: HttpConfig,
    val grpc: GrpcConfig,
    val db: DbConfig,
    val shutdownGracePeriodMs: Long,
) {
    data class HttpConfig(val host: String, val port: Int)
    data class GrpcConfig(val port: Int)
    data class DbConfig(
        val host: String,
        val port: Int,
        val database: String,
        val user: String,
        val password: String,
        val poolMaxSize: Int,
        val pipeliningLimit: Int,
        val schemaOnStartup: Boolean,
    )

    companion object {
        fun load(vertx: Vertx): Future<AppConfig> {
            val retriever = ConfigRetriever.create(
                vertx,
                ConfigRetrieverOptions()
                    .addStore(yamlStore("config/application.yaml"))
                    .addStore(envStore())
                    .addStore(sysStore())
            )
            return retriever.config.map { json -> parse(json) }
        }

        private fun yamlStore(path: String): ConfigStoreOptions =
            ConfigStoreOptions()
                .setType("file")
                .setFormat("yaml")
                .setConfig(JsonObject().put("path", path))

        private fun envStore(): ConfigStoreOptions =
            ConfigStoreOptions().setType("env")

        private fun sysStore(): ConfigStoreOptions =
            ConfigStoreOptions().setType("sys")

        private fun parse(json: JsonObject): AppConfig {
            val http = json.getJsonObject("http", JsonObject())
            val grpc = json.getJsonObject("grpc", JsonObject())
            val db   = json.getJsonObject("db", JsonObject())
            val pool = db.getJsonObject("pool", JsonObject())
            return AppConfig(
                raw = json,
                http = HttpConfig(
                    host = http.getString("host", "0.0.0.0"),
                    port = http.getInteger("port", 8080),
                ),
                grpc = GrpcConfig(
                    port = grpc.getInteger("port", 9090),
                ),
                db = DbConfig(
                    host     = db.getString("host", "localhost"),
                    port     = db.getInteger("port", 5432),
                    database = db.getString("database", "appdb"),
                    user     = db.getString("user", "app"),
                    password = db.getString("password", "app"),
                    poolMaxSize     = pool.getInteger("maxSize", 16),
                    pipeliningLimit = pool.getInteger("pipeliningLimit", 256),
                    schemaOnStartup = db.getBoolean("schemaOnStartup", true),
                ),
                shutdownGracePeriodMs = json.getLong("shutdownGracePeriodMs", 15_000L),
            )
        }
    }
}
