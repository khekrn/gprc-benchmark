package com.example.app.grpc

import com.example.app.domain.UserService
import com.example.app.grpc.v1.CreateUserRequest
import com.example.app.grpc.v1.CreateUserResponse
import com.example.app.grpc.v1.GetUserRequest
import com.example.app.grpc.v1.GetUserResponse
import com.example.app.grpc.v1.User
import com.example.app.grpc.v1.VertxUserServiceGrpcServer
import com.example.app.grpc.v1.VertxUserServiceGrpcServer.UserServiceApi
import io.grpc.Status
import io.vertx.core.Future
import io.vertx.grpc.server.GrpcServer
import io.vertx.kotlin.coroutines.dispatcher
import io.vertx.kotlin.coroutines.vertxFuture
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob

/**
 * Implements the generated UserServiceApi using suspending domain code.
 *
 * vertx-grpc 5 generates a Vert.x-style API where each method returns a Future.
 * We bridge from Future to suspend with `vertxFuture { ... }`, which launches
 * a coroutine on the calling event loop's dispatcher and completes the Future
 * with the result. This is the idiomatic way to expose suspending code to
 * Vert.x APIs that expect a Future.
 */
class UserGrpcService(
    private val users: UserService,
) : UserServiceApi {

    fun bind(server: GrpcServer) {
        VertxUserServiceGrpcServer.bindAll(server, this)
    }

    override fun getUser(request: GetUserRequest): Future<GetUserResponse> = vertxFuture {
        val user = users.get(request.id)
            ?: throw Status.NOT_FOUND.withDescription("user ${request.id}").asRuntimeException()
        GetUserResponse.newBuilder().setUser(user.toProto()).build()
    }

    override fun createUser(request: CreateUserRequest): Future<CreateUserResponse> = vertxFuture {
        if (request.email.isBlank())
            throw Status.INVALID_ARGUMENT.withDescription("email required").asRuntimeException()
        val u = users.create(email = request.email, name = request.name)
        CreateUserResponse.newBuilder().setUser(u.toProto()).build()
    }

    private fun com.example.app.domain.User.toProto(): User = User.newBuilder()
        .setId(id)
        .setEmail(email)
        .setName(name)
        .setCreatedAtEpochMillis(createdAtEpochMillis)
        .build()
}
