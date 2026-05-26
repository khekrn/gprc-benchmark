# Simplified Workflow Execution Architecture

## Overview

This implementation provides a simplified, effective approach to workflow execution with dynamic worker discovery and load balancing.

## Architecture Flow

### 1. Workflow Request Processing

```
HTTP API Request → Server → Redis Worker Discovery → Stream Connection → Worker Execution
```

### 2. Key Components

#### WorkerPoolManager (`pool_manager.go`)

- **Purpose**: Manages workflow execution requests
- **Key Method**: `ExecuteWorkflow(workflowName, requestID, workflowID, payload)`
- **Flow**:
  1. Fetches available workers for the workflow from Redis
  2. Creates a WorkflowExecutor
  3. Delegates execution to the executor

#### WorkflowExecutor (`executor.go`)

- **Purpose**: Handles individual workflow execution with streaming connections
- **Key Features**:
  - Establishes bidirectional streaming connections with all available workers
  - Implements load balancing (currently simple selection, extensible)
  - Handles worker responses asynchronously
  - Automatically closes connections when workflow completes

#### Redis Client (`redis/client.go`)

- **Enhanced**: Added `WorkflowTypes` field to `WorkerInfo`
- **New Method**: `GetWorkersForWorkflow(workflowName)` - filters workers by workflow type
- **Worker Discovery**: Workers register themselves with supported workflow types

## API Endpoints

### Workflow Execution

```bash
POST /api/v1/workflows/start
{
  "workflow_name": "loan_approval",
  "payload": {"amount": 50000, "customer_id": "123"}
}
```

### Workflow Endpoint Management (for testing/configuration)

```bash
# Register endpoints for a workflow
POST /api/v1/workflows/endpoints
{
  "workflow_name": "loan_approval",
  "endpoints": ["localhost:8081", "localhost:8082"]
}

# Get all workflow-to-endpoints mappings
GET /api/v1/workflows/mappings
```

### System Monitoring

```bash
GET /health                    # Health check
GET /api/v1/connections       # Connection statistics
GET /api/v1/workflows/:id     # Workflow status
```

## Workflow Execution Flow

### Step 1: API Request

Client sends POST request to `/api/v1/workflows/start` with workflow name and payload.

### Step 2: Worker Discovery

Server calls `redis.GetWorkersForWorkflow(workflowName)` to find all online workers that can handle the specific workflow type.

### Step 3: Connection Establishment

WorkflowExecutor creates bidirectional gRPC streaming connections with all discovered workers:

```go
stream, err := client.WorkflowStream(ctx)
```

### Step 4: Load Balancing & Execution

- Selects one worker using load balancing strategy
- Sends `WorkflowExecutionRequest` via the stream
- Monitors for responses asynchronously

### Step 5: Response Handling

Handles three types of worker responses:

- `ExecutionResponse`: Acknowledgment that workflow started
- `StateUpdate`: Progress updates during workflow execution
- `WorkflowComplete`: Final completion status

### Step 6: Connection Cleanup

When workflow completes, closes the streaming connection and cleans up resources.

## Key Benefits

1. **Dynamic Discovery**: No persistent connections - fetches workers fresh for each request
2. **Load Balancing**: Can distribute requests across multiple workers for the same workflow
3. **Workflow Isolation**: Each workflow type (loan_approval, presentment, etc.) has its own worker pool
4. **Horizontal Scaling**: Workers can be added/removed dynamically via Redis registration
5. **Connection Efficiency**: Establishes connections only when needed, closes when done
6. **Fault Tolerance**: Failed connections don't affect other workers

## Example Usage

### 1. Start the server

```bash
./bin/workflow-server -config config/config.yaml
```

### 2. Register workflow endpoints (for testing)

```bash
curl -X POST http://localhost:8080/api/v1/workflows/endpoints \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_name": "loan_approval",
    "endpoints": ["localhost:8081", "localhost:8082"]
  }'
```

### 3. Execute a workflow

```bash
curl -X POST http://localhost:8080/api/v1/workflows/start \
  -H "Content-Type: application/json" \
  -d '{
    "workflow_name": "loan_approval",
    "payload": {"amount": 50000, "customer_id": "123"}
  }'
```

### 4. Monitor connections

```bash
curl http://localhost:8080/api/v1/connections
```

## Worker Requirements

Workers need to:

1. Register themselves in Redis with `WorkflowTypes` field set
2. Implement the `WorkerService` gRPC interface
3. Handle bidirectional streaming via `WorkflowStream` method
4. Send appropriate responses: `ExecutionResponse`, `StateUpdate`, `WorkflowComplete`

## Configuration

Workers register with Redis using:

```go
workerInfo := WorkerInfo{
    Name:          "loan-worker-1",
    Endpoint:      "localhost:8081",
    WorkflowTypes: []string{"loan_approval"},
    Status:        "online",
    Capacity:      "10"
}
```

This simplified architecture provides a clean, scalable solution for workflow execution with dynamic worker discovery and efficient resource management.
