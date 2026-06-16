package com.example.app

import com.example.app.config.AppConfig
import com.example.app.observability.AppShutdown
import com.example.app.observability.Metrics
import com.example.app.verticles.AppVerticle
import com.fasterxml.jackson.databind.SerializationFeature
import com.fasterxml.jackson.datatype.jsr310.JavaTimeModule
import com.fasterxml.jackson.module.kotlin.registerKotlinModule
import io.vertx.core.DeploymentOptions
import io.vertx.core.Vertx
import io.vertx.core.VertxOptions
import io.vertx.core.json.jackson.DatabindCodec
import io.vertx.kotlin.coroutines.coAwait
import kotlinx.coroutines.runBlocking
import org.slf4j.LoggerFactory

private val log = LoggerFactory.getLogger("com.example.app.Main")

/**
 * Composition root. Everything wires up here, top to bottom, no DI framework.
 * Run with: `mvn -pl full-app exec:java` (after `docker compose up -d postgres`).
 */
fun main(): Unit = runBlocking {
    // Vert.x ships its own ObjectMapper and does NOT auto-register modules.
    // Register them once so domain types with java.time fields
    // (User.createdAt: OffsetDateTime) serialize to ISO-8601 instead of failing.
    DatabindCodec.mapper()
        .registerKotlinModule()
        .registerModule(JavaTimeModule())
        .disable(SerializationFeature.WRITE_DATES_AS_TIMESTAMPS)

    val vertx = Vertx.builder()
        .with(
            VertxOptions()
                // One event loop per core gives the kernel the best chance to
                // assign each loop to a dedicated CPU.  See chapter 1 + 17.
                .setEventLoopPoolSize(Runtime.getRuntime().availableProcessors())
                .setWorkerPoolSize(8)
                .setPreferNativeTransport(true)
                .setMetricsOptions(Metrics.options())
                .setWarningExceptionTime(2_000_000_000L)   // 2s blocked-thread warn
        )
        // Vert.x 5 attaches the pre-built Micrometer registry via the factory.
        .withMetrics(Metrics.factory())
        .build()

    val config = AppConfig.load(vertx).coAwait()
    log.info("Loaded config: http={} grpc={} db={}:{}/{}",
        config.http.port, config.grpc.port,
        config.db.host, config.db.port, config.db.database)

    val opts = DeploymentOptions().setConfig(config.raw)
    val deploymentId = vertx.deployVerticle(AppVerticle(), opts).coAwait()
    log.info("AppVerticle deployed: id={}", deploymentId)

    AppShutdown.install(vertx, deploymentId, config.shutdownGracePeriodMs)
}
