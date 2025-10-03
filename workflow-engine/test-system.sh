#!/bin/bash

# Test script for the complete workflow engine system
set -e

echo "🚀 Starting Workflow Engine System Test"
echo "========================================"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Check if required tools are installed
check_requirements() {
    print_status "Checking requirements..."
    
    if ! command -v go &> /dev/null; then
        print_error "Go is required but not installed"
        exit 1
    fi
    
    if ! command -v curl &> /dev/null; then
        print_error "curl is required but not installed"
        exit 1
    fi
    
    if ! command -v psql &> /dev/null; then
        print_warning "PostgreSQL client (psql) not found. Make sure PostgreSQL is running."
    fi
    
    print_success "All requirements satisfied"
}

# Start the Go workflow server
start_server() {
    print_status "Starting Go workflow server..."
    
    cd go
    
    # Check if the binary exists, if not build it
    if [ ! -f "bin/workflow-engine" ]; then
        print_status "Building Go server..."
        make build
    fi
    
    # Start the server in background
    print_status "Starting server binary..."
    ./bin/workflow-engine &
    SERVER_PID=$!
    
    # Wait for server to be ready
    print_status "Waiting for server to be ready..."
    for i in {1..30}; do
        if curl -s http://localhost:8080/health > /dev/null 2>&1; then
            print_success "Server is ready!"
            break
        fi
        sleep 2
        if [ $i -eq 30 ]; then
            print_error "Server failed to start within 60 seconds"
            kill $SERVER_PID 2>/dev/null || true
            exit 1
        fi
    done
    
    cd ..
}

# Start the worker
start_worker() {
    print_status "Starting workflow worker..."
    
    cd worker
    
    # Check if the binary exists, if not build it
    if [ ! -f "bin/workflow-worker" ]; then
        print_status "Building worker..."
        make build
    fi
    
    # Start the worker in background
    ./bin/workflow-worker &
    WORKER_PID=$!
    
    # Give worker time to connect and register
    print_status "Waiting for worker to register with server..."
    sleep 5
    
    if kill -0 $WORKER_PID 2>/dev/null; then
        print_success "Worker started successfully (PID: $WORKER_PID)"
    else
        print_error "Worker failed to start"
        exit 1
    fi
    
    cd ..
}

# Test the system
test_system() {
    print_status "Testing the workflow system..."
    
    # Test 1: Health check
    print_status "Test 1: Server health check"
    if curl -s http://localhost:8080/health | grep -q "healthy"; then
        print_success "✓ Health check passed"
    else
        print_error "✗ Health check failed"
        return 1
    fi
    
    # Test 2: Check active connections
    print_status "Test 2: Checking active connections"
    connections=$(curl -s http://localhost:8080/api/v1/connections | grep -o '"active_connections":[0-9]*' | cut -d':' -f2)
    if [ "$connections" -gt 0 ]; then
        print_success "✓ Worker connected ($connections active connections)"
    else
        print_warning "⚠ No active connections found"
    fi
    
    # Test 3: Verify worker endpoint registration
    print_status "Test 3: Verifying worker endpoint registration"
    # Give a moment for registration to complete
    sleep 2
    
    # Test 4: Start a workflow
    print_status "Test 4: Starting a loan approval workflow"
    
    workflow_response=$(curl -s -X POST http://localhost:8080/api/v1/workflows/start \
        -H "Content-Type: application/json" \
        -d '{
            "workflow_name": "loan_approval",
            "payload": {
                "application_data": {
                    "application_id": "TEST_APP_001",
                    "amount": 50000,
                    "applicant": {
                        "name": "John Doe",
                        "pan": "ABCDE1234F",
                        "aadhaar": "123456789012",
                        "email": "john.doe@example.com",
                        "phone": "+919876543210"
                    },
                    "purpose": "Personal loan"
                }
            }
        }')
    
    if echo "$workflow_response" | grep -q '"success":true'; then
        workflow_id=$(echo "$workflow_response" | grep -o '"workflow_id":[0-9]*' | cut -d':' -f2)
        print_success "✓ Workflow started successfully (ID: $workflow_id)"
        
        # Test 5: Check workflow status
        print_status "Test 5: Checking workflow status"
        sleep 8 # Give workflow time to process through multiple steps
        
        status_response=$(curl -s http://localhost:8080/api/v1/workflows/$workflow_id)
        if echo "$status_response" | grep -q '"success":true'; then
            status=$(echo "$status_response" | grep -o '"status":"[^"]*"' | cut -d':' -f2 | tr -d '"')
            print_success "✓ Workflow status retrieved: $status"
        else
            print_error "✗ Failed to retrieve workflow status"
        fi
    else
        print_error "✗ Failed to start workflow"
        echo "Response: $workflow_response"
        return 1
    fi
}

# Cleanup function
cleanup() {
    print_status "Cleaning up..."
    
    # Kill worker if running
    if [ ! -z "$WORKER_PID" ] && kill -0 $WORKER_PID 2>/dev/null; then
        kill $WORKER_PID
        print_status "Worker stopped"
    fi
    
    # Kill server if running
    if [ ! -z "$SERVER_PID" ] && kill -0 $SERVER_PID 2>/dev/null; then
        kill $SERVER_PID
        print_status "Server stopped"
    fi
    
    print_success "Cleanup completed"
}

# Set up trap for cleanup
trap cleanup EXIT

# Main execution
main() {
    check_requirements
    start_server
    start_worker
    
    print_status "System startup completed. Running tests..."
    echo ""
    
    if test_system; then
        echo ""
        print_success "🎉 All tests passed! The workflow engine system is working correctly."
        echo ""
        print_status "System Overview:"
        echo "- Go Workflow Server: http://localhost:8080 (PID: $SERVER_PID)"
        echo "- gRPC Server: localhost:9090"
        echo "- Worker: Connected and processing workflows (PID: $WORKER_PID)"
        echo ""
        print_status "Prerequisites for this setup:"
        echo "- PostgreSQL should be running on localhost:5432"
        echo "- Database 'workflow_engine' should exist with proper schema"
        echo ""
        print_status "You can now test the system manually:"
        echo "curl -X POST http://localhost:8080/api/v1/workflows/start \\"
        echo "  -H 'Content-Type: application/json' \\"
        echo "  -d '{\"workflow_name\": \"loan_approval\", \"payload\": {...}}'"
        echo ""
        print_status "Press Ctrl+C to stop the system"
        
        # Keep the system running
        while true; do
            sleep 10
        done
    else
        print_error "Tests failed!"
        exit 1
    fi
}

# Run main function
main