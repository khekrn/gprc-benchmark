package com.beam.bench

import io.vertx.core.DeploymentOptions
import io.vertx.kotlin.coroutines.CoroutineVerticle
import io.vertx.kotlin.coroutines.coAwait
import org.slf4j.LoggerFactory
import java.util.function.Supplier

/**
 * Root verticle. Owns the long-lived resources (the Postgres pool) and deploys
 * one [GrpcVerticle] per event loop.
 *
 * Why this shape:
 *  - The pool is created once on `vertx` (the root) and shared. Reactive
 *    pg-client is designed to be shared across event loops; per-verticle pools
 *    would burn connections for no win.
 *  - Deploying GrpcVerticle with `instances = eventLoops` is how Vert.x pins
 *    one HTTP/gRPC server stub to each event loop. Vert.x distributes incoming
 *    connections across them without us doing anything.
 *  - We undeploy and close in [stop] so SIGTERM via the JVM shutdown hook gives
 *    Postgres a clean disconnect.
 */
class MainVerticle(private val cfg: Config) : CoroutineVerticle() {

    private lateinit var db: Db

    override suspend fun start() {
        db = Db.build(vertx, cfg)
        db.warmup(cfg.pgPoolMin)
        LOG.info(
            "DB pool ready (host={}:{} db={} max={} min={} pipeliningLimit={})",
            cfg.pgHost, cfg.pgPort, cfg.pgDb, cfg.pgPoolMax, cfg.pgPoolMin,
            cfg.pipeliningLimit
        )

        val deployOptions = DeploymentOptions().setInstances(cfg.eventLoops)
        // Pass a Supplier (not a single instance) so each event loop deploys
        // its own GrpcVerticle. SAM-converted explicitly to disambiguate from
        // the deployVerticle(Deployable, opts) overload.
        vertx.deployVerticle(
            Supplier { GrpcVerticle(db, cfg.listenHost, cfg.listenPort) },
            deployOptions,
        ).coAwait()

        LOG.info(
            "kotlin-vertx server up on {}:{} ({} gRPC verticle instance(s))",
            cfg.listenHost, cfg.listenPort, cfg.eventLoops
        )
    }

    override suspend fun stop() {
        if (::db.isInitialized) {
            db.close()
            LOG.info("DB pool closed")
        }
    }

    companion object {
        private val LOG = LoggerFactory.getLogger(MainVerticle::class.java)
    }
}
