package com.example.app.cache

import com.example.app.config.RedisConfig
import io.vertx.core.Vertx
import io.vertx.core.json.Json
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.redis.client.Redis
import io.vertx.redis.client.RedisAPI
import io.vertx.redis.client.RedisOptions

class RedisCache private constructor(
    private val client: Redis,
    private val api: RedisAPI,
) {
    suspend inline fun <reified T : Any> getJson(key: String): T? {
        val resp = api.get(key).coAwait() ?: return null
        val s = resp.toString() ?: return null
        return Json.decodeValue(s, T::class.java)
    }

    suspend fun <T : Any> putJson(key: String, value: T, ttlSeconds: Long) {
        val s = Json.encode(value)
        api.set(listOf(key, s, "EX", ttlSeconds.toString())).coAwait()
    }

    suspend fun delete(key: String) {
        api.del(listOf(key)).coAwait()
    }

    suspend fun ping(): Boolean = runCatching { api.ping(emptyList()).coAwait() }.isSuccess

    fun close() = client.close()

    companion object {
        fun create(vertx: Vertx, cfg: RedisConfig): RedisCache {
            val options = RedisOptions()
                .setConnectionString(cfg.uri)
                .setMaxPoolSize(cfg.maxPoolSize)
                .setMaxWaitingHandlers(2048)
            val client = Redis.createClient(vertx, options)
            return RedisCache(client, RedisAPI.api(client))
        }
    }
}
