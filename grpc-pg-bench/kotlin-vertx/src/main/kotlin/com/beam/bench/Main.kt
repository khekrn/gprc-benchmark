package com.beam.bench

import io.vertx.core.Vertx
import io.vertx.core.VertxOptions
import org.slf4j.LoggerFactory
import java.util.concurrent.TimeUnit
import kotlin.system.exitProcess

private val LOG = LoggerFactory.getLogger("com.beam.bench.Main")

/**
 * Entry point. Reads [Config] from env, starts Vert.x with the configured
 * event-loop pool size, deploys [MainVerticle], and installs a shutdown hook
 * so SIGTERM (the orchestrator uses `kill` to stop the server between sweeps)
 * drains and closes cleanly.
 */
fun main() {
    val cfg = Config.fromEnv()

    val vertx = Vertx.vertx(
        VertxOptions()
            .setEventLoopPoolSize(cfg.eventLoops)
            // The benchmark never schedules blocking work; keep the worker
            // pool minimal so we don't pay for idle threads on a 2-core box.
            .setWorkerPoolSize(2)
            // Don't terminate the JVM if a hot-path call ever overruns its
            // event-loop budget; surface a warning instead so we notice.
            .setMaxEventLoopExecuteTime(50)
            .setMaxEventLoopExecuteTimeUnit(TimeUnit.MILLISECONDS)
    )

    // SIGTERM (the orchestrator's `kill`) drives JVM shutdown hooks. The hook
    // below cascades stop() through every deployed verticle so Postgres sees
    // a clean disconnect. It runs on its own thread, so the event loops are
    // free to drain — no deadlock.
    Runtime.getRuntime().addShutdownHook(Thread({
        try {
            vertx.close().toCompletionStage().toCompletableFuture()
                .get(10, TimeUnit.SECONDS)
            LOG.info("vertx closed")
        } catch (t: Throwable) {
            LOG.warn("vertx close did not finish cleanly: {}", t.message)
        }
    }, "vertx-shutdown"))

    vertx.deployVerticle(MainVerticle(cfg))
        .onFailure { err ->
            LOG.error("startup failed", err)
            // We are on an event-loop thread here. Calling exitProcess directly
            // would trigger the shutdown hook (above) which waits on vertx.close
            // via .get() — but vertx.close needs the event loops to run, and
            // they're blocked inside System.exit awaiting that very hook.
            // Hand exit off to a regular thread to break the cycle.
            Thread({ exitProcess(1) }, "exit-on-startup-failure").start()
        }
}
