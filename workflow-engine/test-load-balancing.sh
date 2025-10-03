#!/bin/bash

# Test script for load-balanced workflow engine system

echo "🧪 Testing Load-Balanced Workflow Engine System"
echo "==============================================="

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

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

# Test server health endpoints
test_server_health() {
    print_status "Testing server health endpoints..."
    
    for port in 8080 8081 8082; do
        response=$(curl -s http://localhost:$port/health 2>/dev/null)
        if echo "$response" | grep -q "healthy"; then
            print_success "✓ Server on port $port is healthy"
        else
            print_error "✗ Server on port $port is not responding"
        fi
    done
}

# Test workflow execution on different servers
test_workflow_execution() {
    print_status "Testing workflow execution on different servers..."
    
    for port in 8080 8081 8082; do
        print_status "Sending workflow to server on port $port..."
        
        response=$(curl -s -X POST http://localhost:$port/api/v1/workflows/start \
            -H "Content-Type: application/json" \
            -d '{
                "workflow_name": "loan_approval",
                "payload": {
                    "application_data": {
                        "application_id": "TEST_'$port'_001",
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
        
        if echo "$response" | grep -q '"success":true'; then
            workflow_id=$(echo "$response" | grep -o '"workflow_id":[0-9]*' | cut -d':' -f2)
            print_success "✓ Workflow started on server $port (ID: $workflow_id)"
        else
            print_error "✗ Failed to start workflow on server $port"
            echo "Response: $response"
        fi
        
        sleep 1
    done
}

# Main test execution
main() {
    print_status "Starting load balancing tests..."
    echo ""
    
    # Give servers time to start up
    print_status "Waiting for servers to be ready..."
    sleep 3
    
    # Test health endpoints
    test_server_health
    echo ""
    
    # Test workflow execution
    test_workflow_execution
    echo ""
    
    print_success "🎉 Load balancing tests completed!"
    echo ""
    print_status "Next steps:"
    echo "1. Check server logs to see which server handled which workflow"
    echo "2. Verify worker logs show round-robin connection to different servers"
    echo "3. Monitor database to see workflows distributed across instances"
    echo ""
    print_status "Manual testing:"
    echo "- Send multiple requests rapidly to see load distribution"
    echo "- curl -X POST http://localhost:8080/api/v1/workflows/start [...]"
    echo "- curl -X POST http://localhost:8081/api/v1/workflows/start [...]"
    echo "- curl -X POST http://localhost:8082/api/v1/workflows/start [...]"
}

# Run tests
main