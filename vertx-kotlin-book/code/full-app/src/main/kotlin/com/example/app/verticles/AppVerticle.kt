package com.example.app.verticles

import com.example.app.config.AppConfig
import com.example.app.db.DbMigrator
import com.example.app.db.DbModule
import com.example.app.db.UserRepository
import com.example.app.domain.UserService
import com.example.app.grpc.UserGrpcService
import com.example.app.http.Routes
import io.vertx.core.http.HttpServer
import io.vertx.ext.web.Router
import io.vertx.grpc.server.GrpcServer
import io.vertx.kotlin.coroutines.CoroutineVerticle
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.sqlclient.Pool
import org.slf4j.LoggerFactory

/**
 * Composition root verticle.  We deliberately use a single verticle for
 * the whole app: every component shares the same event loop and the same
 * Pool.  Scaling out means deploying many instances of this verticle via
 * DeploymentOptions.setInstances(N).
 *
 *   Vert.x       config → loaded by Main
 *      │
 *      └── AppVerticle.start()
 *               ├── Pool        (postgres)
 *               ├── Repository  (Pool)
 *               ├── Service     (Repository)
 *               ├── HttpServer  (routes)
 *               └── GrpcServer  (services)
 *
 * Resources are released in stop().
 */
class AppVerticle : CoroutineVerticle() {

    private val log = LoggerFactory.getLogger(AppVerticle::class.java)

    private lateinit var pool: Pool
    private lateinit var httpServer: HttpServer
    private lateinit var grpcHttp: HttpServer

    override suspend fun start() {
        val cfg = AppConfig.load(vertx).coAwait()

        pool = DbModule.pool(vertx, cfg.db)
        if (cfg.db.schemaOnStartup) DbMigrator.migrate(pool)

        val repo    = UserRepository(pool)
        val service = UserService(repo)

        // ---- HTTP -----------------------------------------------------
        val router = Router.router(vertx)
        router.route().handler(io.vertx.ext.web.handler.BodyHandler.create())
        Routes(vertx, service).mount(router)
        httpServer = vertx.createHttpServer()
            .requestHandler(router)
            .listen(cfg.http.port, cfg.http.host).coAwait()
        log.info("HTTP server listening on {}:{}", cfg.http.host, cfg.http.port)

        // ---- gRPC -----------------------------------------------------
        val grpcServer = GrpcServer.server(vertx)
        UserGrpcService(vertx, service).bindTo(grpcServer)
        grpcHttp = vertx.createHttpServer()
            .requestHandler(grpcServer)
            .listen(cfg.grpc.port).coAwait()
        log.info("gRPC server listening on :{}", cfg.grpc.port)
    }

    override suspend fun stop() {
        log.info("AppVerticle stopping…")
        runCatching { httpServer.close().coAwait() }
        runCatching { grpcHttp.close().coAwait() }
        runCatching { pool.close().coAwait() }
        log.info("AppVerticle stopped")
    }
}
