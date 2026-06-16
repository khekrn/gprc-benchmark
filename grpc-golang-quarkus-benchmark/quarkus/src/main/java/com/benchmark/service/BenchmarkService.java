package com.benchmark.service;

import com.benchmark.entity.BenchmarkRecord;
import com.benchmark.proto.v1.*;
import com.benchmark.proto.v1.BenchmarkProto.UnaryRequest;
import com.benchmark.proto.v1.BenchmarkProto.UnaryResponse;
import com.benchmark.proto.v1.BenchmarkProto.StreamingRequest;
import com.benchmark.proto.v1.BenchmarkProto.StreamingResponse;
import com.benchmark.proto.v1.BenchmarkProto.HealthRequest;
import com.benchmark.proto.v1.BenchmarkProto.HealthResponse;
import com.benchmark.proto.v1.BenchmarkProto.BenchmarkMessage;
import com.google.protobuf.Timestamp;
import io.micrometer.core.instrument.Counter;
import io.micrometer.core.instrument.MeterRegistry;
import io.micrometer.core.instrument.Tag;
import io.micrometer.core.instrument.Timer;
import io.quarkus.grpc.GrpcService;
import io.quarkus.hibernate.reactive.panache.Panache;
import io.smallrye.mutiny.Multi;
import io.smallrye.mutiny.Uni;
import jakarta.inject.Inject;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;

import java.security.SecureRandom;
import java.time.Instant;
import java.util.Map;
import java.util.UUID;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.atomic.AtomicLong;

@GrpcService
public class BenchmarkService implements com.benchmark.proto.v1.BenchmarkService {

    private static final Logger LOG = LoggerFactory.getLogger(BenchmarkService.class);
    private static final String SERVER_TYPE = "quarkus";
    private static final SecureRandom random = new SecureRandom();

    @Inject
    MeterRegistry meterRegistry;

    private final Map<String, AtomicLong> activeConnections = new ConcurrentHashMap<>();
    
    // Metrics
    private Timer unaryRequestTimer;
    private Timer streamingRequestTimer;
    private Timer databaseTimer;
    private Counter unaryRequestCounter;
    private Counter streamingRequestCounter;
    private Counter databaseOperationCounter;

    public void init() {
        // Initialize metrics
        unaryRequestTimer = Timer.builder("grpc.request.duration")
                .tag("method", "unary")
                .tag("server_type", SERVER_TYPE)
                .register(meterRegistry);

        streamingRequestTimer = Timer.builder("grpc.request.duration")
                .tag("method", "streaming")
                .tag("server_type", SERVER_TYPE)
                .register(meterRegistry);

        databaseTimer = Timer.builder("database.operation.duration")
                .tag("operation", "save")
                .tag("server_type", SERVER_TYPE)
                .register(meterRegistry);

        unaryRequestCounter = Counter.builder("grpc.requests.total")
                .tag("method", "unary")
                .tag("server_type", SERVER_TYPE)
                .register(meterRegistry);

        streamingRequestCounter = Counter.builder("grpc.requests.total")
                .tag("method", "streaming")
                .tag("server_type", SERVER_TYPE)
                .register(meterRegistry);

        databaseOperationCounter = Counter.builder("database.operations.total")
                .tag("operation", "save")
                .tag("server_type", SERVER_TYPE)
                .register(meterRegistry);
    }

    @Override
    public Uni<UnaryResponse> processUnary(UnaryRequest request) {
        Timer.Sample sample = Timer.start(meterRegistry);
        
        return Uni.createFrom().item(() -> {
            LOG.debug("Processing unary request: {}", request.getMessage().getId());
            
            // Record payload size
            int payloadSize = request.getMessage().getPayload().size();
            meterRegistry.gauge("message.payload.size", 
                              java.util.List.of(
                                  Tag.of("operation_type", "unary"), 
                                  Tag.of("server_type", SERVER_TYPE)
                              ), 
                              payloadSize);

            // Simulate processing time proportional to payload size
            try {
                Thread.sleep(payloadSize / 1024); // 1ms per KB
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
            }

            return request;
        })
        .chain(req -> {
            long processingTimeMs = System.currentTimeMillis() - 
                                  toInstant(req.getMessage().getTimestamp()).toEpochMilli();

            UnaryResponse.Builder responseBuilder = UnaryResponse.newBuilder()
                    .setId(UUID.randomUUID().toString())
                    .setStatus("processed")
                    .setProcessedAt(Timestamp.newBuilder()
                            .setSeconds(Instant.now().getEpochSecond())
                            .setNanos((int) (Instant.now().getNano()))
                            .build())
                    .setProcessingTimeMs(processingTimeMs)
                    .setServerType(SERVER_TYPE)
                    .setSavedToDb(false);

            if (req.getSaveToDb()) {
                return saveToDatabase(req, processingTimeMs)
                        .map(saved -> responseBuilder.setSavedToDb(saved).build());
            } else {
                return Uni.createFrom().item(responseBuilder.build());
            }
        })
        .invoke(() -> {
            sample.stop(unaryRequestTimer);
            unaryRequestCounter.increment();
        })
        .onFailure().invoke(throwable -> {
            LOG.error("Error processing unary request", throwable);
            sample.stop(unaryRequestTimer);
        });
    }

    @Override
    public Multi<StreamingResponse> processStreaming(Multi<StreamingRequest> request) {
        String connectionId = UUID.randomUUID().toString();
        activeConnections.put(connectionId, new AtomicLong(0));
        
        LOG.info("Started streaming connection: {}", connectionId);

        return request
                .onItem().transform(req -> {
                    Timer.Sample sample = Timer.start(meterRegistry);
                    
                    try {
                        LOG.debug("Processing streaming request: {}", req.getMessage().getId());
                        
                        // Record payload size
                        int payloadSize = req.getMessage().getPayload().size();
                        meterRegistry.gauge("message.payload.size", 
                                          java.util.List.of(
                                              Tag.of("operation_type", "streaming"), 
                                              Tag.of("server_type", SERVER_TYPE)
                                          ), 
                                          payloadSize);

                        // Simulate processing
                        Thread.sleep(payloadSize / 2048); // 1ms per 2KB

                        long processingTimeMs = System.currentTimeMillis() - 
                                              toInstant(req.getMessage().getTimestamp()).toEpochMilli();

                        StreamingResponse.Builder responseBuilder = StreamingResponse.newBuilder()
                                .setId(UUID.randomUUID().toString())
                                .setStatus("processed")
                                .setProcessedAt(Timestamp.newBuilder()
                                        .setSeconds(Instant.now().getEpochSecond())
                                        .setNanos((int) (Instant.now().getNano()))
                                        .build())
                                .setProcessingTimeMs(processingTimeMs)
                                .setServerType(SERVER_TYPE)
                                .setBatchCount(req.getBatchSize())
                                .setSavedToDb(false);

                        // Increment connection counter
                        activeConnections.get(connectionId).incrementAndGet();

                        sample.stop(streamingRequestTimer);
                        streamingRequestCounter.increment();

                        return responseBuilder.build();
                        
                    } catch (InterruptedException e) {
                        Thread.currentThread().interrupt();
                        sample.stop(streamingRequestTimer);
                        throw new RuntimeException("Processing interrupted", e);
                    } catch (Exception e) {
                        sample.stop(streamingRequestTimer);
                        throw new RuntimeException("Processing failed", e);
                    }
                })
                .onTermination().invoke(() -> {
                    activeConnections.remove(connectionId);
                    LOG.info("Closed streaming connection: {}", connectionId);
                })
                .onFailure().invoke(throwable -> {
                    LOG.error("Error in streaming connection: " + connectionId, throwable);
                    activeConnections.remove(connectionId);
                });
    }

    @Override
    public Uni<HealthResponse> healthCheck(HealthRequest request) {
        return Uni.createFrom().item(
                HealthResponse.newBuilder()
                        .setStatus("healthy")
                        .setTimestamp(Timestamp.newBuilder()
                                .setSeconds(Instant.now().getEpochSecond())
                                .setNanos((int) (Instant.now().getNano()))
                                .build())
                        .setServerType(SERVER_TYPE)
                        .build()
        );
    }

    private Uni<Boolean> saveToDatabase(UnaryRequest request, long processingTimeMs) {
        Timer.Sample sample = Timer.start(meterRegistry);
        
        BenchmarkMessage message = request.getMessage();
        BenchmarkRecord record = new BenchmarkRecord(
                message.getId(),
                toInstant(message.getTimestamp()),
                "unary",
                SERVER_TYPE,
                message.getMetadata().getClientId(),
                message.getMetadata().getSequenceNumber(),
                message.getMetadata().getCorrelationId(),
                message.getPayload().size(),
                processingTimeMs,
                message.getMetadata().getHeadersMap()
        );

        return Panache.withTransaction(() -> record.persist())
                .map(ignored -> {
                    sample.stop(databaseTimer);
                    databaseOperationCounter.increment();
                    return true;
                })
                .onFailure().recoverWithItem(throwable -> {
                    LOG.error("Failed to save to database", throwable);
                    sample.stop(databaseTimer);
                    return false;
                });
    }

    private Instant toInstant(Timestamp timestamp) {
        return Instant.ofEpochSecond(timestamp.getSeconds(), timestamp.getNanos());
    }

    public int getActiveConnectionsCount() {
        return activeConnections.size();
    }

    // Utility method to generate 4KB payload for testing
    public static byte[] generate4KBPayload() {
        byte[] payload = new byte[4096]; // 4KB
        random.nextBytes(payload);
        return payload;
    }

    // Initialize metrics on startup
    @jakarta.annotation.PostConstruct
    void initMetrics() {
        init();
    }
}
