package com.beam.bench;

import com.beam.bench.proto.CommandRequest;
import com.beam.bench.proto.CommandResponse;
import com.beam.bench.proto.CommandServiceGrpc;
import com.beam.bench.proto.GetStateRequest;
import com.beam.bench.proto.StateResponse;
import io.grpc.stub.StreamObserver;
import org.springframework.stereotype.Service;

import java.nio.charset.StandardCharsets;
import java.time.Instant;

/**
 * gRPC service over the standard grpc-java {@code ImplBase} (StreamObserver
 * style). It is a {@code BindableService} Spring bean, so Spring Boot's gRPC
 * auto-configuration registers it with the Netty server automatically.
 *
 * With {@code spring.threads.virtual.enabled=true} the request handlers run on
 * virtual threads, so the blocking JDBC work in {@link Db} parks the carrier
 * thread on I/O. Method bodies mirror the other stacks: FNV-1a touch of the
 * payload, capture receive time, one DB call, emit the response.
 */
@Service
public class CommandServiceImpl extends CommandServiceGrpc.CommandServiceImplBase {

    private final Db db;

    public CommandServiceImpl(Db db) {
        this.db = db;
    }

    @Override
    public void execute(CommandRequest request, StreamObserver<CommandResponse> responseObserver) {
        int checksum = Fnv.fnv1a32(request.getPayload().getBytes(StandardCharsets.UTF_8));
        long recvMicros = nowMicros();
        try {
            long id = db.insertCommand(
                    request.getWorkflowId(),
                    request.getCommandType(),
                    request.getPayload(),
                    request.getSeq(),
                    checksum & 0xffffffffL);
            responseObserver.onNext(CommandResponse.newBuilder()
                    .setId(id)
                    .setChecksum(checksum)
                    .setReceivedAtMicros(recvMicros)
                    .build());
            responseObserver.onCompleted();
        } catch (Exception e) {
            responseObserver.onError(e);
        }
    }

    @Override
    public void executeTx(CommandRequest request, StreamObserver<CommandResponse> responseObserver) {
        int checksum = Fnv.fnv1a32(request.getPayload().getBytes(StandardCharsets.UTF_8));
        long recvMicros = nowMicros();
        try {
            long id = db.executeTx(
                    request.getWorkflowId(),
                    request.getCommandType(),
                    request.getPayload(),
                    request.getSeq(),
                    checksum & 0xffffffffL);
            responseObserver.onNext(CommandResponse.newBuilder()
                    .setId(id)
                    .setChecksum(checksum)
                    .setReceivedAtMicros(recvMicros)
                    .build());
            responseObserver.onCompleted();
        } catch (Exception e) {
            responseObserver.onError(e);
        }
    }

    @Override
    public void getState(GetStateRequest request, StreamObserver<StateResponse> responseObserver) {
        try {
            Db.StateRow row = db.getState(request.getWorkflowId());
            responseObserver.onNext(StateResponse.newBuilder()
                    .setFound(row.found())
                    .setWorkflowId(row.found() ? row.workflowId() : request.getWorkflowId())
                    .setState(row.state())
                    .setVersion(row.version())
                    .setUpdatedAtMicros(row.updatedAtMicros())
                    .build());
            responseObserver.onCompleted();
        } catch (Exception e) {
            responseObserver.onError(e);
        }
    }

    /** Wall-clock microseconds since epoch (matches the other stacks). */
    private static long nowMicros() {
        Instant now = Instant.now();
        return now.getEpochSecond() * 1_000_000L + now.getNano() / 1_000L;
    }
}
