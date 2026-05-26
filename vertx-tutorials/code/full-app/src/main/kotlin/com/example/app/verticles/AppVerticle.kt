package com.example.app.verticles

import com.example.app.cache.RedisCache
import com.example.app.config.AppConfig
import com.example.app.db.UserRepository
import com.example.app.domain.UserService
import com.example.app.grpc.UserGrpcService
import com.example.app.http.HttpServerFactory
import com.example.app.observability.HealthEndpoints
import io.vertx.core.http.HttpServer
import io.vertx.grpc.server.GrpcServer
import io.vertx.kotlin.coroutines.CoroutineVerticle
import io.vertx.kotlin.coroutines.coAwait
import org.slf4j.LoggerFactory

/**
 * Root verticle. One instance is deployed per event loop. Everything inside
 * a CoroutineVerticle runs on its assigned event-loop thread by default, which
 * means we do not need synchronization between handlers within this instance.
 *
 * Lifecycle:
 *   start() : called once. Build all dependencies, register routes, listen.
 *   stop()  : called on undeploy. Close servers, then pools.
 */
class AppVerticle : CoroutineVerticle() {
    private val log = LoggerFactory.getLogger(AppVerticle::class.java)

    private lateinit var httpServer: HttpServer
    private lateinit var grpcServer: GrpcServer
    private lateinit var grpcHttpServer: HttpServer
    private lateinit var userRepository: UserRepository
    private lateinit var redisCache: RedisCache

    override suspend fun start() {
        val cfg = AppConfig.load(vertx)
        log.info("Starting AppVerticle on event loop: {}", Thread.currentThread().name)

        // ---- Infra ----
        userRepository = UserRepository.create(vertx, cfg.postgres)
        redisCache = RedisCache.create(vertx, cfg.redis)

        // ---- Domain ----
        val userService = UserService(userRepository, redisCache)

        // ---- HTTP REST ----
        val restRouter = HttpServerFactory.buildRestRouter(vertx, userService)
        HealthEndpoints.mount(restRouter, userRepository, redisCache)

        httpServer = vertx.createHttpServer(HttpServerFactory.restOptions(cfg.http))
            .requestHandler(restRouter)
            .listen(cfg.http.port)
            .coAwait()
        log.info("REST listening on :{}", httpServer.actualPort())

        // ---- gRPC ----
        grpcServer = GrpcServer.server(vertx)
        UserGrpcService(userService).bind(grpcServer)
        grpcHttpServer = vertx.createHttpServer(HttpServerFactory.grpcOptions(cfg.grpc))
            .requestHandler(grpcServer)
            .listen(cfg.grpc.port)
            .coAwait()
        log.info("gRPC listening on :{}", grpcHttpServer.actualPort())
    }

    override suspend fun stop() {
        log.info("Stopping AppVerticle...")
        // Stop accepting first, then drain.
        runCatching { httpServer.close().coAwait() }
        runCatching { grpcHttpServer.close().coAwait() }
        runCatching { userRepository.close() }
        runCatching { redisCache.close() }
        log.info("AppVerticle stopped")
    }
}
