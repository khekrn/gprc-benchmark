# gRPC Workflow Engine Benchmark

A high-performance, scalable workflow engine implementation using gRPC bidirectional streaming. This project compares the performance of different technology stacks for building distributed workflow systems.

## 🏗️ Architecture Overview

```
┌─────────────────┐    gRPC BiDi Stream    ┌──────────────────┐
│   Worker Node   │◄──────────────────────►│ Workflow Engine  │
│                 │                        │                  │
│ ┌─────────────┐ │                        │ ┌──────────────┐ │
│ │  Workflow   │ │   State Updates &      │ │   gRPC       │ │
│ │  Executor   │ │   Completions          │ │   Server     │ │
│ └─────────────┘ │                        │ └──────────────┘ │
│                 │                        │                  │
│ ┌─────────────┐ │                        │ ┌──────────────┐ │
│ │   Engine    │ │                        │ │   HTTP       │ │
│ │  (Go/Java/  │ │                        │ │   Server     │ │
│ │  Kotlin)    │ │                        │ └──────────────┘ │
│ └─────────────┘ │                        │                  │
└─────────────────┘                        │ ┌──────────────┐ │
                                           │ │ PostgreSQL   │ │
                                           │ │  Database    │ │
                                           │ └──────────────┘ │
                                           └──────────────────┘
```

## 📋 Implementation Status

### ✅ Completed

- **Go Server**: Full implementation with gRPC, HTTP API, PostgreSQL
- **Common Worker**: Go-based worker compatible with all server implementations
- **Loan Approval Workflow**: Complete 7-step workflow with conditions
- **Database Schema**: Optimized PostgreSQL schema for workflow tracking
- **Docker Support**: Complete containerization and orchestration
- **Testing Infrastructure**: Automated testing and validation scripts

### 🚧 Planned

- **Java Vert.x Server**: Reactive, async gRPC implementation
- **Kotlin Coroutine Server**: Coroutine-based streaming implementation
- **Performance Benchmarks**: Comprehensive performance comparison
- **Load Testing**: Stress testing with multiple concurrent workflows

## 🚀 Quick Start

### Prerequisites

- Docker & Docker Compose
- Go 1.21+ (for building from source)
- Protocol Buffers compiler

### 1. Clone and Setup

```bash
git clone <repository>
cd grpc-benchmark/workflow-engine
```

### 2. Start the Complete System

```bash
./test-system.sh
```

This script will:

- Start PostgreSQL database
- Start Go workflow server
- Start the worker
- Run comprehensive tests
- Keep the system running for manual testing

### 3. Manual Testing

```bash
# Start a workflow
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
          "aadhaar": "123456789012"
        }
      }
    }
  }'

# Check workflow status
curl http://localhost:8080/api/v1/workflows/{workflow_id}

# Check system health
curl http://localhost:8080/health
```

## 📊 Workflow Implementation

### Loan Approval Workflow

```
PostLoanApplication → PanVerification → AadhaarVerification →
BureauPull → FinalDecision → UpdateStatus → SendCallback
```

#### Step Details

1. **PostLoanApplication**: Validates application data and basic requirements
2. **PanVerification**: Verifies PAN number with external service (90% success rate)
3. **AadhaarVerification**: Verifies Aadhaar number (85% success rate)
4. **BureauPull**: Pulls credit bureau data and generates score (300-850)
5. **FinalDecision**: Approves/rejects based on all verifications + credit score ≥650
6. **UpdateStatus**: Updates application status in the system
7. **SendCallback**: Sends final result to requesting system

Each step includes:

- **Pending State**: Reported when step starts
- **Success/Failure State**: Reported when step completes
- **Error Handling**: Graceful failure with appropriate callbacks
- **Variable Management**: In-memory state with persistent storage

## 🛠️ Technology Stack

### Go Implementation

- **Framework**: Native Go with Gin for HTTP
- **gRPC**: google.golang.org/grpc v1.67.1
- **Database**: PostgreSQL with lib/pq driver
- **Features**:
  - Bidirectional streaming
  - Connection pooling
  - Structured logging
  - Docker containerization

### Java Implementation (Planned)

- **Framework**: Vert.x with reactive streams
- **gRPC**: grpc-java with reactive bindings
- **Database**: Reactive PostgreSQL client
- **Features**:
  - Event-driven architecture
  - Non-blocking I/O
  - Reactive streams

### Kotlin Implementation (Planned)

- **Framework**: Ktor with coroutines
- **gRPC**: grpc-kotlin with coroutine support
- **Database**: Exposed ORM with coroutines
- **Features**:
  - Suspend functions
  - Flow-based streaming
  - Structured concurrency

## 📁 Project Structure

```
workflow-engine/
├── go/                     # Go server implementation
│   ├── cmd/server/         # Server entry point
│   ├── internal/           # Internal packages
│   │   ├── database/       # Database operations
│   │   ├── grpc/          # gRPC server
│   │   ├── handlers/      # HTTP handlers
│   │   ├── models/        # Data models
│   │   └── config/        # Configuration
│   ├── proto/             # Protocol buffers
│   ├── scripts/           # Database scripts
│   └── docker-compose.yml # Development setup
├── worker/                # Common worker implementation
│   ├── cmd/worker/        # Worker entry point
│   ├── internal/          # Internal packages
│   │   ├── engine/        # Workflow engine
│   │   ├── grpc/         # gRPC client
│   │   └── config/       # Configuration
│   └── proto/            # Protocol buffers
├── java/                 # Java Vert.x implementation (planned)
├── kotlin/               # Kotlin implementation (planned)
└── test-system.sh        # Integration test script
```

## 🔧 Development

### Go Server

```bash
cd go
make dev          # Setup development environment
make docker-run   # Run with Docker
make build        # Build binary
make test         # Run tests
```

### Worker

```bash
cd worker
make dev          # Setup development environment
make run          # Run worker
make build        # Build binary
make test         # Run tests
```

## 📈 Performance Metrics

The system tracks:

- **Workflow Execution Time**: End-to-end processing time
- **Step Processing Time**: Individual step execution time
- **Connection Management**: Streaming connection lifecycle
- **Database Performance**: Query execution times
- **Throughput**: Concurrent workflow processing capacity
- **Resource Usage**: Memory and CPU utilization

## 🌐 API Documentation

### REST Endpoints

#### Start Workflow

```http
POST /api/v1/workflows/start
Content-Type: application/json

{
  "workflow_name": "loan_approval",
  "payload": { ... }
}
```

#### Get Workflow Status

```http
GET /api/v1/workflows/{id}
```

#### Health Check

```http
GET /health
```

#### Active Connections

```http
GET /api/v1/connections
```

### gRPC Services

#### RegisterEndpoint

```protobuf
rpc RegisterEndpoint(RegisterEndpointRequest) returns (RegisterEndpointResponse);
```

#### WorkflowStream

```protobuf
rpc WorkflowStream(stream WorkflowStreamRequest) returns (stream WorkflowStreamResponse);
```

## 🔍 Monitoring

### Logs

- Structured JSON logging
- Request/response tracking
- Performance metrics
- Error reporting

### Health Checks

- Server health endpoint
- Database connectivity
- Active connection monitoring
- Worker registration status

### Metrics (Planned)

- Prometheus metrics export
- Grafana dashboards
- Performance benchmarks
- Load testing results

## 🎯 Benchmarking Plan

### Performance Tests

1. **Single Workflow Latency**: Measure end-to-end execution time
2. **Concurrent Workflows**: Test with 100, 1000, 10000 concurrent workflows
3. **Streaming Performance**: Measure gRPC streaming overhead
4. **Database Performance**: Query execution and connection pooling
5. **Memory Usage**: Memory consumption under load
6. **CPU Utilization**: Processing efficiency

### Comparison Metrics

- **Throughput**: Workflows per second
- **Latency**: P50, P95, P99 response times
- **Resource Usage**: Memory and CPU consumption
- **Scalability**: Performance under increasing load
- **Reliability**: Error rates and recovery times

## 🛡️ Production Considerations

### Security

- TLS for gRPC connections
- Authentication and authorization
- Input validation and sanitization
- SQL injection prevention

### Scalability

- Horizontal scaling support
- Load balancing strategies
- Database connection pooling
- Caching mechanisms

### Reliability

- Circuit breaker patterns
- Retry mechanisms
- Health checks and monitoring
- Graceful degradation

## 🤝 Contributing

1. Fork the repository
2. Create a feature branch
3. Implement your changes
4. Add tests and documentation
5. Submit a pull request

## 📝 License

This project is licensed under the MIT License - see the LICENSE file for details.

## 🙏 Acknowledgments

- gRPC community for excellent streaming support
- Go community for performance optimization insights
- PostgreSQL team for robust database features
- Docker for simplified development workflows
