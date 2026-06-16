package com.benchmark.entity;

import io.quarkus.hibernate.reactive.panache.PanacheEntity;
import io.vertx.core.json.JsonObject;
import jakarta.persistence.*;
import java.time.Instant;
import java.util.Map;

@Entity
@Table(name = "benchmark_records")
public class BenchmarkRecord extends PanacheEntity {

    @Column(name = "benchmark_id", nullable = false)
    public String benchmarkId;

    @Column(nullable = false)
    public Instant timestamp;

    @Column(name = "operation_type", nullable = false, length = 20)
    public String operationType;

    @Column(name = "server_type", nullable = false, length = 20)
    public String serverType;

    @Column(name = "client_id", nullable = false)
    public String clientId;

    @Column(name = "sequence_number", nullable = false)
    public Long sequenceNumber;

    @Column(name = "correlation_id", nullable = false)
    public String correlationId;

    @Column(name = "payload_size", nullable = false)
    public Integer payloadSize;

    @Column(name = "processing_time_ms", nullable = false)
    public Long processingTimeMs;

    @Column(columnDefinition = "jsonb")
    public JsonObject headers;

    @Column(name = "created_at", nullable = false)
    public Instant createdAt;

    // Default constructor
    public BenchmarkRecord() {
        this.createdAt = Instant.now();
    }

    // Constructor with parameters
    public BenchmarkRecord(String benchmarkId, Instant timestamp, String operationType, 
                          String serverType, String clientId, Long sequenceNumber, 
                          String correlationId, Integer payloadSize, Long processingTimeMs, 
                          Map<String, String> headerMap) {
        this();
        this.benchmarkId = benchmarkId;
        this.timestamp = timestamp;
        this.operationType = operationType;
        this.serverType = serverType;
        this.clientId = clientId;
        this.sequenceNumber = sequenceNumber;
        this.correlationId = correlationId;
        this.payloadSize = payloadSize;
        this.processingTimeMs = processingTimeMs;
        this.headers = headerMap != null ? new JsonObject().put("data", headerMap) : new JsonObject();
    }

    // Static methods for queries
    public static io.smallrye.mutiny.Uni<Long> countByServerTypeAndOperationType(String serverType, String operationType) {
        return count("serverType = ?1 and operationType = ?2", serverType, operationType);
    }

    public static io.smallrye.mutiny.Uni<java.util.List<BenchmarkRecord>> findByServerTypeAndOperationType(
            String serverType, String operationType, Instant since) {
        return list("serverType = ?1 and operationType = ?2 and timestamp >= ?3", 
                   serverType, operationType, since);
    }

    public static io.smallrye.mutiny.Uni<BenchmarkStats> getStatsByServerTypeAndOperationType(
            String serverType, String operationType, Instant since) {
        // For simplicity, let's implement this with basic Panache methods
        // In a real scenario, you'd use native queries or repository patterns
        return find("serverType = ?1 and operationType = ?2 and timestamp >= ?3", 
                   serverType, operationType, since)
            .list()
            .map(records -> {
                if (records.isEmpty()) {
                    return new BenchmarkStats(0L, 0.0, 0L, 0L);
                }
                
                long count = records.size();
                double avgProcessingTime = records.stream()
                    .mapToLong(r -> ((BenchmarkRecord) r).processingTimeMs)
                    .average()
                    .orElse(0.0);
                long minProcessingTime = records.stream()
                    .mapToLong(r -> ((BenchmarkRecord) r).processingTimeMs)
                    .min()
                    .orElse(0L);
                long maxProcessingTime = records.stream()
                    .mapToLong(r -> ((BenchmarkRecord) r).processingTimeMs)
                    .max()
                    .orElse(0L);
                    
                return new BenchmarkStats(count, avgProcessingTime, minProcessingTime, maxProcessingTime);
            });
    }

    public static class BenchmarkStats {
        public final long totalRequests;
        public final double avgProcessingTime;
        public final long minProcessingTime;
        public final long maxProcessingTime;

        public BenchmarkStats(long totalRequests, double avgProcessingTime, 
                            long minProcessingTime, long maxProcessingTime) {
            this.totalRequests = totalRequests;
            this.avgProcessingTime = avgProcessingTime;
            this.minProcessingTime = minProcessingTime;
            this.maxProcessingTime = maxProcessingTime;
        }
    }
}
