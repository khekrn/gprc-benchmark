# Workflow Engine System Flow

## Architecture Overview

The workflow engine uses bidirectional gRPC streaming for real-time communication between the server and workers. This allows for immediate workflow execution and state tracking.

## Sequence Diagram

```mermaid
sequenceDiagram
    participant Client as HTTP Client
    participant Server as Go Server
    participant DB as PostgreSQL
    participant Worker as Workflow Worker
    participant Stream as gRPC Stream

    Note over Worker,Stream: 1. Worker Registration & Connection
    Worker->>Stream: Connect to gRPC WorkflowStream
    Worker->>Stream: Send WorkerRegistrationRequest{name: "loan_approval", endpoint: "localhost:9091"}
    Stream->>Server: Forward registration
    Server->>DB: CreateEndpoint("loan_approval", "localhost:9091")
    DB-->>Server: Endpoint created
    Server->>Stream: WorkerRegistrationResponse{success: true}
    Stream->>Worker: Registration confirmed
    Note over Server: Worker "loan_approval" is now registered and stream is active

    Note over Client,Worker: 2. Workflow Execution Request
    Client->>Server: POST /api/v1/workflows/start {"workflow_name": "loan_approval", "payload": {...}}
    Server->>DB: GetEndpointByName("loan_approval")
    DB-->>Server: Endpoint exists
    Server->>DB: CreateWorkflow("loan_approval", requestID, "loan_approval")
    DB-->>Server: Workflow created (ID: 1)

    Server->>Server: Check activeEndpoints for "loan_approval"
    Server->>Stream: SendWorkflowExecution(loan_approval, workflowID=1)
    Stream->>Worker: WorkflowExecutionResponse{workflowId: 1}
    Server-->>Client: 200 OK {workflow_id: 1, success: true}

    Note over Worker,Server: 3. Workflow Step Execution
    Worker->>Worker: Start loan_approval workflow

    loop For each workflow step
        Worker->>Worker: Execute step (e.g., PostLoanApplication)
        Worker->>Stream: StateUpdateRequest{workflowId: 1, stateName: "PostLoanApplication", status: "p"}
        Stream->>Server: Forward state update
        Server->>DB: CreateState(1, "PostLoanApplication", "task", "p")
        Server->>Stream: StateUpdateResponse{success: true}
        Stream->>Worker: State update confirmed

        Worker->>Worker: Process business logic
        Worker->>Stream: StateUpdateRequest{workflowId: 1, stateName: "PostLoanApplication", status: "s"}
        Stream->>Server: Forward completion
        Server->>DB: UpdateStateStatus(1, "PostLoanApplication", "s")
        Server->>Stream: StateUpdateResponse{success: true}
        Stream->>Worker: Completion confirmed
    end

    Note over Worker,Server: 4. Workflow Completion
    Worker->>Stream: WorkflowCompleteRequest{workflowId: 1, status: "s", variables: "{}"}
    Stream->>Server: Forward completion
    Server->>DB: UpdateWorkflowStatus(1, "s")
    Server->>Stream: WorkflowCompleteResponse{success: true}
    Stream->>Worker: Completion confirmed

    Note over Client,Server: 5. Status Checking
    Client->>Server: GET /api/v1/workflows/1
    Server->>DB: GetWorkflowStatus(1)
    DB-->>Server: Status: "s" (success)
    Server-->>Client: 200 OK {status: "s", workflow: {...}}
```

## Key Improvements Made

### 1. **Stream-Based Registration**

- Workers now register themselves via the gRPC stream as the first message
- Server associates the stream with the endpoint name
- No separate registration call needed

### 2. **Endpoint-to-Stream Association**

- Server tracks streams by endpoint name instead of random IDs
- When a workflow request comes in, server finds the exact worker for that workflow
- Direct routing: `loan_approval` workflow → `loan_approval` worker

### 3. **Real-time State Tracking**

- All state changes are immediately sent to server via stream
- Database is updated in real-time
- Server acknowledges each state change

### 4. **Persistent Connections**

- Workers maintain persistent gRPC connections
- Automatic cleanup when workers disconnect
- Server tracks active connections for availability checking

## Database Schema

```sql
-- Endpoints table
waves.endpoint (id, name, endpoint, created_at, updated_at)

-- Workflows table
waves.workflow (id, name, rid, type, status, created_at, updated_at)

-- States table
waves.state (id, workflow_id, name, type, status, created_at, updated_at)

-- Variables table
waves.variables (id, workflow_id, data, created_at, updated_at)
```

## Workflow Steps

The `loan_approval` workflow has 7 steps:

1. **PostLoanApplication** (task) → Process loan application
2. **PostLoanApplicationCond** (condition) → Check if application is valid
3. **PanVerification** (task) → Verify PAN number
4. **PanVerificationCond** (condition) → Check PAN verification result
5. **AadhaarVerification** (task) → Verify Aadhaar number
6. **AadhaarVerificationCond** (condition) → Check Aadhaar result
7. **SendCallback** (task) → Send final callback

Each step updates its state: `p` (pending) → `s` (success) or `f` (failed)

## API Endpoints

- **POST** `/api/v1/workflows/start` - Start a workflow
- **GET** `/api/v1/workflows/:id` - Get workflow status
- **GET** `/api/v1/connections` - List active worker connections
- **GET** `/health` - Health check

## Success Indicators

✅ Worker registration: "Worker loan_approval registered with stream"
✅ Endpoint creation: "Created new endpoint: loan_approval -> localhost:9091"  
✅ Active connections: "1 active connections"
✅ Workflow execution: "Workflow execution started: ID 1"
✅ State tracking: All state updates confirmed
✅ Completion: "Workflow completed successfully"

The system now provides real-time workflow execution with persistent worker connections, making it ready for high-performance benchmarking across Go, Java, and Kotlin implementations!
