package com.example.app.domain

import com.example.app.db.UserRepository
import kotlinx.coroutines.flow.Flow

/**
 * Thin application-service layer.  Business logic that doesn't belong in the
 * repository (validation orchestration, cross-aggregate checks) lives here.
 * Keeping it separate makes it easy to test without a database.
 */
class UserService(private val repo: UserRepository) {

    suspend fun getById(id: Long): User =
        repo.findById(id) ?: throw UserError.NotFound(id)

    suspend fun create(input: NewUser): User {
        // Example: enforce a uniqueness check up front to return a friendly
        // 409 without relying on the DB error.  We still catch DuplicateEmail
        // from the repository because race conditions exist.
        repo.findByEmail(input.email)?.let { throw UserError.DuplicateEmail(input.email) }
        return repo.create(input)
    }

    suspend fun bulkCreate(inputs: List<NewUser>): List<User> =
        repo.createMany(inputs)

    fun streamAll(emailPrefix: String?): Flow<User> =
        repo.streamAll(emailPrefix)
}
