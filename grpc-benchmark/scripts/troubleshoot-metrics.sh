#!/bin/bash

# Metrics troubleshooting script
set -e

echo "=== gRPC Benchmark Metrics Troubleshooting ==="
echo ""

# Check if server is running
echo "1. Checking if gRPC server is running..."
if ps aux | grep -v grep | grep -q "./bin/server\|go run.*server"; then
    echo "✓ gRPC server process is running"
else
    echo "✗ gRPC server is not running"
    echo "  Start with: ./bin/server"
    echo ""
fi

# Check if metrics endpoint is accessible
echo ""
echo "2. Checking metrics endpoint..."
if curl -f http://localhost:8081/health >/dev/null 2>&1; then
    echo "✓ Metrics server health endpoint is accessible"
    
    # Check if metrics are being generated
    echo ""
    echo "3. Checking if metrics are being generated..."
    METRICS_COUNT=$(curl -s http://localhost:8081/metrics | grep -c "grpc_" || echo "0")
    if [ "$METRICS_COUNT" -gt "0" ]; then
        echo "✓ Found $METRICS_COUNT gRPC metrics"
        echo ""
        echo "Sample metrics:"
        curl -s http://localhost:8081/metrics | grep "grpc_" | head -5
    else
        echo "! No gRPC metrics found - you may need to run some tests first"
        echo "  Run a quick test: ./bin/client -server=localhost:8080 -test=echo -duration=10s -concurrency=2"
    fi
else
    echo "✗ Metrics server is not accessible on localhost:8081"
    echo "  Check if the server started successfully"
    echo "  Check server logs for errors"
fi

# Check Prometheus
echo ""
echo "4. Checking Prometheus..."
if curl -f http://localhost:9090/-/healthy >/dev/null 2>&1; then
    echo "✓ Prometheus is running"
    
    # Check if Prometheus is scraping our target
    echo ""
    echo "5. Checking Prometheus targets..."
    TARGETS_RESPONSE=$(curl -s http://localhost:9090/api/v1/targets 2>/dev/null || echo "")
    if echo "$TARGETS_RESPONSE" | grep -q "grpc-benchmark-server"; then
        if echo "$TARGETS_RESPONSE" | grep -q '"health":"up"'; then
            echo "✓ Prometheus is successfully scraping gRPC metrics"
        else
            echo "! Prometheus target is configured but down"
            echo "  Check if target endpoint is localhost:8081 in Prometheus config"
        fi
    else
        echo "! gRPC benchmark target not found in Prometheus"
        echo "  Update Prometheus config to scrape localhost:8081"
    fi
else
    echo "✗ Prometheus is not running on localhost:9090"
fi

# Check Grafana
echo ""
echo "6. Checking Grafana..."
if curl -f http://localhost:3000/api/health >/dev/null 2>&1; then
    echo "✓ Grafana is running"
    echo "  Login at: http://localhost:3000 (admin/admin)"
else
    echo "✗ Grafana is not running on localhost:3000"
fi

echo ""
echo "=== Quick Fixes ==="
echo ""

# Generate some metrics
echo "7. Generating test metrics..."
if curl -f http://localhost:8081/health >/dev/null 2>&1; then
    echo "Running quick test to generate metrics..."
    ./bin/client -server=localhost:8080 -test=echo -duration=5s -concurrency=2 >/dev/null 2>&1 || echo "Test failed - check server"
    
    # Check metrics again
    echo ""
    echo "Metrics after test:"
    METRICS_COUNT=$(curl -s http://localhost:8081/metrics | grep -c "grpc_unary_requests_total\|grpc_streaming_requests_total" || echo "0")
    echo "Found $METRICS_COUNT request metrics"
    
    if [ "$METRICS_COUNT" -gt "0" ]; then
        echo ""
        echo "Sample request metrics:"
        curl -s http://localhost:8081/metrics | grep -E "grpc_(unary|streaming)_requests_total" | head -3
    fi
fi

echo ""
echo "=== Troubleshooting Summary ==="
echo ""
echo "If you don't see metrics in Grafana:"
echo "1. Ensure gRPC server is running: ./bin/server"
echo "2. Run some tests to generate metrics: ./scripts/load-test.sh -d 30s -c 5"
echo "3. Check metrics endpoint: curl http://localhost:8081/metrics"
echo "4. Verify Prometheus scraping: http://localhost:9090/targets"
echo "5. Check Grafana datasource: http://localhost:3000/datasources"
echo ""
echo "Dashboard URLs:"
echo "- Prometheus: http://localhost:9090"
echo "- Grafana: http://localhost:3000"
echo "- Metrics endpoint: http://localhost:8081/metrics"
