package com.example.app.observability

import io.vertx.core.Vertx
import io.vertx.kotlin.coroutines.coAwait
import kotlinx.coroutines.runBlocking
import org.slf4j.LoggerFactory

object AppShutdown {
    private val log = LoggerFactory.getLogger(AppShutdown::class.java)

    fun install(vertx: Vertx, deploymentId: String, gracePeriodMs: Long) {
        val latch = java.util.concurrent.CountDownLatch(1)
        Runtime.getRuntime().addShutdownHook(Thread({
            try {
                runBlocking {
                    log.info("Undeploying {}", deploymentId)
                    vertx.undeploy(deploymentId).coAwait()
                    log.info("Closing Vert.x (grace {} ms)", gracePeriodMs)
                    vertx.close().coAwait()
                    log.info("Shutdown complete")
                }
            } catch (t: Throwable) {
                log.error("Shutdown error", t)
            } finally {
                latch.countDown()
            }
        }, "app-shutdown"))
        latch.await()  // park main thread until JVM exits
    }
}
