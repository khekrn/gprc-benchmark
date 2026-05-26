package com.example.app

import io.vertx.core.Vertx
import io.vertx.core.VertxOptions
import io.vertx.core.ThreadingModel
import io.vertx.core.DeploymentOptions
import io.vertx.core.metrics.MetricsOptions
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.micrometer.MicrometerMetricsOptions
import io.vertx.micrometer.VertxPrometheusOptions
import io.vertx.micrometer.backends.BackendRegistries
import io.micrometer.prometheusmetrics.PrometheusConfig
import io.micrometer.prometheusmetrics.PrometheusMeterRegistry
import com.example.app.verticles.AppVerticle
import com.example.app.observability.AppShutdown
import kotlinx.coroutines.runBlocking
import org.slf4j.LoggerFactory

/**
 * Single entry point. Builds a Vertx instance, registers a Prometheus registry,
 * deploys the root AppVerticle, and installs a graceful-shutdown hook.
 *
 * We deliberately use `runBlocking` ONLY in main — never anywhere inside a
 * verticle or handler. See chapter 5 for why.
 */
fun main(): Unit = runBlocking {
    val log = LoggerFactory.getLogger("Main")

    // Prometheus registry shared by Vert.x metrics and our domain meters.
    val prometheus = PrometheusMeterRegistry(PrometheusConfig.DEFAULT)

    val metricsOptions: MetricsOptions = MicrometerMetricsOptions()
        .setPrometheusOptions(VertxPrometheusOptions().setEnabled(true))
        .setMicrometerRegistry(prometheus)
        .setEnabled(true)

    val vertxOptions = VertxOptions()
        // Default is 2 * Runtime.getRuntime().availableProcessors().
        // Inside a container with cpuset/limit, availableProcessors honors
        // the cgroup quota when -XX:+UseContainerSupport is enabled (default on 17+).
        .setEventLoopPoolSize(2 * Runtime.getRuntime().availableProcessors())
        // Worker pool is for `executeBlocking`. Sized for short blocking calls.
        .setWorkerPoolSize(20)
        // Detect handlers that block the event loop for too long. 100ms is aggressive
        // but right for an API server. Bump for batch workloads.
        .setBlockedThreadCheckInterval(1_000)
        .setMaxEventLoopExecuteTime(100_000_000L)        // 100 ms in ns
        .setMetricsOptions(metricsOptions)
        // Prefer native transport (epoll on Linux, kqueue on macOS) when available.
        .setPreferNativeTransport(true)

    val vertx = Vertx.vertx(vertxOptions)
    log.info("Vert.x started — native transport in use: {}", vertx.isNativeTransportEnabled)

    val deploymentOptions = DeploymentOptions()
        // Deploy one AppVerticle per event-loop thread for clean horizontal scaling.
        .setInstances(Runtime.getRuntime().availableProcessors())
        .setThreadingModel(ThreadingModel.EVENT_LOOP)

    val deploymentId = vertx.deployVerticle(::AppVerticle, deploymentOptions).coAwait()
    log.info("AppVerticle deployed: {}", deploymentId)

    // Wire JVM-level shutdown so SIGTERM from Docker / Kubernetes is honored.
    AppShutdown.install(vertx, deploymentId)
}
