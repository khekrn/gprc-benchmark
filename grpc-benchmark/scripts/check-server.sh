#!/bin/bash

# Quick server connectivity test
set -e

SERVER_HOST=${1:-"localhost:8080"}

echo "Testing gRPC server connectivity on $SERVER_HOST..."

# Check if client binary exists
if [ ! -f "./bin/client" ]; then
    echo "Building client binary..."
    make build
fi

# Try a quick echo test
echo "Attempting gRPC echo test..."
if ./bin/client -server=$SERVER_HOST -test=echo -duration=5s -concurrency=1; then
    echo ""
    echo "✓ Server is running and responding correctly!"
    echo "✓ You can now run load tests"
else
    echo ""
    echo "✗ Server test failed"
    echo ""
    echo "Troubleshooting steps:"
    echo "1. Check if server is running: ps aux | grep server"
    echo "2. Check if port is open: lsof -i :8080"
    echo "3. Start server: ./bin/server"
    echo "4. Check server logs for errors"
fi
