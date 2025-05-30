# gRPC Benchmark Service

A comprehensive gRPC benchmark service implementation in Go with PostgreSQL database integration, Prometheus metrics, and Podman containerization support. This project is designed to benchmark various gRPC communication patterns and prepare for future Vert.x implementation.

## Features

- **gRPC Service Types**:
  - Unary RPC (Echo)
  - Client Streaming
  - Server Streaming  
  - Bidirectional Streaming
  - Large Data Transfer Streaming

- **Database Integration**:
  - PostgreSQL with pgx driver
  - Automatic schema creation
  - Performance metrics storage

- **Monitoring & Metrics**:
  - Prometheus metrics collection
  - Grafana dashboards
  - Request/response tracking
  - Latency measurements

- **Containerization**:
  - Podman/Docker support
  - Multi-service orchestration
  - Health checks

## Project Structure

```
.
├── buf.gen.yaml              # Buf protobuf generation config
├── buf.yaml                  # Buf main configuration
├── docker-compose.podman.yml # Podman compose file
├── Dockerfile.server         # Server container
├── Dockerfile.client         # Client container
├── Makefile                  # Build and management commands
├── go.mod                    # Go module definition
├── cmd/
│   ├── server/              # gRPC server implementation
│   └── client/              # Benchmark client
├── internal/
│   ├── db/                  # Database layer
│   │   ├── db.go           # Database operations
│   │   └── schema.sql      # Database schema
│   ├── metrics/            # Prometheus metrics
│   │   └── metrics.go      
│   └── service/            # gRPC service implementation
│       └── service.go      
├── proto/                   # Generated protobuf files
├── proto-src/              # Source protobuf definitions
│   └── benchmark.proto     
└── monitoring/             # Monitoring configuration
    ├── prometheus.yml      
    └── grafana/           
        ├── dashboards/    
        └── datasources/   
```

## Getting Started

### Prerequisites

- Go 1.23+
- Podman or Docker
- Make

### Installation

1. **Clone and setup**:
```bash
git clone <repository-url>
cd gprc-benchmark
```

2. **Install dependencies**:
```bash
make deps
```

3. **Generate protobuf files**:
```bash
make proto
```

4. **Build the project**:
```bash
make build
```

### Running with Podman

1. **Start all services**:
```bash
make up
```

This starts:
- PostgreSQL database (port 5432)
- Redis (port 6379)
- gRPC server (port 8080)
- Metrics server (port 8081)
- Prometheus (port 9090)
- Grafana (port 3000)

2. **Check service status**:
```bash
make status
make health
```

3. **View logs**:
```bash
make logs              # All services
make logs-server       # Server only
make logs-postgres     # Database only
```

### Running Benchmarks

#### Quick Benchmarks

```bash
# Run all benchmark types
make benchmark-all

# Individual benchmark types
make benchmark-echo            # Unary RPC
make benchmark-client-stream   # Client streaming
make benchmark-server-stream   # Server streaming
make benchmark-bidi-stream     # Bidirectional streaming
make benchmark-large-data      # Large data transfer
```

#### Custom Benchmarks

```bash
# Custom parameters
go run ./cmd/client \
  -server=localhost:8080 \
  -test=echo \
  -duration=60s \
  -concurrency=20 \
  -message-size=2048

# Using containers
make benchmark-container
```

#### Performance Testing

```bash
make perf-test-light    # 60s, 5 concurrent clients
make perf-test-medium   # 120s, 20 concurrent clients
make perf-test-heavy    # 300s, 50 concurrent clients
```

### Available Benchmark Options

| Parameter | Description | Default |
|-----------|-------------|---------|
| `-server` | gRPC server address | `localhost:8080` |
| `-test` | Test type (echo, client-stream, server-stream, bidi-stream, large-data, all) | `echo` |
| `-duration` | Test duration | `30s` |
| `-concurrency` | Number of concurrent clients | `10` |
| `-message-size` | Message size in bytes | `1024` |
| `-stream-count` | Messages per stream | `100` |
| `-chunk-size` | Chunk size for large data | `8192` |

### Database Operations

```bash
# Initialize database schema
make db-init

# Reset database (drop and recreate)
make db-reset
```

### Monitoring

Access the monitoring dashboards:

- **Prometheus**: http://localhost:9090
- **Grafana**: http://localhost:3000 (admin/admin)

```bash
# Open monitoring URLs
make monitor
```

## gRPC Service Details

### 1. Echo (Unary RPC)
Simple request-response pattern for baseline performance measurement.

### 2. Client Streaming
Multiple client requests followed by single server response. Useful for batch operations.

### 3. Server Streaming
Single client request followed by multiple server responses. Good for data feeds.

### 4. Bidirectional Streaming
Concurrent sending and receiving. Simulates real-time communication.

### 5. Large Data Streaming
File transfer simulation with chunked data. Tests throughput with large payloads.

## Metrics Collected

### Request Metrics
- Total requests by method and status
- Request duration histograms
- Active stream counts
- Concurrent request tracking

### Database Metrics
- Database operation counts and duration
- Connection pool metrics
- Query performance

### Custom Metrics
- Message processing counts by type and priority
- Message size distributions
- Queue time measurements
- Throughput calculations

## Environment Configuration

Key environment variables (see `.env`):

```bash
# Server Configuration
SERVER_PORT=8080
METRICS_PORT=8081
LOG_LEVEL=info

# Database Configuration
DATABASE_URL=postgres://benchmark_user:benchmark_pass@localhost:5432/benchmark?sslmode=disable
DB_MAX_CONNECTIONS=25
DB_MIN_CONNECTIONS=5

# gRPC Configuration
MAX_CONCURRENT_STREAMS=1000
KEEP_ALIVE=30s
KEEP_ALIVE_TIMEOUT=5s
```

## Development

### Local Development

```bash
# Run server locally
make dev-server

# Run client locally
make dev-client

# Format code
make format

# Run tests
make test
make test-race
```

### Building for Production

```bash
# Build binaries
make build

# Build container images
make build-images
```

## Preparation for Vert.x Implementation

This Go implementation serves as a reference for the future Vert.x implementation:

### Architectural Patterns
- Service layer separation
- Database abstraction
- Metrics collection patterns
- Configuration management

### Performance Baselines
- Latency measurements
- Throughput benchmarks
- Resource utilization patterns
- Scaling characteristics

### Testing Strategies
- Load testing patterns
- Stress testing scenarios
- Performance regression detection

## Database Schema

The service creates the following tables:

### `benchmark_records`
Stores metadata for each gRPC request:
- `record_id`: Unique identifier
- `request_type`: Type of gRPC call
- `user_id`, `session_id`: Request context
- `payload`: Request payload
- `metadata`: Additional metadata (JSONB)
- `processing_time_ns`: Processing duration
- `concurrent_requests`: Concurrent request count

### `data_chunks`
Stores large data transfer information:
- `record_id`: Links to benchmark_records
- `chunk_id`: Chunk sequence number
- `filename`: Original filename
- `data_size`: Chunk size
- `total_chunks`: Total expected chunks

## Troubleshooting

### Common Issues

1. **gRPC Server connectivity issues**:
```bash
# Check if server is responding
make check-server
# Or run directly
./scripts/check-server.sh

# Check if server process is running
ps aux | grep server

# Check if port is occupied
lsof -i :8080

# Start server if not running
./bin/server
# or
go run ./cmd/server
```

2. **Port conflicts**:
```bash
# Check what's using the ports
lsof -i :8080
lsof -i :8081
# Or change ports in .env file
```

3. **Database connection issues**:
```bash
# Test database connection
psql -h localhost -p 5432 -U postgres -d proddb -c "\l"

# Initialize schema if needed
make db-init-local

# Check PostgreSQL is running
brew services list | grep postgresql
# Start if needed
brew services start postgresql
```

4. **Load test failures**:
```bash
# Validate complete setup
make validate

# Run with verbose output
./scripts/load-test.sh -v -d 10s -c 2

# Check database records after test
psql -h localhost -p 5432 -U postgres -d proddb -c "SELECT COUNT(*) FROM benchmark.benchmark_records;"
```

5. **Container issues**:
```bash
# Check container status
make status
# Clean up and restart
make clean-all && make up
```

### Performance Tuning

1. **Database connections**: Adjust `DB_MAX_CONNECTIONS` based on load
2. **gRPC settings**: Tune `MAX_CONCURRENT_STREAMS` for your use case
3. **Message sizes**: Optimize based on your data patterns

## Contributing

1. Follow Go best practices
2. Add tests for new features
3. Update documentation
4. Ensure compatibility with future Vert.x implementation

## License

[Add your license information here]

## Future Vert.x Implementation Notes

When implementing in Vert.x:

1. **Reactive Patterns**: Use Vert.x reactive streams equivalent to Go channels
2. **Database**: Consider using Vert.x PostgreSQL reactive client
3. **Metrics**: Use Micrometer metrics (equivalent to Prometheus client)
4. **Configuration**: Use Vert.x config management
5. **Deployment**: Consider Vert.x specific containerization patterns

The current Go implementation provides a solid foundation for understanding performance characteristics and implementation patterns that will translate well to Vert.x.
