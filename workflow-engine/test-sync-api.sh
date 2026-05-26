#!/bin/bash

# Script to test the synchronous workflow API

echo "Testing Synchronous Workflow API..."
echo "===================================="

# Function to test sync API on a server
test_sync_api() {
    local server_port=$1
    local server_name=$2
    
    echo ""
    echo "Testing $server_name (port $server_port) - Sync API..."
    
    # Test sync workflow execution
    echo "Starting synchronous workflow execution..."
    curl -X POST "http://localhost:$server_port/api/v1/workflows/start-sync" \
      -H "Content-Type: application/json" \
      -d '{
        "workflow_name": "loan_approval",
        "payload": {
          "customer_id": "CUST_'$(date +%s)'",
          "loan_amount": 50000,
          "loan_type": "personal",
          "application_data": {
            "application_id": "APP_'$(date +%s)'",
            "applicant": {
              "name": "John Doe",
              "email": "john.doe@example.com",
              "phone": "+1234567890",
              "pan": "ABCDE1234F",
              "aadhaar": "123456789012"
            },
            "loan_details": {
              "amount": 50000,
              "tenure": 24,
              "purpose": "personal"
            }
          }
        },
        "timeout_sec": 30
      }' | jq .
    
    echo ""
    echo "Sync API test completed for $server_name"
}

# Test both servers
test_sync_api 8080 "Server 1"
test_sync_api 8081 "Server 2"

echo ""
echo "===================================="
echo "Synchronous API Test Completed!"
echo ""
echo "The sync API should return the complete workflow result including:"
echo "- Final status (success/failed)"
echo "- All state transitions"
echo "- Final workflow variables"
echo "- Execution time"