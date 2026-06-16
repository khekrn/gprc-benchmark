package com.benchmark.proto.v1;

import java.util.function.BiFunction;
import io.quarkus.grpc.MutinyClient;

@jakarta.annotation.Generated(value = "by Mutiny Grpc generator", comments = "Source: benchmark.proto")
public class BenchmarkServiceClient implements BenchmarkService, MutinyClient<MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub> {

    private final MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub stub;

    public BenchmarkServiceClient(String name, io.grpc.Channel channel, BiFunction<String, MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub, MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub> stubConfigurator) {
        this.stub = stubConfigurator.apply(name, MutinyBenchmarkServiceGrpc.newMutinyStub(channel));
    }

    private BenchmarkServiceClient(MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub stub) {
        this.stub = stub;
    }

    public BenchmarkServiceClient newInstanceWithStub(MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub stub) {
        return new BenchmarkServiceClient(stub);
    }

    @Override
    public MutinyBenchmarkServiceGrpc.MutinyBenchmarkServiceStub getStub() {
        return stub;
    }

    @Override
    public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request) {
        return stub.processUnary(request);
    }

    @Override
    public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request) {
        return stub.healthCheck(request);
    }

    @Override
    public io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> processStreaming(io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> request) {
        return stub.processStreaming(request);
    }
}
