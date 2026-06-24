package com.beam.bench

import com.beam.bench.proto.CommandRequest
import com.beam.bench.proto.CommandResponse
import com.beam.bench.proto.CommandServiceGrpcKt
import com.beam.bench.proto.GetStateRequest
import com.beam.bench.proto.StateResponse
import org.springframework.stereotype.Service
import java.nio.charset.StandardCharsets
import java.time.Instant

/**
 * Coroutine gRPC service (grpc-kotlin `CoroutineImplBase`): each unary RPC is a
 * `suspend fun`. The FNV-1a touch and receive-time are computed inline, then
 * the suspending R2DBC call runs without blocking the carrier thread — the
 * reactive-coroutine equivalent of kotlin-vertx's `coAwait`.
 */
@Service
class CommandServiceImpl(
    private val db: Db,
) : CommandServiceGrpcKt.CommandServiceCoroutineImplBase() {

    override suspend fun execute(request: CommandRequest): CommandResponse {
        val checksum = fnv1a32(request.payload.toByteArray(StandardCharsets.UTF_8))
        val recvMicros = nowMicros()
        val id = db.insertCommand(
            workflowId = request.workflowId,
            commandType = request.commandType,
            payload = request.payload,
            seq = request.seq,
            checksum = checksum.toLong() and 0xffffffffL,
        )
        return CommandResponse.newBuilder()
            .setId(id)
            .setChecksum(checksum)
            .setReceivedAtMicros(recvMicros)
            .build()
    }

    override suspend fun executeTx(request: CommandRequest): CommandResponse {
        val checksum = fnv1a32(request.payload.toByteArray(StandardCharsets.UTF_8))
        val recvMicros = nowMicros()
        val id = db.executeTx(
            workflowId = request.workflowId,
            commandType = request.commandType,
            payload = request.payload,
            seq = request.seq,
            checksum = checksum.toLong() and 0xffffffffL,
        )
        return CommandResponse.newBuilder()
            .setId(id)
            .setChecksum(checksum)
            .setReceivedAtMicros(recvMicros)
            .build()
    }

    override suspend fun getState(request: GetStateRequest): StateResponse {
        val row = db.getState(request.workflowId)
        return StateResponse.newBuilder()
            .setFound(row != null)
            .setWorkflowId(request.workflowId)
            .setState(row?.state ?: "")
            .setVersion(row?.version ?: 0L)
            .setUpdatedAtMicros(row?.updatedAtMicros ?: 0L)
            .build()
    }

    private fun nowMicros(): Long {
        val now = Instant.now()
        return now.epochSecond * 1_000_000L + now.nano / 1_000L
    }
}
