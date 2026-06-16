# gRPC Golang vs Quarkus Benchmark

A comprehensive benchmarking suite comparing gRPC performance between Golang and Quarkus implementations.

## ✅ Project Status: COMPLETE

All components have been successfully built and tested:

- ✅ Protobuf generation with buf.build v2
- ✅ Golang gRPC service with pgx database integration
- ✅ Quarkus gRPC service with Hibernate Reactive Panache
- ✅ Docker containers with resource limits (2 CPU, 512MB)
- ✅ Prometheus metrics and Grafana monitoring
- ✅ Automated benchmark scripts

## 🚀 Quick Start

### Prerequisites

```bash
# Install dependencies
brew install go maven podman buf
```

### Certificate Fix for Podman (macOS)

```bash
# If you encounter TLS certificate errors:
podman pull --tls-verify=false docker.io/library/alpine:latest
# Or use the fixed build script: ./scripts/build-fixed.sh
```

### Setup Database

```bash
# Start PostgreSQL
podman run -d --name postgres-benchmark \
  -e POSTGRES_USER=benchmark \
  -e POSTGRES_PASSWORD=benchmark123 \
  -e POSTGRES_DB=benchmark \
  -p 5432:5432 \
  --tls-verify=false docker.io/library/postgres:15
```

### Build and Run

```bash
# Option 1: Build with certificate fix
./scripts/build-fixed.sh

# Option 2: Run end-to-end test
./scripts/e2e-test.sh

# Start all services
./scripts/start.sh

# Run benchmarks
./scripts/benchmark.sh
```

## 📊 Monitoring

- **Grafana Dashboard**: http://localhost:3000 (admin/admin)
- **Prometheus**: http://localhost:9090
- **Golang Metrics**: http://localhost:8080/metrics
- **Quarkus Metrics**: http://localhost:8081/metrics

## 🏗️ Architecture

### Components

1. **Golang Service** (Port 50051)

   - Built with Go 1.21 and gRPC-Go v1.60+
   - PostgreSQL integration via pgx
   - Prometheus metrics collection
   - 4KB payload handling

2. **Quarkus Service** (Port 50052)

   - Built with Java 21 and Quarkus 3.5
   - Hibernate Reactive Panache for database
   - Micrometer metrics integration
   - Native compilation ready

3. **Database**

   - PostgreSQL 15 with optimized schema
   - Connection pooling and prepared statements
   - Benchmark data persistence

4. **Monitoring Stack**
   - Prometheus for metrics collection
   - Grafana for visualization
   - Custom dashboards for performance analysis

### Benchmark Features

- **Unary RPCs**: Single request-response pattern
- **Bidirectional Streaming**: Real-time data exchange
- **4KB Payloads**: Realistic message sizes
- **Database Integration**: Persistent storage testing
- **Resource Constraints**: 2 CPU cores, 512MB memory
- **Metrics Collection**: Latency, throughput, resource usage

## 🔧 Troubleshooting

### Common Issues

1. **TLS Certificate Errors**

```bash
# Use --tls-verify=false flag or run:
./scripts/build-fixed.sh
```

2. **Database Connection Issues**

```bash
# Check if PostgreSQL is running:
podman ps | grep postgres-benchmark

# Check logs:
podman logs postgres-benchmark
```

3. **Port Conflicts**

```bash
# Check what's using ports:
lsof -i :50051  # Golang gRPC
lsof -i :50052  # Quarkus gRPC
lsof -i :5432   # PostgreSQL
```

### Health Checks

```bash
# Test Golang service
cd golang && ./bin/client -server=localhost:50051 -operation=health -count=1

# Test Quarkus service
curl http://localhost:8081/q/health

# Test database
podman exec postgres-benchmark pg_isready -U benchmark
```

## 📈 Benchmark Results

Results are stored in:

- `benchmark-results/` directory
- Grafana dashboards (live)
- Prometheus metrics (historical)

Key metrics measured:

- Request latency (p50, p95, p99)
- Throughput (requests/second)
- Resource utilization (CPU, memory)
- Database performance
- Error rates

## 🛠️ Development

### Project Structure

```
grpc-golang-quarkus-benchmark/
├── proto-src/           # Protocol buffer definitions
├── golang/              # Go implementation
├── quarkus/             # Quarkus implementation
├── docker/              # Container configurations
├── monitoring/          # Prometheus & Grafana configs
├── scripts/             # Automation scripts
└── benchmark-results/   # Test results
```

### Testing Scripts

- `./scripts/e2e-test.sh` - Comprehensive integration test
- `./scripts/test-local.sh` - Local service testing
- `./test-summary.sh` - Build status summary

### Build Scripts

- `./scripts/build.sh` - Standard build (may have cert issues)
- `./scripts/build-fixed.sh` - Build with certificate fix
- `./scripts/start.sh` - Start all services
- `./scripts/stop.sh` - Stop all services
- `./scripts/benchmark.sh` - Run performance tests

## 📝 Technical Details

### Protocol Buffers

- Generated using buf.build v2
- Supports 4KB message payloads
- Includes metadata and timing information
- Compatible with both Go and Java

### Performance Optimizations

- Connection pooling for database
- Prepared statements for queries
- Efficient protobuf serialization
- Resource-constrained testing environment

### Monitoring Integration

- Prometheus metrics for both services
- Custom Grafana dashboards
- Real-time performance visualization
- Historical data analysis

## 🔗 References

- [gRPC Documentation](https://grpc.io/docs/)
- [Protocol Buffers](https://developers.google.com/protocol-buffers)
- [buf.build](https://buf.build/)
- [Quarkus gRPC Guide](https://quarkus.io/guides/grpc)
- [Prometheus](https://prometheus.io/)
- [Grafana](https://grafana.com/)

---

**Status**: ✅ Ready for benchmarking
**Last Updated**: June 2025
**Tested On**: macOS with Podman
