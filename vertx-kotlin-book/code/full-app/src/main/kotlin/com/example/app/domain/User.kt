package com.example.app.domain

import java.time.OffsetDateTime

/** Domain entity.  Repository row mappers translate to/from this. */
data class User(
    val id: Long,
    val email: String,
    val fullName: String,
    val createdAt: OffsetDateTime,
)

/** Input DTO used by HTTP and gRPC create paths. */
data class NewUser(val email: String, val fullName: String) {
    init {
        require(email.contains('@')) { "email must contain '@'" }
        require(fullName.isNotBlank()) { "fullName must not be blank" }
        require(email.length <= 320) { "email too long" }
        require(fullName.length <= 200) { "fullName too long" }
    }
}

sealed class UserError(msg: String) : RuntimeException(msg) {
    class NotFound(id: Long) : UserError("user $id not found")
    class DuplicateEmail(email: String) : UserError("email already exists: $email")
    class Validation(msg: String) : UserError(msg)
}
