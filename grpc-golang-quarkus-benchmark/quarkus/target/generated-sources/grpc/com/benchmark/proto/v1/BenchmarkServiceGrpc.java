package com.benchmark.proto.v1;

import static io.grpc.MethodDescriptor.generateFullMethodName;

/**
 * <pre>
 * Service definitions
 * </pre>
 */
@io.quarkus.Generated(value = "by gRPC proto compiler (version 1.57.2)", comments = "Source: benchmark.proto")
@io.grpc.stub.annotations.GrpcGenerated
public final class BenchmarkServiceGrpc {

    private BenchmarkServiceGrpc() {
    }

    public static final java.lang.String SERVICE_NAME = "benchmark.v1.BenchmarkService";

    // Static method descriptors that strictly reflect the proto.
    private static volatile io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.UnaryRequest, com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> getProcessUnaryMethod;

    @io.grpc.stub.annotations.RpcMethod(fullMethodName = SERVICE_NAME + '/' + "ProcessUnary", requestType = com.benchmark.proto.v1.BenchmarkProto.UnaryRequest.class, responseType = com.benchmark.proto.v1.BenchmarkProto.UnaryResponse.class, methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
    public static io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.UnaryRequest, com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> getProcessUnaryMethod() {
        io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.UnaryRequest, com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> getProcessUnaryMethod;
        if ((getProcessUnaryMethod = BenchmarkServiceGrpc.getProcessUnaryMethod) == null) {
            synchronized (BenchmarkServiceGrpc.class) {
                if ((getProcessUnaryMethod = BenchmarkServiceGrpc.getProcessUnaryMethod) == null) {
                    BenchmarkServiceGrpc.getProcessUnaryMethod = getProcessUnaryMethod = io.grpc.MethodDescriptor.<com.benchmark.proto.v1.BenchmarkProto.UnaryRequest, com.benchmark.proto.v1.BenchmarkProto.UnaryResponse>newBuilder().setType(io.grpc.MethodDescriptor.MethodType.UNARY).setFullMethodName(generateFullMethodName(SERVICE_NAME, "ProcessUnary")).setSampledToLocalTracing(true).setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest.getDefaultInstance())).setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(com.benchmark.proto.v1.BenchmarkProto.UnaryResponse.getDefaultInstance())).setSchemaDescriptor(new BenchmarkServiceMethodDescriptorSupplier("ProcessUnary")).build();
                }
            }
        }
        return getProcessUnaryMethod;
    }

    private static volatile io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest, com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> getProcessStreamingMethod;

    @io.grpc.stub.annotations.RpcMethod(fullMethodName = SERVICE_NAME + '/' + "ProcessStreaming", requestType = com.benchmark.proto.v1.BenchmarkProto.StreamingRequest.class, responseType = com.benchmark.proto.v1.BenchmarkProto.StreamingResponse.class, methodType = io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING)
    public static io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest, com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> getProcessStreamingMethod() {
        io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest, com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> getProcessStreamingMethod;
        if ((getProcessStreamingMethod = BenchmarkServiceGrpc.getProcessStreamingMethod) == null) {
            synchronized (BenchmarkServiceGrpc.class) {
                if ((getProcessStreamingMethod = BenchmarkServiceGrpc.getProcessStreamingMethod) == null) {
                    BenchmarkServiceGrpc.getProcessStreamingMethod = getProcessStreamingMethod = io.grpc.MethodDescriptor.<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest, com.benchmark.proto.v1.BenchmarkProto.StreamingResponse>newBuilder().setType(io.grpc.MethodDescriptor.MethodType.BIDI_STREAMING).setFullMethodName(generateFullMethodName(SERVICE_NAME, "ProcessStreaming")).setSampledToLocalTracing(true).setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(com.benchmark.proto.v1.BenchmarkProto.StreamingRequest.getDefaultInstance())).setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(com.benchmark.proto.v1.BenchmarkProto.StreamingResponse.getDefaultInstance())).setSchemaDescriptor(new BenchmarkServiceMethodDescriptorSupplier("ProcessStreaming")).build();
                }
            }
        }
        return getProcessStreamingMethod;
    }

    private static volatile io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.HealthRequest, com.benchmark.proto.v1.BenchmarkProto.HealthResponse> getHealthCheckMethod;

    @io.grpc.stub.annotations.RpcMethod(fullMethodName = SERVICE_NAME + '/' + "HealthCheck", requestType = com.benchmark.proto.v1.BenchmarkProto.HealthRequest.class, responseType = com.benchmark.proto.v1.BenchmarkProto.HealthResponse.class, methodType = io.grpc.MethodDescriptor.MethodType.UNARY)
    public static io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.HealthRequest, com.benchmark.proto.v1.BenchmarkProto.HealthResponse> getHealthCheckMethod() {
        io.grpc.MethodDescriptor<com.benchmark.proto.v1.BenchmarkProto.HealthRequest, com.benchmark.proto.v1.BenchmarkProto.HealthResponse> getHealthCheckMethod;
        if ((getHealthCheckMethod = BenchmarkServiceGrpc.getHealthCheckMethod) == null) {
            synchronized (BenchmarkServiceGrpc.class) {
                if ((getHealthCheckMethod = BenchmarkServiceGrpc.getHealthCheckMethod) == null) {
                    BenchmarkServiceGrpc.getHealthCheckMethod = getHealthCheckMethod = io.grpc.MethodDescriptor.<com.benchmark.proto.v1.BenchmarkProto.HealthRequest, com.benchmark.proto.v1.BenchmarkProto.HealthResponse>newBuilder().setType(io.grpc.MethodDescriptor.MethodType.UNARY).setFullMethodName(generateFullMethodName(SERVICE_NAME, "HealthCheck")).setSampledToLocalTracing(true).setRequestMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(com.benchmark.proto.v1.BenchmarkProto.HealthRequest.getDefaultInstance())).setResponseMarshaller(io.grpc.protobuf.ProtoUtils.marshaller(com.benchmark.proto.v1.BenchmarkProto.HealthResponse.getDefaultInstance())).setSchemaDescriptor(new BenchmarkServiceMethodDescriptorSupplier("HealthCheck")).build();
                }
            }
        }
        return getHealthCheckMethod;
    }

    /**
     * Creates a new async stub that supports all call types for the service
     */
    public static BenchmarkServiceStub newStub(io.grpc.Channel channel) {
        io.grpc.stub.AbstractStub.StubFactory<BenchmarkServiceStub> factory = new io.grpc.stub.AbstractStub.StubFactory<BenchmarkServiceStub>() {

            @java.lang.Override
            public BenchmarkServiceStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
                return new BenchmarkServiceStub(channel, callOptions);
            }
        };
        return BenchmarkServiceStub.newStub(factory, channel);
    }

    /**
     * Creates a new blocking-style stub that supports unary and streaming output calls on the service
     */
    public static BenchmarkServiceBlockingStub newBlockingStub(io.grpc.Channel channel) {
        io.grpc.stub.AbstractStub.StubFactory<BenchmarkServiceBlockingStub> factory = new io.grpc.stub.AbstractStub.StubFactory<BenchmarkServiceBlockingStub>() {

            @java.lang.Override
            public BenchmarkServiceBlockingStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
                return new BenchmarkServiceBlockingStub(channel, callOptions);
            }
        };
        return BenchmarkServiceBlockingStub.newStub(factory, channel);
    }

    /**
     * Creates a new ListenableFuture-style stub that supports unary calls on the service
     */
    public static BenchmarkServiceFutureStub newFutureStub(io.grpc.Channel channel) {
        io.grpc.stub.AbstractStub.StubFactory<BenchmarkServiceFutureStub> factory = new io.grpc.stub.AbstractStub.StubFactory<BenchmarkServiceFutureStub>() {

            @java.lang.Override
            public BenchmarkServiceFutureStub newStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
                return new BenchmarkServiceFutureStub(channel, callOptions);
            }
        };
        return BenchmarkServiceFutureStub.newStub(factory, channel);
    }

    /**
     * <pre>
     * Service definitions
     * </pre>
     */
    public interface AsyncService {

        /**
         * <pre>
         * Unary RPC
         * </pre>
         */
        default void processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request, io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> responseObserver) {
            io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getProcessUnaryMethod(), responseObserver);
        }

        /**
         * <pre>
         * Bidirectional streaming RPC
         * </pre>
         */
        default io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> processStreaming(io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> responseObserver) {
            return io.grpc.stub.ServerCalls.asyncUnimplementedStreamingCall(getProcessStreamingMethod(), responseObserver);
        }

        /**
         * <pre>
         * Health check
         * </pre>
         */
        default void healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request, io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> responseObserver) {
            io.grpc.stub.ServerCalls.asyncUnimplementedUnaryCall(getHealthCheckMethod(), responseObserver);
        }
    }

    /**
     * Base class for the server implementation of the service BenchmarkService.
     * <pre>
     * Service definitions
     * </pre>
     */
    public static abstract class BenchmarkServiceImplBase implements io.grpc.BindableService, AsyncService {

        @java.lang.Override
        public io.grpc.ServerServiceDefinition bindService() {
            return BenchmarkServiceGrpc.bindService(this);
        }
    }

    /**
     * A stub to allow clients to do asynchronous rpc calls to service BenchmarkService.
     * <pre>
     * Service definitions
     * </pre>
     */
    public static class BenchmarkServiceStub extends io.grpc.stub.AbstractAsyncStub<BenchmarkServiceStub> {

        private BenchmarkServiceStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            super(channel, callOptions);
        }

        @java.lang.Override
        protected BenchmarkServiceStub build(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            return new BenchmarkServiceStub(channel, callOptions);
        }

        /**
         * <pre>
         * Unary RPC
         * </pre>
         */
        public void processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request, io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> responseObserver) {
            io.grpc.stub.ClientCalls.asyncUnaryCall(getChannel().newCall(getProcessUnaryMethod(), getCallOptions()), request, responseObserver);
        }

        /**
         * <pre>
         * Bidirectional streaming RPC
         * </pre>
         */
        public io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest> processStreaming(io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse> responseObserver) {
            return io.grpc.stub.ClientCalls.asyncBidiStreamingCall(getChannel().newCall(getProcessStreamingMethod(), getCallOptions()), responseObserver);
        }

        /**
         * <pre>
         * Health check
         * </pre>
         */
        public void healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request, io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> responseObserver) {
            io.grpc.stub.ClientCalls.asyncUnaryCall(getChannel().newCall(getHealthCheckMethod(), getCallOptions()), request, responseObserver);
        }
    }

    /**
     * A stub to allow clients to do synchronous rpc calls to service BenchmarkService.
     * <pre>
     * Service definitions
     * </pre>
     */
    public static class BenchmarkServiceBlockingStub extends io.grpc.stub.AbstractBlockingStub<BenchmarkServiceBlockingStub> {

        private BenchmarkServiceBlockingStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            super(channel, callOptions);
        }

        @java.lang.Override
        protected BenchmarkServiceBlockingStub build(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            return new BenchmarkServiceBlockingStub(channel, callOptions);
        }

        /**
         * <pre>
         * Unary RPC
         * </pre>
         */
        public com.benchmark.proto.v1.BenchmarkProto.UnaryResponse processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request) {
            return io.grpc.stub.ClientCalls.blockingUnaryCall(getChannel(), getProcessUnaryMethod(), getCallOptions(), request);
        }

        /**
         * <pre>
         * Health check
         * </pre>
         */
        public com.benchmark.proto.v1.BenchmarkProto.HealthResponse healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request) {
            return io.grpc.stub.ClientCalls.blockingUnaryCall(getChannel(), getHealthCheckMethod(), getCallOptions(), request);
        }
    }

    /**
     * A stub to allow clients to do ListenableFuture-style rpc calls to service BenchmarkService.
     * <pre>
     * Service definitions
     * </pre>
     */
    public static class BenchmarkServiceFutureStub extends io.grpc.stub.AbstractFutureStub<BenchmarkServiceFutureStub> {

        private BenchmarkServiceFutureStub(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            super(channel, callOptions);
        }

        @java.lang.Override
        protected BenchmarkServiceFutureStub build(io.grpc.Channel channel, io.grpc.CallOptions callOptions) {
            return new BenchmarkServiceFutureStub(channel, callOptions);
        }

        /**
         * <pre>
         * Unary RPC
         * </pre>
         */
        public com.google.common.util.concurrent.ListenableFuture<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse> processUnary(com.benchmark.proto.v1.BenchmarkProto.UnaryRequest request) {
            return io.grpc.stub.ClientCalls.futureUnaryCall(getChannel().newCall(getProcessUnaryMethod(), getCallOptions()), request);
        }

        /**
         * <pre>
         * Health check
         * </pre>
         */
        public com.google.common.util.concurrent.ListenableFuture<com.benchmark.proto.v1.BenchmarkProto.HealthResponse> healthCheck(com.benchmark.proto.v1.BenchmarkProto.HealthRequest request) {
            return io.grpc.stub.ClientCalls.futureUnaryCall(getChannel().newCall(getHealthCheckMethod(), getCallOptions()), request);
        }
    }

    private static final int METHODID_PROCESS_UNARY = 0;

    private static final int METHODID_HEALTH_CHECK = 1;

    private static final int METHODID_PROCESS_STREAMING = 2;

    private static final class MethodHandlers<Req, Resp> implements io.grpc.stub.ServerCalls.UnaryMethod<Req, Resp>, io.grpc.stub.ServerCalls.ServerStreamingMethod<Req, Resp>, io.grpc.stub.ServerCalls.ClientStreamingMethod<Req, Resp>, io.grpc.stub.ServerCalls.BidiStreamingMethod<Req, Resp> {

        private final AsyncService serviceImpl;

        private final int methodId;

        MethodHandlers(AsyncService serviceImpl, int methodId) {
            this.serviceImpl = serviceImpl;
            this.methodId = methodId;
        }

        @java.lang.Override
        @java.lang.SuppressWarnings("unchecked")
        public void invoke(Req request, io.grpc.stub.StreamObserver<Resp> responseObserver) {
            switch(methodId) {
                case METHODID_PROCESS_UNARY:
                    serviceImpl.processUnary((com.benchmark.proto.v1.BenchmarkProto.UnaryRequest) request, (io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.UnaryResponse>) responseObserver);
                    break;
                case METHODID_HEALTH_CHECK:
                    serviceImpl.healthCheck((com.benchmark.proto.v1.BenchmarkProto.HealthRequest) request, (io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.HealthResponse>) responseObserver);
                    break;
                default:
                    throw new AssertionError();
            }
        }

        @java.lang.Override
        @java.lang.SuppressWarnings("unchecked")
        public io.grpc.stub.StreamObserver<Req> invoke(io.grpc.stub.StreamObserver<Resp> responseObserver) {
            switch(methodId) {
                case METHODID_PROCESS_STREAMING:
                    return (io.grpc.stub.StreamObserver<Req>) serviceImpl.processStreaming((io.grpc.stub.StreamObserver<com.benchmark.proto.v1.BenchmarkProto.StreamingResponse>) responseObserver);
                default:
                    throw new AssertionError();
            }
        }
    }

    public static io.grpc.ServerServiceDefinition bindService(AsyncService service) {
        return io.grpc.ServerServiceDefinition.builder(getServiceDescriptor()).addMethod(getProcessUnaryMethod(), io.grpc.stub.ServerCalls.asyncUnaryCall(new MethodHandlers<com.benchmark.proto.v1.BenchmarkProto.UnaryRequest, com.benchmark.proto.v1.BenchmarkProto.UnaryResponse>(service, METHODID_PROCESS_UNARY))).addMethod(getProcessStreamingMethod(), io.grpc.stub.ServerCalls.asyncBidiStreamingCall(new MethodHandlers<com.benchmark.proto.v1.BenchmarkProto.StreamingRequest, com.benchmark.proto.v1.BenchmarkProto.StreamingResponse>(service, METHODID_PROCESS_STREAMING))).addMethod(getHealthCheckMethod(), io.grpc.stub.ServerCalls.asyncUnaryCall(new MethodHandlers<com.benchmark.proto.v1.BenchmarkProto.HealthRequest, com.benchmark.proto.v1.BenchmarkProto.HealthResponse>(service, METHODID_HEALTH_CHECK))).build();
    }

    private static abstract class BenchmarkServiceBaseDescriptorSupplier implements io.grpc.protobuf.ProtoFileDescriptorSupplier, io.grpc.protobuf.ProtoServiceDescriptorSupplier {

        BenchmarkServiceBaseDescriptorSupplier() {
        }

        @java.lang.Override
        public com.google.protobuf.Descriptors.FileDescriptor getFileDescriptor() {
            return com.benchmark.proto.v1.BenchmarkProto.getDescriptor();
        }

        @java.lang.Override
        public com.google.protobuf.Descriptors.ServiceDescriptor getServiceDescriptor() {
            return getFileDescriptor().findServiceByName("BenchmarkService");
        }
    }

    private static final class BenchmarkServiceFileDescriptorSupplier extends BenchmarkServiceBaseDescriptorSupplier {

        BenchmarkServiceFileDescriptorSupplier() {
        }
    }

    private static final class BenchmarkServiceMethodDescriptorSupplier extends BenchmarkServiceBaseDescriptorSupplier implements io.grpc.protobuf.ProtoMethodDescriptorSupplier {

        private final java.lang.String methodName;

        BenchmarkServiceMethodDescriptorSupplier(java.lang.String methodName) {
            this.methodName = methodName;
        }

        @java.lang.Override
        public com.google.protobuf.Descriptors.MethodDescriptor getMethodDescriptor() {
            return getServiceDescriptor().findMethodByName(methodName);
        }
    }

    private static volatile io.grpc.ServiceDescriptor serviceDescriptor;

    public static io.grpc.ServiceDescriptor getServiceDescriptor() {
        io.grpc.ServiceDescriptor result = serviceDescriptor;
        if (result == null) {
            synchronized (BenchmarkServiceGrpc.class) {
                result = serviceDescriptor;
                if (result == null) {
                    serviceDescriptor = result = io.grpc.ServiceDescriptor.newBuilder(SERVICE_NAME).setSchemaDescriptor(new BenchmarkServiceFileDescriptorSupplier()).addMethod(getProcessUnaryMethod()).addMethod(getProcessStreamingMethod()).addMethod(getHealthCheckMethod()).build();
                }
            }
        }
        return result;
    }
}
