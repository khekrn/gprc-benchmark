package com.example.app.http

import com.example.app.domain.NewUser
import com.example.app.domain.UserError
import com.example.app.domain.UserService
import com.example.app.observability.Metrics
import com.example.app.observability.MDCContext
import io.vertx.core.Vertx
import io.vertx.core.json.JsonArray
import io.vertx.core.json.JsonObject
import io.vertx.ext.web.Router
import io.vertx.ext.web.RoutingContext
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.kotlin.coroutines.dispatcher
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.flow.collect
import kotlinx.coroutines.launch
import kotlinx.coroutines.withContext
import org.slf4j.LoggerFactory
import java.util.UUID

/**
 * HTTP routes.  Each handler is non-blocking and uses coroutines to
 * sequence async calls without callbacks.
 *
 * We mount routes on a Router built by the verticle and let the verticle
 * own the HttpServer.  This separation keeps the verticle small.
 */
class Routes(
    private val vertx: Vertx,
    private val users: UserService,
) {
    private val log = LoggerFactory.getLogger(Routes::class.java)
    private val scope = CoroutineScope(SupervisorJob() + vertx.dispatcher())

    fun mount(router: Router) {
        router.route().handler { ctx ->
            // request-id tagging for log correlation
            val reqId = ctx.request().getHeader("X-Request-Id") ?: UUID.randomUUID().toString()
            ctx.put("requestId", reqId)
            ctx.response().putHeader("X-Request-Id", reqId)
            ctx.next()
        }

        router.route().handler { ctx ->
            val started = System.nanoTime()
            ctx.addEndHandler {
                val elapsedNanos = System.nanoTime() - started
                Metrics.registry.timer("http.request",
                    "method", ctx.request().method().name(),
                    "status", ctx.response().statusCode.toString()
                ).record(elapsedNanos, java.util.concurrent.TimeUnit.NANOSECONDS)
            }
            ctx.next()
        }

        router.errorHandler(500) { ctx -> writeProblem(ctx, 500, "Internal Server Error", ctx.failure()?.message) }
        router.errorHandler(404) { ctx -> writeProblem(ctx, 404, "Not Found", null) }

        // ----- routes ---------------------------------------------------
        router.get("/healthz").handler  { it.response().end("ok") }
        router.get("/readyz").handler   { it.response().end("ok") }
        router.get("/metrics").handler  { ctx -> ctx.response()
            .putHeader("Content-Type", "text/plain; version=0.0.4")
            .end(Metrics.registry.scrape()) }

        router.get("/api/users/:id").coHandler { ctx -> handleGetUser(ctx) }
        router.post("/api/users").coHandler   { ctx -> handleCreateUser(ctx) }
        router.post("/api/users/bulk").coHandler { ctx -> handleBulkCreate(ctx) }
        router.get("/api/users").coHandler    { ctx -> handleStreamUsers(ctx) }
    }

    private suspend fun handleGetUser(ctx: RoutingContext) {
        val id = ctx.pathParam("id").toLongOrNull()
            ?: return writeProblem(ctx, 400, "Bad Request", "id must be Long")
        try {
            val u = users.getById(id)
            ctx.response().putHeader("Content-Type", "application/json")
                .end(JsonObject.mapFrom(u).encode())
        } catch (e: UserError.NotFound) {
            writeProblem(ctx, 404, "Not Found", e.message)
        }
    }

    private suspend fun handleCreateUser(ctx: RoutingContext) {
        val body = runCatching { ctx.body().asJsonObject() }.getOrNull()
            ?: return writeProblem(ctx, 400, "Bad Request", "json expected")
        val input = try {
            NewUser(
                email    = body.getString("email"),
                fullName = body.getString("fullName"),
            )
        } catch (e: IllegalArgumentException) {
            return writeProblem(ctx, 400, "Bad Request", e.message)
        }
        try {
            val u = users.create(input)
            ctx.response().setStatusCode(201)
                .putHeader("Content-Type", "application/json")
                .end(JsonObject.mapFrom(u).encode())
        } catch (e: UserError.DuplicateEmail) {
            writeProblem(ctx, 409, "Conflict", e.message)
        }
    }

    private suspend fun handleBulkCreate(ctx: RoutingContext) {
        val arr = runCatching { ctx.body().asJsonArray() }.getOrNull()
            ?: return writeProblem(ctx, 400, "Bad Request", "array expected")
        val inputs = arr.map { e ->
            val obj = e as JsonObject
            NewUser(obj.getString("email"), obj.getString("fullName"))
        }
        val created = users.bulkCreate(inputs)
        val out = JsonArray(created.map { JsonObject.mapFrom(it) })
        ctx.response().setStatusCode(201)
            .putHeader("Content-Type", "application/json")
            .end(out.encode())
    }

    /**
     * Stream rows as NDJSON (one JSON object per line).  Demonstrates how
     * coroutine Flows pair with chunked HTTP responses, including
     * back-pressure: writeAwait() suspends if the socket buffer is full.
     */
    private suspend fun handleStreamUsers(ctx: RoutingContext) {
        val prefix = ctx.request().getParam("emailPrefix")
        val resp = ctx.response()
            .putHeader("Content-Type", "application/x-ndjson")
            .setChunked(true)

        users.streamAll(prefix).collect { user ->
            val line = JsonObject.mapFrom(user).encode() + "\n"
            // writeBuffer back-pressure: if the channel says "writeQueueFull"
            // we wait for drain.  This is what makes back-pressure end-to-end:
            // socket pressure → PG cursor pause via the channel buffer.
            if (resp.writeQueueFull()) {
                io.vertx.core.Future.future<Void> { p ->
                    resp.drainHandler { p.complete() }
                }.coAwait()
            }
            resp.write(line)
        }
        resp.end()
    }

    // -------- helpers ---------------------------------------------------

    private fun writeProblem(ctx: RoutingContext, status: Int, title: String, detail: String?) {
        val problem = JsonObject()
            .put("type", "about:blank")
            .put("title", title)
            .put("status", status)
            .apply { if (detail != null) put("detail", detail) }
        ctx.response().setStatusCode(status)
            .putHeader("Content-Type", "application/problem+json")
            .end(problem.encode())
    }

    private fun io.vertx.ext.web.Route.coHandler(block: suspend (RoutingContext) -> Unit) =
        handler { ctx ->
            val reqId = ctx.get<String>("requestId") ?: "-"
            scope.launch(MDCContext(mapOf("requestId" to reqId))) {
                try {
                    block(ctx)
                } catch (t: Throwable) {
                    log.error("unhandled in route {}", ctx.request().path(), t)
                    if (!ctx.response().ended()) writeProblem(ctx, 500, "Internal Server Error", t.message)
                }
            }
        }
}
