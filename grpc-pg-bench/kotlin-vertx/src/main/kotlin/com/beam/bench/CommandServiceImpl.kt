package com.beam.bench

import com.beam.bench.proto.CommandRequest
import com.beam.bench.proto.CommandResponse
import com.beam.bench.proto.CommandServiceGrpcService
import io.vertx.core.Future
import io.vertx.kotlin.coroutines.vertxFuture
import kotlinx.coroutines.CoroutineScope
import java.nio.charset.StandardCharsets
import java.time.Instant

/**
 * gRPC service implementation.
 *
 * The Vert.x protoc plugin generates two base classes per service:
 *  - `CommandServiceService` — the override target with `Future<T>` methods.
 *  - `CommandServiceGrpcService` — extends the above AND implements
 *    `io.vertx.grpc.server.Service`, so it can be handed directly to
 *    `GrpcServer.addService`.
 *
 * We extend the *GrpcService* variant so [GrpcVerticle] can register us
 * without an extra wrapper.
 *
 * We bridge to coroutines via [vertxFuture] from the owning verticle's scope:
 * the suspend block runs on the verticle's event-loop dispatcher (no thread
 * hop), and its result completes the returned Future. Because vertx-pg-client
 * is non-blocking, the whole call stays on the event loop — `coAwait` only
 * yields the loop while waiting on the network, never blocks it.
 */
class CommandServiceImpl(
    private val db: Db,
    private val scope: CoroutineScope,
) : CommandServiceGrpcService() {

    override fun execute(request: CommandRequest): Future<CommandResponse> = vertxFuture(scope) {
        val checksum = fnv1a32(request.payload.toByteArray(StandardCharsets.UTF_8))
        val recvMicros = nowMicros()

        val id = db.insertCommand(
            workflowId = request.workflowId,
            commandType = request.commandType,
            payload = request.payload,
            seq = request.seq,
            // FNV-1a is a uint32 in Go; widen to long without sign extension.
            checksum = checksum.toLong() and 0xffffffffL,
        )

        CommandResponse.newBuilder()
            .setId(id)
            .setChecksum(checksum)
            .setReceivedAtMicros(recvMicros)
            .build()
    }

    /** Wall clock time in microseconds since epoch. */
    private fun nowMicros(): Long {
        val now = Instant.now()
        return now.epochSecond * 1_000_000L + now.nano / 1_000L
    }
}
