package com.benchmark.proto.v1;

import static com.benchmark.proto.v1.BenchmarkServiceGrpc.getServiceDescriptor;
import static io.grpc.stub.ServerCalls.asyncUnaryCall;
import static io.grpc.stub.ServerCalls.asyncServerStreamingCall;
import static io.grpc.stub.ServerCalls.asyncClientStreamingCall;
import static io.grpc.stub.ServerCalls.asyncBidiStreamingCall;

@jakarta.annotation.Generated(value = "by Mutiny Grpc generator", comments = "Source: benchmark.proto")
public final class MutinyBenchmarkServiceGrpc implements io.quarkus.grpc.MutinyGrpc {

    private MutinyBenchmarkServiceGrpc() {
    }

    public static MutinyBenchmarkServiceStub newMutinyStub(io.grpc.Channel channel) {
        return new MutinyBenchmarkServiceStub(channel);
    }

    /**
     * <pre>
     *  Service definitions
     * </pre>
     */
    public static class MutinyBenchmarkServiceStub extends io.grpc.stub.AbstractStub<MutinyBenchmarkServiceStub> implements io.quarkus.grpc.MutinyStub {

        private BenchmarkServiceGrpc.BenchmarkServiceStub delegateStub;

        private MutinyBenchmarkServiceStub(io.grpc.Channel channel) {
            super(channel);
            delegateStub = BenchmarkServiceGrpc.newStub(channel);
        }

        private MutinyBenchmarkServiceStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            super(channel, callOptions);
            delegateStub = BenchmarkServiceGrpc.newStub(channel).build(channel, callOptions);
        }

        @Override
        protected MutinyBenchmarkServiceStub build(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            return new MutinyBenchmarkServiceStub(channel, callOptions);
        }

        public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request) {
            return io.quarkus.grpc.stubs.ClientCalls.oneToOne(request, delegateStub::processUnary);
        }

        /**
         * <pre>
         *  &quot;unary&quot; or &quot;streaming&quot;
         * </pre>
         */
        public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request) {
            return io.quarkus.grpc.stubs.ClientCalls.oneToOne(request, delegateStub::healthCheck);
        }

        public io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> processStreaming(io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> request) {
            return io.quarkus.grpc.stubs.ClientCalls.manyToMany(request, delegateStub::processStreaming);
        }
    }

    /**
     * <pre>
     *  Service definitions
     * </pre>
     */
    public static abstract class BenchmarkServiceImplBase implements io.grpc.BindableService {

        private String compression;

        /**
         * Set whether the server will try to use a compressed response.
         *
         * @param compression the compression, e.g {@code gzip}
         */
        public BenchmarkServiceImplBase withCompression(String compression) {
            this.compression = compression;
            return this;
        }

        public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request) {
            throw new io.grpc.StatusRuntimeException(io.grpc.Status.UNIMPLEMENTED);
        }

        /**
         * <pre>
         *  &quot;unary&quot; or &quot;streaming&quot;
         * </pre>
         */
        public io.smallrye.mutiny.Uni<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request) {
            throw new io.grpc.StatusRuntimeException(io.grpc.Status.UNIMPLEMENTED);
        }

        public io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> processStreaming(io.smallrye.mutiny.Multi<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> request) {
            throw new io.grpc.StatusRuntimeException(io.grpc.Status.UNIMPLEMENTED);
        }

        @java.lang.Override
        public io.grpc.ServerServiceDefinition bindService() {
            return io.grpc.ServerServiceDefinition.builder(getServiceDescriptor()).addMethod(com.benchmark.proto.v1.BenchmarkServiceGrpc.getProcessUnaryMethod(), asyncUnaryCall(new MethodHandlers<com.benchmark.proto.v1.BenchmarkProto.UnaryRequest, com.benchmark.proto.v1.BenchmarkProto.UnaryResponse>(this, METHODID_PROCESS_UNARY, compression))).addMethod(com.benchmark.proto.v1.BenchmarkServiceGrpc.getProcessStreamingMethod(), asyncBidiStreamingCall(new MethodHandlers<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest, com.benchmark.proto.v1.BenchmarkProto.StreamingResponse>(this, METHODID_PROCESS_STREAMING, compression))).addMethod(com.benchmark.proto.v1.BenchmarkServiceGrpc.getHealthCheckMethod(), asyncUnaryCall(new MethodHandlers<com.benchmark.proto.v1.BenchmarkProto.HealthRequest, com.benchmark.proto.v1.BenchmarkProto.HealthResponse>(this, METHODID_HEALTH_CHECK, compression))).build();
        }
    }

    private static final int METHODID_PROCESS_UNARY = 0;

    private static final int METHODID_PROCESS_STREAMING = 1;

    private static final int METHODID_HEALTH_CHECK = 2;

    private static final class MethodHandlers<Req, Resp> implements io.grpc.stub.ServerCalls.UnaryMethod<Req, Resp>, io.grpc.stub.ServerCalls.ServerStreamingMethod<Req, Resp>, io.grpc.stub.ServerCalls.ClientStreamingMethod<Req, Resp>, io.grpc.stub.ServerCalls.BidiStreamingMethod<Req, Resp> {

        private final BenchmarkServiceImplBase serviceImpl;

        private final int methodId;

        private final String compression;

        MethodHandlers(BenchmarkServiceImplBase serviceImpl, int methodId, String compression) {
            this.serviceImpl = serviceImpl;
            this.methodId = methodId;
            this.compression = compression;
        }

        @java.lang.Override
        @java.lang.SuppressWarnings("unchecked")
        public void invoke(Req request, io.grpc.stub.StreamObserver<Resp> responseObserver) {
            switch(methodId) {
                case METHODID_PROCESS_UNARY:
                    io.quarkus.grpc.stubs.ServerCalls.oneToOne((com.benchmark.proto.v1.BenchmarkProto.UnaryRequest) request, (io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse>) responseObserver, compression, serviceImpl::processUnary);
                    break;
                case METHODID_HEALTH_CHECK:
                    io.quarkus.grpc.stubs.ServerCalls.oneToOne((com.benchmark.proto.v1.BenchmarkProto.HealthRequest) request, (io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.HealthResponse>) responseObserver, compression, serviceImpl::healthCheck);
                    break;
                default:
                    throw new java.lang.AssertionError();
            }
        }

        @java.lang.Override
        @java.lang.SuppressWarnings("unchecked")
        public io.grpc.stub.StreamObserver<Req> invoke(io.grpc.stub.StreamObserver<Resp> responseObserver) {
            switch(methodId) {
                case METHODID_PROCESS_STREAMING:
                    return (io.grpc.stub.StreamObserver<Req>) io.quarkus.grpc.stubs.ServerCalls.manyToMany((io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse>) responseObserver, serviceImpl::processStreaming);
                default:
                    throw new java.lang.AssertionError();
            }
        }
    }
}
