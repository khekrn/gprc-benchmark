#!/bin/bash

# Script to start multiple workflow workers

echo "Starting multiple workflow workers..."

# Kill any existing workers
pkill -f workflow-worker
sleep 2

# Create logs directory if it doesn't exist
mkdir -p worker/logs

# Build the worker
cd worker
make build

# Start Worker 1 (port 9191)
echo "Starting Worker 1 (port 9191)..."
./bin/workflow-worker -config config/config.yaml > logs/worker1.log 2>&1 &
WORKER1_PID=$!
echo "Worker 1 started with PID: $WORKER1_PID"

# Wait a moment between starts
sleep 2

# Start Worker 2 (port 9192)
echo "Starting Worker 2 (port 9192)..."
./bin/workflow-worker -config config/config-worker2.yaml > logs/worker2.log 2>&1 &
WORKER2_PID=$!
echo "Worker 2 started with PID: $WORKER2_PID"

# Wait a moment between starts
sleep 2

# Start Worker 3 (port 9193)
echo "Starting Worker 3 (port 9193)..."
./bin/workflow-worker -config config/config-worker3.yaml > logs/worker3.log 2>&1 &
WORKER3_PID=$!
echo "Worker 3 started with PID: $WORKER3_PID"

cd ..

echo "All workers started successfully!"
echo "Worker 1: port 9191, PID=$WORKER1_PID"
echo "Worker 2: port 9192, PID=$WORKER2_PID"
echo "Worker 3: port 9193, PID=$WORKER3_PID"
echo ""
echo "Log files:"
echo "Worker 1: worker/logs/worker1.log"
echo "Worker 2: worker/logs/worker2.log"
echo "Worker 3: worker/logs/worker3.log"
echo ""
echo "To stop workers: pkill -f workflow-worker"