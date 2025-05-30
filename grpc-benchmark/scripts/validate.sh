#!/bin/bash

# Simple validation script for gRPC benchmark system
set -e

echo "=== gRPC Benchmark System Validation ==="
echo ""

# Check if binaries exist
echo "1. Checking binaries..."
if [ -f "./bin/server" ] && [ -f "./bin/client" ]; then
    echo "✓ Server and client binaries found"
else
    echo "✗ Binaries not found. Run 'make build' first."
    exit 1
fi

# Check database connection
echo ""
echo "2. Checking database connection..."
if psql -h localhost -p 5432 -U postgres -d proddb -c "\q" >/dev/null 2>&1; then
    echo "✓ Database connection successful"
else
    echo "✗ Database connection failed"
    echo "  Make sure PostgreSQL is running and 'proddb' database exists"
    exit 1
fi

# Check if schema exists
echo ""
echo "3. Checking database schema..."
SCHEMA_EXISTS=$(psql -h localhost -p 5432 -U postgres -d proddb -t -c "SELECT EXISTS(SELECT 1 FROM information_schema.schemata WHERE schema_name = 'benchmark');" 2>/dev/null | tr -d ' ')
if [ "$SCHEMA_EXISTS" = "t" ]; then
    echo "✓ Benchmark schema exists"
else
    echo "! Benchmark schema not found. Initializing..."
    psql -h localhost -p 5432 -U postgres -d proddb -f internal/db/schema.sql
    echo "✓ Schema initialized"
fi

# Check if server is running
echo ""
echo "4. Checking gRPC server..."
if curl -f http://localhost:8081/health >/dev/null 2>&1; then
    echo "✓ gRPC server is running"
    
    # Run a quick test
    echo ""
    echo "5. Running quick benchmark test..."
    ./bin/client -server=localhost:8080 -test=echo -duration=10s -concurrency=2
    
    # Check if data was recorded
    echo ""
    echo "6. Checking database records..."
    RECORD_COUNT=$(psql -h localhost -p 5432 -U postgres -d proddb -t -c "SELECT COUNT(*) FROM benchmark.benchmark_records;" 2>/dev/null | tr -d ' ')
    echo "✓ Found $RECORD_COUNT records in database"
    
else
    echo "✗ gRPC server is not running"
    echo "  Start the server with: ./bin/server"
    echo "  Or: go run ./cmd/server"
fi

echo ""
echo "=== Validation Complete ==="
echo ""
echo "To run full load tests:"
echo "  ./scripts/load-test.sh"
echo ""
echo "To view monitoring:"
echo "  Prometheus: http://localhost:9090"
echo "  Grafana: http://localhost:3000"
