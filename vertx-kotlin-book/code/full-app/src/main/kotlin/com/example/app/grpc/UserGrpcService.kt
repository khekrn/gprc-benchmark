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
import com.example.app.grpc.proto.UsersGrpcService
import com.example.app.grpc.proto.UsersService
import io.vertx.core.Future
import io.vertx.core.Vertx
import io.vertx.core.streams.ReadStream
import io.vertx.core.streams.WriteStream
import io.vertx.grpc.common.GrpcStatus
import io.vertx.grpc.server.GrpcServer
import io.vertx.grpc.server.GrpcServerResponse
import io.vertx.grpc.server.StatusException
import io.vertx.kotlin.coroutines.coAwait
import io.vertx.kotlin.coroutines.dispatcher
import io.vertx.kotlin.coroutines.vertxFuture
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.launch
import org.slf4j.LoggerFactory
import java.time.format.DateTimeFormatter
import java.util.concurrent.atomic.AtomicLong

/**
 * gRPC service implementation covering every RPC style, written with the
 * modern Vert.x 5 coroutine bridge.
 *
 * The protoc plugin generates an abstract [UsersService] we extend.  Unary and
 * client-streaming methods return a `Future`, so we build them from a coroutine
 * with [vertxFuture].  Server-streaming and bidi methods are `void` and write
 * to a [WriteStream]; we drive those from a coroutine launched on the verticle
 * dispatcher (streaming) or from plain stream handlers (chat).
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
) : UsersService() {

    private val log = LoggerFactory.getLogger(UserGrpcService::class.java)
    private val scope = CoroutineScope(SupervisorJob() + vertx.dispatcher())

    fun bindTo(server: GrpcServer) {
        // addService (not bind) so the service is also listed for gRPC reflection.
        server.addService(UsersGrpcService.of(this))
    }

    // ----- Unary ------------------------------------------------------
    override fun getUser(request: GetUserRequest): Future<UserReply> = vertxFuture(vertx, scope) {
        try {
            users.getById(request.id).toReply()
        } catch (e: UserError.NotFound) {
            throw StatusException(GrpcStatus.NOT_FOUND, e.message)
        }
    }

    override fun createUser(request: CreateUserRequest): Future<UserReply> = vertxFuture(vertx, scope) {
        try {
            users.create(NewUser(request.email, request.fullName)).toReply()
        } catch (e: UserError.DuplicateEmail) {
            throw StatusException(GrpcStatus.ALREADY_EXISTS, e.message)
        } catch (e: IllegalArgumentException) {
            throw StatusException(GrpcStatus.INVALID_ARGUMENT, e.message)
        }
    }

    // ----- Server streaming -------------------------------------------
    // The generated signature is `void listUsers(request, WriteStream)`.  We
    // launch a coroutine so the body reads top-to-bottom; back-pressure is
    // honoured by awaiting drain when the write queue is full.
    override fun listUsers(request: ListUsersRequest, response: WriteStream<UserReply>) {
        scope.launch {
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
            } catch (t: Throwable) {
                log.error("listUsers failed after {} rows", n, t)
                (response as? GrpcServerResponse<*, *>)?.status(GrpcStatus.INTERNAL)?.end()
            }
        }
    }

    // ----- Client streaming -------------------------------------------
    // Collect the inbound stream via handlers, await the client half-close,
    // then perform the inserts as a coroutine.
    override fun importUsers(request: ReadStream<CreateUserRequest>): Future<ImportSummary> = vertxFuture(vertx, scope) {
        val inbox = java.util.concurrent.ConcurrentLinkedQueue<CreateUserRequest>()
        Future.future<Void> { p ->
            request.handler { req -> inbox.add(req) }
            request.endHandler { p.complete() }
            request.exceptionHandler { t -> p.fail(t) }
        }.coAwait()   // suspend until client sends its half-close

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
    // `void chat(request, WriteStream)`.  Pure stream handlers, no coroutine
    // needed: each inbound message produces one echo on the response stream.
    override fun chat(request: ReadStream<ChatMessage>, response: WriteStream<ChatMessage>) {
        request.handler { msg ->
            val reply = ChatMessage.newBuilder()
                .setFrom("server")
                .setText("echo: ${msg.text}")
                .setTsMillis(System.currentTimeMillis())
                .build()
            response.write(reply)
        }
        request.endHandler { response.end() }
        request.exceptionHandler { t ->
            log.warn("chat error", t)
            (response as? GrpcServerResponse<*, *>)?.status(GrpcStatus.INTERNAL)?.end()
        }
    }

    private fun User.toReply(): UserReply =
        UserReply.newBuilder()
            .setId(id)
            .setEmail(email)
            .setFullName(fullName)
            .setCreatedAt(createdAt.format(DateTimeFormatter.ISO_OFFSET_DATE_TIME))
            .build()
}
