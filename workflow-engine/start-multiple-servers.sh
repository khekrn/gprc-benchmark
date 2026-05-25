#!/bin/bash

# Script to start multiple workflow servers

echo "Starting multiple workflow servers..."

# Kill any existing servers
pkill -f workflow-engine
sleep 2

# Create logs directory if it doesn't exist
mkdir -p go/logs

# Build the server
cd go
make build

# Start Server 1 (HTTP: 8080, gRPC: 9090)
echo "Starting Server 1 (HTTP: 8080, gRPC: 9090)..."
./bin/workflow-engine -config config/config.yaml > logs/server1.log 2>&1 &
SERVER1_PID=$!
echo "Server 1 started with PID: $SERVER1_PID"

# Wait a moment between starts
sleep 3

# Start Server 2 (HTTP: 8081, gRPC: 9091)
echo "Starting Server 2 (HTTP: 8081, gRPC: 9091)..."
./bin/workflow-engine -config config/config-server2.yaml > logs/server2.log 2>&1 &
SERVER2_PID=$!
echo "Server 2 started with PID: $SERVER2_PID"

cd ..

echo "Both servers started successfully!"
echo "Server 1: HTTP=8080, gRPC=9090, PID=$SERVER1_PID"
echo "Server 2: HTTP=8081, gRPC=9091, PID=$SERVER2_PID"
echo ""
echo "Health check URLs:"
echo "Server 1: http://localhost:8080/health"
echo "Server 2: http://localhost:8081/health"
echo ""
echo "Log files:"
echo "Server 1: go/logs/server1.log"
echo "Server 2: go/logs/server2.log"
echo ""
echo "To stop servers: pkill -f workflow-engine"# Keep script running to show process info
wait