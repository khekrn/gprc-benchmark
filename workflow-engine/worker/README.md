# Workflow Worker

A workflow execution worker that connects to the workflow engine via gRPC bidirectional streaming. This worker implements the loan approval workflow and can communicate with any workflow server (Go, Java, or Kotlin implementations).

## Features

- **gRPC Bidirectional Streaming**: Real-time communication with workflow servers
- **Loan Approval Workflow**: Complete implementation of the loan approval process
- **Dynamic Workflow Registration**: Registers workflows with the server on startup
- **State Management**: Tracks and reports workflow execution states
- **Configurable**: Environment-based configuration
- **Cross-Platform**: Works with Go, Java, and Kotlin workflow servers

## Workflow Implementation

The worker implements the **Loan Approval Workflow** with the following steps:

```
PostLoanApplication → PanVerification → AadhaarVerification →
BureauPull → FinalDecision → UpdateStatus → SendCallback
```

Each step includes:

- **Tasks**: Actual processing (API calls, validations, etc.)
- **Conditions**: Decision points that determine the next step
- **State Updates**: Real-time progress reporting to the server
- **Error Handling**: Graceful failure handling with appropriate callbacks

### Workflow Steps Detail

1. **PostLoanApplication**: Validates and processes the loan application
2. **PanVerification**: Verifies the applicant's PAN number
3. **AadhaarVerification**: Verifies the applicant's Aadhaar number
4. **BureauPull**: Pulls credit bureau data and calculates credit score
5. **FinalDecision**: Makes the final approval/rejection decision
6. **UpdateStatus**: Updates the application status
7. **SendCallback**: Sends the final result to the requesting system

## Quick Start

### Prerequisites

- Go 1.21 or later
- Protocol Buffers compiler (protoc)
- A running workflow server (Go/Java/Kotlin)

### Setup and Run

1. **Install dependencies**:

   ```bash
   make deps
   ```

2. **Generate protobuf files**:

   ```bash
   make proto
   ```

3. **Configure environment**:

   ```bash
   cp .env.example .env
   # Edit .env with your configuration
   ```

4. **Run the worker**:
   ```bash
   make run
   ```

## Configuration

Configure the worker using environment variables in `.env`:

```bash
# Worker Configuration
WORKER_NAME=loan_approval_worker
WORKER_ENDPOINT=localhost:9091
WORKER_PORT=9091

# Server Configuration
SERVER_ADDRESS=localhost
SERVER_PORT=9090
```

## API Communication

### Registration

The worker registers itself with the workflow server on startup:

```protobuf
rpc RegisterEndpoint(RegisterEndpointRequest) returns (RegisterEndpointResponse);
```

### Streaming Communication

Bidirectional streaming for workflow execution and state updates:

```protobuf
rpc WorkflowStream(stream WorkflowStreamRequest) returns (stream WorkflowStreamResponse);
```

### Message Types

- **StateUpdate**: Reports step execution progress
- **WorkflowComplete**: Reports workflow completion
- **ExecutionRequest**: Receives workflow execution requests

## Development

### Available Commands

```bash
make help          # Show all available commands
make build         # Build the application
make run           # Run locally
make test          # Run tests
make proto         # Generate protobuf files
make fmt           # Format code
make lint          # Run linter
make dev           # Full development cycle
```

### Project Structure

```
.
├── cmd/
│   └── worker/          # Application entry point
├── internal/
│   ├── config/          # Configuration management
│   ├── engine/          # Workflow execution engine
│   │   ├── engine.go    # Core engine logic
│   │   └── handlers.go  # Workflow step handlers
│   └── grpc/           # gRPC client implementation
├── proto/              # Protocol Buffer definitions
├── .env                # Environment configuration
├── go.mod              # Go module definition
├── Makefile           # Build automation
└── README.md          # This file
```

## Testing

### Manual Testing

1. **Start a workflow server**:

   ```bash
   # In the go server directory
   cd ../go
   make docker-run
   ```

2. **Start the worker**:

   ```bash
   make run
   ```

3. **Trigger a workflow**:
   ```bash
   curl -X POST http://localhost:8080/api/v1/workflows/start \
     -H "Content-Type: application/json" \
     -d '{
       "workflow_name": "loan_approval",
       "payload": {
         "application_data": {
           "application_id": "APP_001",
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
     }'
   ```

### Sample Workflow Data

```json
{
  "application_data": {
    "application_id": "APP_001",
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
```

## Workflow Step Details

### PAN Verification

- Simulates external PAN verification API
- 90% success rate for testing
- Returns verification score and status

### Aadhaar Verification

- Simulates external Aadhaar verification API
- 85% success rate for testing
- Returns verification score and status

### Credit Bureau Pull

- Simulates credit bureau API call
- Generates mock credit score (300-850)
- Returns comprehensive bureau data

### Final Decision Logic

- Requires: PAN verified + Aadhaar verified + Credit Score ≥ 650
- Calculates interest rate (8.5% - 12%)
- Sets approval amount and terms

## Compatibility

This worker is designed to work with:

- **Go Workflow Server**: Native gRPC communication
- **Java Vert.x Server**: Cross-language gRPC streaming
- **Kotlin Coroutine Server**: Reactive gRPC communication

## Monitoring

The worker provides:

- Structured logging of all workflow steps
- Real-time state updates to the server
- Error reporting and recovery
- Connection status monitoring
- Performance metrics logging

## Next Steps

1. Add comprehensive unit tests
2. Implement retry mechanisms for failed steps
3. Add metrics and monitoring endpoints
4. Implement dynamic workflow loading
5. Add support for custom workflow definitions
6. Implement workflow scheduling and queuing
