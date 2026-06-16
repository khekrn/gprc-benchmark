package com.benchmark.proto.v1;

import io.quarkus.grpc.MutinyService;

@jakarta.annotation.Generated(value = "by Mutiny Grpc generator", comments = "Source: benchmark.proto")
public interface BenchmarkService extends MutinyService {

    io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request);

    /**
     * <pre>
     *  &quot;unary&quot; or &quot;streaming&quot;
     * </pre>
     */
    io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request);

    io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> processStreaming(io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> request);
}
