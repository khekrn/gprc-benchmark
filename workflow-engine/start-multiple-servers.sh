#!/bin/bash

# Start multiple workflow engine server instances

echo "Starting Workflow Engine Server Instances..."

# Build the server first
cd go
make build
cd ..

# Start server instance 1 (default ports)
echo "Starting Server Instance 1 (HTTP: 8080, gRPC: 9090)..."
cd go
echo "Running: ./bin/workflow-engine (default config)"
./bin/workflow-engine &
SERVER1_PID=$!
echo "Server 1 started with PID: $SERVER1_PID"
cd ..

# Wait a moment for first server to start
sleep 2

# Start server instance 2 
echo "Starting Server Instance 2 (HTTP: 8081, gRPC: 9091)..."
cd go
echo "Running: ./bin/workflow-engine -config config/config-instance2.yaml"
./bin/workflow-engine -config config/config-instance2.yaml &
SERVER2_PID=$!
echo "Server 2 started with PID: $SERVER2_PID"
cd ..

# Wait a moment 
sleep 2

# Start server instance 3
echo "Starting Server Instance 3 (HTTP: 8082, gRPC: 9092)..."
cd go
echo "Running: ./bin/workflow-engine -config config/config-instance3.yaml"
./bin/workflow-engine -config config/config-instance3.yaml &
SERVER3_PID=$!
echo "Server 3 started with PID: $SERVER3_PID"
cd ..

echo ""
echo "🚀 All server instances started!"
echo "📊 Server endpoints:"
echo "   - Instance 1: HTTP=:8080, gRPC=:9090 (PID: $SERVER1_PID)"
echo "   - Instance 2: HTTP=:8081, gRPC=:9091 (PID: $SERVER2_PID)" 
echo "   - Instance 3: HTTP=:8082, gRPC=:9092 (PID: $SERVER3_PID)"
echo ""
echo "💡 Test with:"
echo "   curl http://localhost:8080/health"
echo "   curl http://localhost:8081/health" 
echo "   curl http://localhost:8082/health"
echo ""
echo "🛑 To stop all servers: pkill -f workflow-engine"

# Keep script running to show process info
wait