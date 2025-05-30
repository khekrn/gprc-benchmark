-- Database schema for benchmark service
-- Create schema if it doesn't exist
CREATE SCHEMA IF NOT EXISTS benchmark;

-- Set search path to use benchmark schema
SET search_path TO benchmark, public;

CREATE TABLE IF NOT EXISTS benchmark.benchmark_records (
    id SERIAL PRIMARY KEY,
    record_id VARCHAR(255) UNIQUE NOT NULL,
    request_type VARCHAR(50) NOT NULL,
    sequence_id INTEGER,
    user_id VARCHAR(255),
    session_id VARCHAR(255),
    payload TEXT,
    metadata JSONB,
    processing_time_ns BIGINT,
    queue_time_ns BIGINT,
    concurrent_requests INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);

-- Index for better query performance
CREATE INDEX IF NOT EXISTS idx_benchmark_records_user_id ON benchmark.benchmark_records(user_id);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_session_id ON benchmark.benchmark_records(session_id);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_created_at ON benchmark.benchmark_records(created_at);
CREATE INDEX IF NOT EXISTS idx_benchmark_records_request_type ON benchmark.benchmark_records(request_type);

-- Table for streaming data chunks
CREATE TABLE IF NOT EXISTS benchmark.data_chunks (
    id SERIAL PRIMARY KEY,
    record_id VARCHAR(255) NOT NULL,
    chunk_id INTEGER NOT NULL,
    filename VARCHAR(255),
    data_size INTEGER,
    total_chunks INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW(),
    FOREIGN KEY (record_id) REFERENCES benchmark.benchmark_records(record_id)
);

CREATE INDEX IF NOT EXISTS idx_data_chunks_record_id ON benchmark.data_chunks(record_id);
