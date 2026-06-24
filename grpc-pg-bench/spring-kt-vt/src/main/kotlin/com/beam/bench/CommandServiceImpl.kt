package com.beam.bench

import com.beam.bench.proto.CommandRequest
import com.beam.bench.proto.CommandResponse
import com.beam.bench.proto.CommandServiceGrpc
import com.beam.bench.proto.GetStateRequest
import com.beam.bench.proto.StateResponse
import io.grpc.stub.StreamObserver
import org.springframework.stereotype.Service
import java.nio.charset.StandardCharsets
import java.time.Instant

/**
 * gRPC service over the standard grpc-java `ImplBase` (StreamObserver style) —
 * the plain Java stubs, called from Kotlin. NOT coroutines (spring-rt covers
 * that). It is a `BindableService` Spring bean, so Boot's gRPC
 * auto-configuration registers it with the Netty server automatically.
 *
 * With `spring.threads.virtual.enabled=true` plus the explicit VT executor in
 * [GrpcServerConfig], the request handlers run on virtual threads, so the
 * blocking JDBC work in [Db] parks the carrier thread on I/O. Method bodies
 * mirror the other stacks: FNV-1a touch of the payload, capture receive time,
 * one DB call, emit the response.
 */
@Service
class CommandServiceImpl(
    private val db: Db,
) : CommandServiceGrpc.CommandServiceImplBase() {

    override fun execute(request: CommandRequest, responseObserver: StreamObserver<CommandResponse>) {
        val checksum = fnv1a32(request.payload.toByteArray(StandardCharsets.UTF_8))
        val recvMicros = nowMicros()
        try {
            val id = db.insertCommand(
                workflowId = request.workflowId,
                commandType = request.commandType,
                payload = request.payload,
                seq = request.seq,
                checksum = checksum.toLong() and 0xffffffffL,
            )
            responseObserver.onNext(
                CommandResponse.newBuilder()
                    .setId(id)
                    .setChecksum(checksum)
                    .setReceivedAtMicros(recvMicros)
                    .build(),
            )
            responseObserver.onCompleted()
        } catch (e: Exception) {
            responseObserver.onError(e)
        }
    }

    override fun executeTx(request: CommandRequest, responseObserver: StreamObserver<CommandResponse>) {
        val checksum = fnv1a32(request.payload.toByteArray(StandardCharsets.UTF_8))
        val recvMicros = nowMicros()
        try {
            val id = db.executeTx(
                workflowId = request.workflowId,
                commandType = request.commandType,
                payload = request.payload,
                seq = request.seq,
                checksum = checksum.toLong() and 0xffffffffL,
            )
            responseObserver.onNext(
                CommandResponse.newBuilder()
                    .setId(id)
                    .setChecksum(checksum)
                    .setReceivedAtMicros(recvMicros)
                    .build(),
            )
            responseObserver.onCompleted()
        } catch (e: Exception) {
            responseObserver.onError(e)
        }
    }

    override fun getState(request: GetStateRequest, responseObserver: StreamObserver<StateResponse>) {
        try {
            val row = db.getState(request.workflowId)
            responseObserver.onNext(
                StateResponse.newBuilder()
                    .setFound(row.found)
                    .setWorkflowId(if (row.found) row.workflowId else request.workflowId)
                    .setState(row.state)
                    .setVersion(row.version)
                    .setUpdatedAtMicros(row.updatedAtMicros)
                    .build(),
            )
            responseObserver.onCompleted()
        } catch (e: Exception) {
            responseObserver.onError(e)
        }
    }

    /** Wall-clock microseconds since epoch (matches the other stacks). */
    private fun nowMicros(): Long {
        val now = Instant.now()
        return now.epochSecond * 1_000_000L + now.nano / 1_000L
    }
}
