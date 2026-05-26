package com.example.app.http

import com.example.app.config.GrpcServerConfig
import com.example.app.config.HttpServerConfig
import com.example.app.domain.UserService
import io.vertx.core.Vertx
import io.vertx.core.http.HttpServerOptions
import io.vertx.core.json.JsonObject
import io.vertx.ext.web.Router
import io.vertx.ext.web.handler.BodyHandler
import io.vertx.ext.web.handler.LoggerHandler
import io.vertx.ext.web.handler.ResponseTimeHandler
import io.vertx.ext.web.handler.TimeoutHandler
import io.vertx.kotlin.coroutines.coroutineRouter
import io.vertx.kotlin.coroutines.CoroutineRouterSupport
import io.vertx.micrometer.PrometheusScrapingHandler
import org.slf4j.LoggerFactory

object HttpServerFactory {
    private val log = LoggerFactory.getLogger("HttpServerFactory")

    fun restOptions(cfg: HttpServerConfig) = HttpServerOptions()
        .setPort(cfg.port)
        .setReusePort(true)
        .setTcpFastOpen(true)
        .setTcpNoDelay(true)
        .setCompressionSupported(true)
        .setHandle100ContinueAutomatically(true)

    fun grpcOptions(cfg: GrpcServerConfig) = HttpServerOptions()
        .setPort(cfg.port)
        .setReusePort(true)
        .setUseAlpn(true)
        // HTTP/2 cleartext (h2c) is fine on a private network; in production add TLS.
        .setHttp2ClearTextEnabled(true)

    fun buildRestRouter(vertx: Vertx, users: UserService): Router {
        val router = Router.router(vertx)

        // Order matters in Vert.x Web. These run for every request.
        router.route()
            .handler(ResponseTimeHandler.create())        // adds x-response-time
            .handler(LoggerHandler.create())              // structured request log
            .handler(BodyHandler.create().setBodyLimit(256 * 1024)) // 256 KB max
            .handler(TimeoutHandler.create(5_000))        // hard 5s timeout

        // Metrics endpoint (Prometheus scrape).
        router.get("/metrics").handler(PrometheusScrapingHandler.create())

        // Domain routes via coroutine-friendly DSL.
        val support = object : CoroutineRouterSupport {}
        support.run {
            router.coroutineRouter {
                route("/api/v1/users/:id").coHandler { ctx ->
                    val id = ctx.pathParam("id")
                    val user = users.get(id)
                    if (user == null) ctx.response().setStatusCode(404).end()
                    else ctx.json(JsonObject.mapFrom(user))
                }
                route(io.vertx.core.http.HttpMethod.POST, "/api/v1/users").coHandler { ctx ->
                    val body = ctx.body().asJsonObject()
                    val u = users.create(
                        email = body.getString("email"),
                        name = body.getString("name"),
                    )
                    ctx.response().setStatusCode(201).end(JsonObject.mapFrom(u).encode())
                }
            }
        }

        // RFC 7807 problem+json error mapping — see chapter 7.
        router.errorHandler(500) { ctx ->
            log.error("Unhandled error on {}", ctx.request().path(), ctx.failure())
            ctx.response()
                .putHeader("Content-Type", "application/problem+json")
                .setStatusCode(500)
                .end(
                    JsonObject()
                        .put("type", "about:blank")
                        .put("title", "Internal Server Error")
                        .put("status", 500)
                        .encode()
                )
        }
        return router
    }
}
