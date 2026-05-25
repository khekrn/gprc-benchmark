package com.beam.bench

import io.vertx.core.http.HttpServer
import io.vertx.core.http.HttpServerOptions
import io.vertx.grpc.server.GrpcServer
import io.vertx.kotlin.coroutines.CoroutineVerticle
import io.vertx.kotlin.coroutines.coAwait
import org.slf4j.LoggerFactory

/**
 * One [GrpcVerticle] instance is deployed per event loop (see [MainVerticle]).
 *
 * Each instance binds its own [HttpServer] on the same host:port. Vert.x shares
 * the listening socket across event loops, so each instance handles a fraction
 * of incoming connections on its own loop — this is how Vert.x scales an
 * HTTP/gRPC server horizontally without us writing thread-pool plumbing.
 *
 * Because this is a [CoroutineVerticle], the verticle itself is a
 * [kotlinx.coroutines.CoroutineScope] whose dispatcher is the verticle's
 * event-loop context. We hand that scope to [CommandServiceImpl] so its
 * `vertxFuture { ... }` blocks run on the same loop the request arrived on,
 * with zero context switches.
 */
class GrpcVerticle(
    private val db: Db,
    private val listenHost: String,
    private val listenPort: Int,
) : CoroutineVerticle() {

    private lateinit var httpServer: HttpServer

    override suspend fun start() {
        val grpcServer = GrpcServer.server(vertx)
        grpcServer.addService(CommandServiceImpl(db, this))

        val httpOptions = HttpServerOptions()
            .setHost(listenHost)
            .setPort(listenPort)

        httpServer = vertx.createHttpServer(httpOptions)
            .requestHandler(grpcServer)
            .listen()
            .coAwait()

        LOG.info(
            "GrpcVerticle ready on {}:{} (event-loop instance, deploymentId={})",
            listenHost, listenPort, deploymentID
        )
    }

    override suspend fun stop() {
        if (::httpServer.isInitialized) {
            httpServer.close().coAwait()
        }
    }

    companion object {
        private val LOG = LoggerFactory.getLogger(GrpcVerticle::class.java)
    }
}
