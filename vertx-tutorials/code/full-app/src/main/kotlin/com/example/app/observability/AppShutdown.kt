package com.example.app.observability

import io.vertx.core.Vertx
import org.slf4j.LoggerFactory

object AppShutdown {
    private val log = LoggerFactory.getLogger("AppShutdown")

    /** Install a JVM hook to undeploy the verticle and close Vertx gracefully on SIGTERM. */
    fun install(vertx: Vertx, deploymentId: String) {
        Runtime.getRuntime().addShutdownHook(Thread {
            log.info("SIGTERM received, draining...")
            // Undeploy first so handlers stop accepting; then close.
            try {
                vertx.undeploy(deploymentId).toCompletionStage().toCompletableFuture().get()
                vertx.close().toCompletionStage().toCompletableFuture().get()
                log.info("Graceful shutdown complete")
            } catch (t: Throwable) {
                log.error("Shutdown error", t)
            }
        })
    }
}
