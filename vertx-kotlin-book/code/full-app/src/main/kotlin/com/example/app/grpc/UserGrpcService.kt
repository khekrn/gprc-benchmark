package com.example.app.grpc

import com.example.app.domain.NewUser
import com.example.app.domain.User
import com.example.app.domain.UserError
import com.example.app.domain.UserService
import com.example.app.grpc.proto.ChatMessage
import com.example.app.grpc.proto.CreateUserRequest
import com.example.app.grpc.proto.GetUserRequest
import com.example.app.grpc.proto.ImportSummary
import com.example.app.grpc.proto.ListUsersRequest
import com.example.app.grpc.proto.UserReply
import com.example.app.grpc.proto.UsersService
import com.example.app.grpc.proto.VertxUsersGrpcServer
import io.vertx.core.Future
import io.vertx.core.Vertx
import io.vertx.core.streams.ReadStream
import io.vertx.core.streams.WriteStream
import io.vertx.grpc.common.GrpcException
import io.vertx.grpc.common.GrpcStatus
import io.vertx.grpc.server.GrpcServer
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.kotlin.coroutines.vertxFuture
import kotlinx.coroutines.flow.collect
import org.slf4j.LoggerFactory
import java.time.format.DateTimeFormatter
import java.util.concurrent.atomic.AtomicLong

/**
 * gRPC service implementation covering every RPC style, written with the
 * modern coroutine bridge: each method body is a suspending block inside
 * [vertxFuture] so the code reads top-to-bottom — no callbacks.
 *
 *   getUser    : Unary
 *   createUser : Unary
 *   listUsers  : Server streaming
 *   importUsers: Client streaming
 *   chat       : Bidirectional streaming
 *
 * Bind to a GrpcServer with [bindTo].
 */
class UserGrpcService(
    private val vertx: Vertx,
    private val users: UserService,
) : UsersService {

    private val log = LoggerFactory.getLogger(UserGrpcService::class.java)

    fun bindTo(server: GrpcServer) {
        VertxUsersGrpcServer.register(server, this)
    }

    // ----- Unary ------------------------------------------------------
    override fun getUser(request: GetUserRequest): Future<UserReply> = vertxFuture {
        try {
            users.getById(request.id).toReply()
        } catch (e: UserError.NotFound) {
            throw GrpcException(GrpcStatus.NOT_FOUND, e.message)
        }
    }

    override fun createUser(request: CreateUserRequest): Future<UserReply> = vertxFuture {
        try {
            users.create(NewUser(request.email, request.fullName)).toReply()
        } catch (e: UserError.DuplicateEmail) {
            throw GrpcException(GrpcStatus.ALREADY_EXISTS, e.message)
        } catch (e: IllegalArgumentException) {
            throw GrpcException(GrpcStatus.INVALID_ARGUMENT, e.message)
        }
    }

    // ----- Server streaming -------------------------------------------
    override fun listUsers(
        request: ListUsersRequest,
        response: WriteStream<UserReply>,
    ): Future<Void> = vertxFuture {
        var n = 0L
        try {
            users.streamAll(request.emailPrefix.ifBlank { null }).collect { u ->
                if (response.writeQueueFull()) {
                    Future.future<Void> { p -> response.drainHandler { p.complete() } }.coAwait()
                }
                response.write(u.toReply())
                n++
            }
            response.end().coAwait()
            log.debug("listUsers sent {} rows", n)
            null
        } catch (t: Throwable) {
            log.error("listUsers failed after {} rows", n, t)
            throw t
        }
    }

    // ----- Client streaming -------------------------------------------
    // We collect the inbound stream into a list synchronously via handlers,
    // then await the end via a Future, then perform inserts as a coroutine.
    override fun importUsers(request: ReadStream<CreateUserRequest>): Future<ImportSummary> = vertxFuture {
        val inbox = java.util.concurrent.ConcurrentLinkedQueue<CreateUserRequest>()
        val done = Future.future<Void> { p ->
            request.handler { req -> inbox.add(req) }
            request.endHandler { p.complete() }
            request.exceptionHandler { t -> p.fail(t) }
        }
        done.coAwait()    // suspend until client sends its half-close

        val imported = AtomicLong()
        val skipped  = AtomicLong()
        val errors   = mutableListOf<String>()
        for (req in inbox) {
            try {
                users.create(NewUser(req.email, req.fullName))
                imported.incrementAndGet()
            } catch (e: UserError.DuplicateEmail) {
                skipped.incrementAndGet()
            } catch (t: Throwable) {
                skipped.incrementAndGet()
                errors.add(t.message ?: "unknown")
            }
        }
        ImportSummary.newBuilder()
            .setImported(imported.get())
            .setSkipped(skipped.get())
            .addAllErrors(errors)
            .build()
    }

    // ----- Bidirectional streaming ------------------------------------
    override fun chat(
        request: ReadStream<ChatMessage>,
        response: WriteStream<ChatMessage>,
    ): Future<Void> = vertxFuture {
        Future.future<Void> { p ->
            request.handler { msg ->
                val reply = ChatMessage.newBuilder()
                    .setFrom("server")
                    .setText("echo: ${msg.text}")
                    .setTsMillis(System.currentTimeMillis())
                    .build()
                response.write(reply)
            }
            request.endHandler {
                response.end().onComplete { p.handle(it) }
            }
            request.exceptionHandler { t ->
                log.warn("chat error", t)
                p.fail(t)
            }
        }.coAwait()
        null
    }

    private fun User.toReply(): UserReply =
        UserReply.newBuilder()
            .setId(id)
            .setEmail(email)
            .setFullName(fullName)
            .setCreatedAt(createdAt.format(DateTimeFormatter.ISO_OFFSET_DATE_TIME))
            .build()
}
