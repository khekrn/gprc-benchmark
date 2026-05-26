#!/bin/bash

echo "=== Debugging Synchronous Workflow API ==="
echo ""

# Check if Redis is running
echo "1. Checking Redis connectivity..."
redis-cli ping 2>/dev/null || echo "⚠️  Redis not responding"
echo ""

# Check server health
echo "2. Checking server health..."
curl -s http://localhost:8080/health | jq . 2>/dev/null || echo "⚠️  Server not responding"
echo ""

# Check worker connections
echo "3. Checking worker connections..."
curl -s http://localhost:8080/api/v1/connections | jq . 2>/dev/null || echo "⚠️  Cannot fetch connections"
echo ""

# Check workflow mappings  
echo "4. Checking workflow mappings..."
curl -s http://localhost:8080/api/v1/workflows/mappings | jq . 2>/dev/null || echo "⚠️  Cannot fetch mappings"
echo ""

# Check system metrics
echo "5. Checking system metrics..."
curl -s http://localhost:8080/api/v1/metrics | jq . 2>/dev/null || echo "⚠️  Cannot fetch metrics"
echo ""

# Try to register workflow endpoint (this creates the workflow in database)
echo "6. Registering workflow endpoint..."
curl -X POST http://localhost:8080/api/v1/workflows/endpoints \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_name": "loan_approval",
    "endpoints": ["http://localhost:9191"]
  }' | jq . 2>/dev/null || echo "⚠️  Cannot register endpoint"
echo ""

# Test async workflow first (simpler)
echo "7. Testing async workflow..."
curl -X POST http://localhost:8080/api/v1/workflows/start \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_name": "loan_approval",
    "payload": {
      "customer_id": "TEST_'$(date +%s)'",
      "loan_amount": 10000
    }
  }' | jq . 2>/dev/null || echo "⚠️  Async workflow failed"
echo ""

# Wait a moment
echo "8. Waiting 3 seconds..."
sleep 3
echo ""

# Test sync workflow
echo "9. Testing sync workflow..."
curl -X POST http://localhost:8080/api/v1/workflows/start-sync \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_name": "loan_approval",
    "timeout_sec": 10,
    "payload": {
      "customer_id": "SYNC_TEST_'$(date +%s)'",
      "loan_amount": 5000
    }
  }' | jq . 2>/dev/null || echo "⚠️  Sync workflow failed"

echo ""
echo "=== Debug Complete ==="
echo ""
echo "If you see ⚠️  warnings above, check:"
echo "1. Redis is running: redis-server"
echo "2. Server is running: ./bin/workflow-engine"  
echo "3. Worker is running: cd ../worker && ./bin/workflow-worker"
echo "4. Check server logs for detailed error messages"