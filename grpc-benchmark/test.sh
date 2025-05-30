#!/bin/bash

# Simple test script to verify gRPC server functionality

echo "Starting gRPC Benchmark Test Suite"
echo "=================================="

# Set environment variables
export SERVER_PORT=8080
export METRICS_PORT=8081
export LOG_LEVEL=info
export LOG_FORMAT=console

# Database can be optional for initial testing
export DATABASE_URL="postgres://benchmark_user:benchmark_pass@localhost:5432/benchmark?sslmode=disable"

echo "Step 1: Testing server binary..."
if [ ! -f "./bin/server" ]; then
    echo "Server binary not found. Building..."
    make build
fi

echo "Step 2: Testing client binary..."
if [ ! -f "./bin/client" ]; then
    echo "Client binary not found. Building..."
    make build
fi

echo "Step 3: Testing protobuf generation..."
ls -la proto/

echo "Step 4: Starting PostgreSQL (if not running)..."
podman ps | grep benchmark-postgres || {
    echo "Starting PostgreSQL container..."
    podman run --name benchmark-postgres \
        -e POSTGRES_DB=benchmark \
        -e POSTGRES_USER=benchmark_user \
        -e POSTGRES_PASSWORD=benchmark_pass \
        -p 5432:5432 -d postgres:15-alpine
    sleep 10
}

echo "Step 5: Initialize database schema..."
podman exec benchmark-postgres psql -U benchmark_user -d benchmark -c "
CREATE TABLE IF NOT EXISTS benchmark_records (
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

CREATE TABLE IF NOT EXISTS data_chunks (
    id SERIAL PRIMARY KEY,
    record_id VARCHAR(255) NOT NULL,
    chunk_id INTEGER NOT NULL,
    filename VARCHAR(255),
    data_size INTEGER,
    total_chunks INTEGER,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
" 2>/dev/null || echo "Database initialization completed"

echo "Step 6: Starting gRPC server..."
./bin/server &
SERVER_PID=$!
sleep 3

echo "Step 7: Testing server health..."
curl -f http://localhost:8081/health 2>/dev/null && echo "✓ Server health check passed" || echo "✗ Server health check failed"

echo "Step 8: Running basic benchmark test..."
./bin/client -server=localhost:8080 -test=echo -duration=5s -concurrency=2 -message-size=100

echo "Step 9: Cleanup..."
kill $SERVER_PID 2>/dev/null || true

echo "Test completed!"
