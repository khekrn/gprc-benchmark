#!/bin/bash

# Script to test load balancing with multiple servers and workers

echo "Testing Load Balancing with Multiple Servers and Workers..."
echo "=================================================="

# Function to test a server
test_server() {
    local server_port=$1
    local server_name=$2
    
    echo ""
    echo "Testing $server_name (port $server_port)..."
    
    # Health check
    echo "Health check:"
    curl -s "http://localhost:$server_port/health" | jq .
    
    # Start workflow
    echo ""
    echo "Starting workflow..."
    curl -X POST "http://localhost:$server_port/api/v1/workflows/start" \
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
        }
      }' | jq .
    
    echo ""
    echo "Waiting for workflow to complete..."
    sleep 8
}

# Test both servers
test_server 8080 "Server 1"
test_server 8081 "Server 2"

echo ""
echo "=================================================="
echo "Load Balancing Test Completed!"
echo ""
echo "Check the logs to see which workers processed the requests:"
echo "Server logs: go/logs/server*.log"
echo "Worker logs: worker/logs/worker*.log"