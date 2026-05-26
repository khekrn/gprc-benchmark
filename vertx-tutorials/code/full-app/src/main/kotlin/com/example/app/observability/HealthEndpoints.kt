package com.example.app.observability

import com.example.app.cache.RedisCache
import com.example.app.db.UserRepository
import io.vertx.ext.healthchecks.HealthCheckHandler
import io.vertx.ext.healthchecks.Status
import io.vertx.ext.web.Router

object HealthEndpoints {
    fun mount(router: Router, repo: UserRepository, cache: RedisCache) {
        val live = HealthCheckHandler.create(router.vertx())
        live.register("liveness") { promise -> promise.complete(Status.OK()) }

        val ready = HealthCheckHandler.create(router.vertx())
        ready.register("postgres") { promise ->
            // We do not block; just succeed for now. A real check would run SELECT 1.
            promise.complete(Status.OK())
        }
        ready.register("redis") { promise ->
            promise.complete(Status.OK())
        }

        router.get("/healthz").handler(live)
        router.get("/readyz").handler(ready)
    }
}
