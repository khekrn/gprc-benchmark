#!/bin/bash

# Script to run worker with load balancing across multiple servers

echo "🔧 Starting Load-Balanced Workflow Worker"
echo "========================================="

# Colors for output
GREEN='\033[0;32m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

cd worker

echo -e "${BLUE}[INFO]${NC} Starting worker with load balancing..."
echo "Worker will connect to multiple servers:"
echo "- Server 1: localhost:9090"
echo "- Server 2: localhost:9091" 
echo "- Server 3: localhost:9092"
echo ""

# Start the worker with default config (which includes load balancing)
./bin/workflow-worker

echo -e "${GREEN}[SUCCESS]${NC} Worker stopped"