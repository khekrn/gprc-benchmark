# Workflow Engine - Go Implementation

A high-performance workflow engine built with Go, featuring gRPC bidirectional streaming for real-time communication between the engine and workers.

## Features

- **gRPC Bidirectional Streaming**: Real-time communication between engine and workers
- **PostgreSQL Integration**: Robust data persistence with optimized schemas
- **REST API**: HTTP endpoints for workflow management
- **Worker Registration**: Dynamic worker registration and management
- **Workflow Execution**: Supports complex workflows with tasks and conditions
- **Connection Pooling**: Efficient connection management with 300-second idle timeout
- **Health Monitoring**: Built-in health checks and metrics

## Architecture

The workflow engine consists of:

1. **HTTP Server**: REST API for starting workflows and checking status
2. **gRPC Server**: Bidirectional streaming for worker communication
3. **Database Layer**: PostgreSQL with optimized schemas for workflows, states, and variables
4. **Connection Manager**: Manages persistent connections to workers

## Prerequisites

- Go 1.21 or later
- PostgreSQL 13 or later
- Protocol Buffers compiler (protoc)
- Docker and Docker Compose (for containerized deployment)

## Quick Start

### Using Docker Compose (Recommended)

1. **Start the services**:

   ```bash
   make docker-run
   ```

   This will start:

   - PostgreSQL database with initialized schema
   - Workflow engine server

2. **The services will be available at**:
   - HTTP API: `http://localhost:8080`
   - gRPC API: `localhost:9090`
   - PostgreSQL: `localhost:5432`

### Manual Setup

1. **Clone and setup**:

   ```bash
   git clone <repository>
   cd workflow-engine/go
   make dev-setup
   ```

2. **Start PostgreSQL** and create the database:

   ```bash
   createdb workflow_engine
   psql -d workflow_engine -f scripts/init.sql
   ```

3. **Configure environment**:

   ```bash
   cp .env.example .env
   # Edit .env with your database credentials
   ```

4. **Run the server**:
   ```bash
   make run
   ```

## API Documentation

### REST Endpoints

#### Start Workflow

```bash
POST /api/v1/workflows/start
Content-Type: application/json

{
  "workflow_name": "loan_approval",
  "payload": {
    "application_id": "12345",
    "amount": 50000,
    "applicant": {
      "name": "John Doe",
      "pan": "ABCDE1234F"
    }
  }
}
```

Response:

```json
{
  "success": true,
  "message": "Workflow started successfully",
  "workflow_id": 1,
  "request_id": "uuid-string"
}
```

#### Get Workflow Status

```bash
GET /api/v1/workflows/{id}
```

Response:

```json
{
  "success": true,
  "workflow": {
    "id": 1,
    "name": "loan_approval",
    "rid": "uuid-string",
    "type": "loan_approval",
    "status": "s",
    "created_at": "2025-10-03T10:00:00Z",
    "updated_at": "2025-10-03T10:05:00Z"
  }
}
```

#### Health Check

```bash
GET /health
```

#### Active Connections

```bash
GET /api/v1/connections
```

### gRPC API

The gRPC service provides:

1. **RegisterEndpoint**: Register a worker endpoint
2. **WorkflowStream**: Bidirectional streaming for workflow execution

## Database Schema

The system uses four main tables in the `waves` schema:

- **endpoint**: Stores registered worker endpoints
- **workflow**: Tracks workflow instances
- **state**: Records individual workflow step states
- **variables**: Stores workflow variables as JSONB

## Workflow Example: Loan Approval

The loan approval workflow follows this sequence:

```
PostLoanApplication → PanVerification → AadhaarVerification →
BureauPull → FinalDecision → UpdateStatus → SendCallback
```

Each step can succeed or fail, with appropriate error handling and callbacks.

## Development

### Available Commands

```bash
make help          # Show all available commands
make build         # Build the application
make run           # Run locally
make test          # Run tests
make proto         # Generate protobuf files
make docker-build  # Build Docker image
make docker-run    # Run with Docker Compose
make fmt           # Format code
make lint          # Run linter
```

### Project Structure

```
.
├── cmd/
│   └── server/          # Application entry point
├── internal/
│   ├── config/          # Configuration management
│   ├── database/        # Database operations
│   ├── grpc/           # gRPC server implementation
│   ├── handlers/       # HTTP handlers
│   └── models/         # Data models
├── proto/              # Protocol Buffer definitions
├── scripts/            # Database scripts
├── Dockerfile          # Container definition
├── docker-compose.yml  # Multi-service setup
└── Makefile           # Build automation
```

### Testing the Implementation

1. **Start the server**:

   ```bash
   make docker-run
   ```

2. **Test worker registration** (using grpcurl):

   ```bash
   grpcurl -plaintext -d '{
     "name": "loan_approval",
     "endpoint": "localhost:9091"
   }' localhost:9090 workflow.v1.WorkflowService/RegisterEndpoint
   ```

3. **Start a workflow**:
   ```bash
   curl -X POST http://localhost:8080/api/v1/workflows/start \
     -H "Content-Type: application/json" \
     -d '{
       "workflow_name": "loan_approval",
       "payload": {"application_id": "12345"}
     }'
   ```

## Performance Considerations

- **Connection Pooling**: Maintains persistent connections for 300 seconds
- **Streaming Efficiency**: Uses gRPC bidirectional streaming for low latency
- **Database Optimization**: Indexed queries and prepared statements
- **Concurrent Processing**: Goroutine-based concurrent request handling

## Monitoring and Observability

- Health check endpoint at `/health`
- Active connection monitoring at `/api/v1/connections`
- Structured logging throughout the application
- Database connection health checks

## Next Steps

After implementing the Go server, you can:

1. Implement the worker client in Go
2. Create Java Vert.x implementation
3. Create Kotlin Coroutine implementation
4. Add comprehensive benchmarking tools
5. Implement advanced features like workflow orchestration and monitoring
