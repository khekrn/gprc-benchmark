package com.benchmark.proto.v1;

import io.grpc.BindableService;
import io.quarkus.grpc.GrpcService;
import io.quarkus.grpc.MutinyBean;

@jakarta.annotation.Generated(value = "by Mutiny Grpc generator", comments = "Source: benchmark.proto")
public class BenchmarkServiceBean extends MutinyBenchmarkServiceGrpc.BenchmarkServiceImplBase implements BindableService, MutinyBean {

    private final BenchmarkService delegate;

    BenchmarkServiceBean(@GrpcService BenchmarkService delegate) {
        this.delegate = delegate;
    }

    @Override
    public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request) {
        try {
            return delegate.processUnary(request);
        } catch (UnsupportedOperationException e) {
            throw new io.grpc.StatusRuntimeException(io.grpc.Status.UNIMPLEMENTED);
        }
    }

    @Override
    public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request) {
        try {
            return delegate.healthCheck(request);
        } catch (UnsupportedOperationException e) {
            throw new io.grpc.StatusRuntimeException(io.grpc.Status.UNIMPLEMENTED);
        }
    }

    @Override
    public io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> processStreaming(io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> request) {
        try {
            return delegate.processStreaming(request);
        } catch (UnsupportedOperationException e) {
            throw new io.grpc.StatusRuntimeException(io.grpc.Status.UNIMPLEMENTED);
        }
    }
}
