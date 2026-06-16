-- Database initialization script for benchmark
-- This script creates the necessary tables and indexes

-- Create the benchmark_records table
CREATE TABLE IF NOT EXISTS benchmark_records (
    id BIGSERIAL PRIMARY KEY,
    benchmark_id VARCHAR(36) NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    operation_type VARCHAR(20) NOT NULL,
    server_type VARCHAR(20) NOT NULL,
    client_id VARCHAR(36) NOT NULL,
    sequence_number BIGINT NOT NULL,
    correlation_id VARCHAR(36) NOT NULL,
    payload_size INTEGER NOT NULL,
    processing_time_ms BIGINT NOT NULL,
    headers JSONB,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- Create indexes for better query performance
CREATE INDEX IF NOT EXISTS idx_benchmark_records_timestamp ON benchmark_records(timestamp);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_operation_type ON benchmark_records(operation_type);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_server_type ON benchmark_records(server_type);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_client_id ON benchmark_records(client_id);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_correlation_id ON benchmark_records(correlation_id);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_composite ON benchmark_records(server_type, operation_type, timestamp);

-- Create a view for benchmark statistics
CREATE OR REPLACE VIEW benchmark_stats AS
SELECT 
    server_type,
    operation_type,
    COUNT(*) as total_requests,
    AVG(processing_time_ms) as avg_processing_time_ms,
    MIN(processing_time_ms) as min_processing_time_ms,
    MAX(processing_time_ms) as max_processing_time_ms,
    PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY processing_time_ms) as p50_processing_time_ms,
    PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY processing_time_ms) as p95_processing_time_ms,
    PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY processing_time_ms) as p99_processing_time_ms,
    AVG(payload_size) as avg_payload_size,
    MIN(created_at) as first_request,
    MAX(created_at) as last_request
FROM benchmark_records
GROUP BY server_type, operation_type;

-- Insert some initial test data for verification
INSERT INTO benchmark_records (
    benchmark_id, timestamp, operation_type, server_type, client_id,
    sequence_number, correlation_id, payload_size, processing_time_ms, headers
) VALUES (
    'test-record-1',
    NOW(),
    'unary',
    'system',
    'init-client',
    1,
    'init-correlation-1',
    4096,
    10,
    '{"init": "true", "test": "data"}'::jsonb
) ON CONFLICT DO NOTHING;
