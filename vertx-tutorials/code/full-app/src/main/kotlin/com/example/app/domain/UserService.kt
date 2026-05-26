package com.example.app.domain

import com.example.app.cache.RedisCache
import com.example.app.db.UserRepository
import org.slf4j.LoggerFactory

/**
 * The thin domain layer. All I/O is async — every call is a suspend function.
 * This is where we wire caching (read-through) on top of the database.
 */
class UserService(
    private val repo: UserRepository,
    private val cache: RedisCache,
) {
    private val log = LoggerFactory.getLogger(UserService::class.java)

    suspend fun get(id: String): User? {
        cache.getJson<User>(cacheKey(id))?.let { return it }
        val row = repo.findById(id) ?: return null
        cache.putJson(cacheKey(id), row, ttlSeconds = 300)
        return row
    }

    suspend fun create(email: String, name: String): User {
        val u = repo.insert(email = email, name = name)
        cache.putJson(cacheKey(u.id), u, ttlSeconds = 300)
        return u
    }

    private fun cacheKey(id: String) = "user:$id"
}

data class User(
    val id: String,
    val email: String,
    val name: String,
    val createdAtEpochMillis: Long,
)
