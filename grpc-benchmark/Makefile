# Load container runtime configuration
include .make.env

.PHONY: help build run stop clean test benchmark proto deps

# Default target
help: ## Show this help message
	@echo "gRPC Benchmark Project"
	@echo "Available targets:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "  %-15s %s\n", $$1, $$2}'

# Dependencies
deps: ## Install Go dependencies and tools
	go mod tidy
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
	go install github.com/bufbuild/buf/cmd/buf@latest

# Generate protobuf files
proto: ## Generate protobuf Go files
	@echo "Generating protobuf files..."
	protoc --go_out=proto --go_opt=paths=source_relative \
		--go-grpc_out=proto --go-grpc_opt=paths=source_relative \
		proto-src/benchmark.proto

# Build
build: proto ## Build server and client binaries
	@echo "Building server..."
	go build -o bin/server ./cmd/server
	@echo "Building client..."
	go build -o bin/client ./cmd/client

# Docker/Podman builds
build-images: ## Build Docker/Podman images
	@echo "Building server image..."
	$(CONTAINER_RUNTIME) build -f Dockerfile.server -t grpc-benchmark-server .
	@echo "Building client image..."
	$(CONTAINER_RUNTIME) build -f Dockerfile.client -t grpc-benchmark-client .

# Environment management
up: ## Start all services with Podman Compose
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) up -d

down: ## Stop all services
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) down

restart: down up ## Restart all services

logs: ## Show logs from all services
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) logs -f

logs-server: ## Show server logs
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) logs -f grpc-server

logs-postgres: ## Show PostgreSQL logs
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) logs -f postgres

# Database management
db-init: ## Initialize database schema
	@echo "Initializing database..."
	$(CONTAINER_RUNTIME) exec -i $$($(COMPOSE_CMD) -f $(COMPOSE_FILE) ps -q postgres) psql -U benchmark_user -d benchmark < internal/db/schema.sql

db-init-local: ## Initialize local database schema
	@echo "Initializing local database schema..."
	./scripts/init-db.sh

db-reset: ## Reset database (drop and recreate)
	@echo "Resetting database..."
	$(CONTAINER_RUNTIME) exec $$($(COMPOSE_CMD) -f $(COMPOSE_FILE) ps -q postgres) dropdb -U benchmark_user benchmark || true
	$(CONTAINER_RUNTIME) exec $$($(COMPOSE_CMD) -f $(COMPOSE_FILE) ps -q postgres) createdb -U benchmark_user benchmark
	make db-init

# Testing
test: ## Run Go tests
	go test -v ./...

test-race: ## Run Go tests with race detection
	go test -race -v ./...

validate: ## Validate complete system setup
	./scripts/validate.sh

# Benchmarking
benchmark-echo: ## Run echo benchmark
	go run ./cmd/client -server=localhost:8080 -test=echo -duration=30s -concurrency=10

benchmark-client-stream: ## Run client stream benchmark
	go run ./cmd/client -server=localhost:8080 -test=client-stream -duration=30s -concurrency=5

# Load Testing
load-test: ## Run comprehensive load test
	./scripts/load-test.sh

load-test-quick: ## Run quick load test (30s)
	./scripts/load-test.sh -d 30s -c 5

load-test-medium: ## Run medium load test (2 minutes, 20 clients)
	./scripts/load-test.sh -d 120s -c 20

load-test-heavy: ## Run heavy load test (5 minutes, 50 clients)
	./scripts/load-test.sh -d 300s -c 50

load-test-echo: ## Run load test for echo only
	./scripts/load-test.sh -t echo -d 60s -c 10

benchmark-server-stream: ## Run server stream benchmark
	go run ./cmd/client -server=localhost:8080 -test=server-stream -duration=30s -concurrency=5

benchmark-bidi-stream: ## Run bidirectional stream benchmark
	go run ./cmd/client -server=localhost:8080 -test=bidi-stream -duration=30s -concurrency=5

benchmark-large-data: ## Run large data stream benchmark
	go run ./cmd/client -server=localhost:8080 -test=large-data -duration=30s -concurrency=3

benchmark-all: ## Run all benchmarks
	go run ./cmd/client -server=localhost:8080 -test=all -duration=30s -concurrency=10

# Container benchmarking
benchmark-container: ## Run benchmark using container
	$(CONTAINER_RUNTIME) run --rm --network=host grpc-benchmark-client \
		-server=localhost:8080 -test=all -duration=30s -concurrency=10

# Performance testing with different loads
perf-test-light: ## Light performance test
	go run ./cmd/client -server=localhost:8080 -test=all -duration=60s -concurrency=5

perf-test-medium: ## Medium performance test
	go run ./cmd/client -server=localhost:8080 -test=all -duration=120s -concurrency=20

perf-test-heavy: ## Heavy performance test
	go run ./cmd/client -server=localhost:8080 -test=all -duration=300s -concurrency=50

# Monitoring
monitor: ## Open monitoring dashboards
	@echo "Prometheus: http://localhost:9090"
	@echo "Grafana: http://localhost:3000 (admin/admin)"

troubleshoot-metrics: ## Troubleshoot metrics collection issues
	./scripts/troubleshoot-metrics.sh

# Cleanup
clean: ## Clean up binaries and containers
	rm -rf bin/
	$(CONTAINER_RUNTIME) system prune -f

clean-all: down clean ## Clean everything including volumes
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) down -v
	$(CONTAINER_RUNTIME) volume prune -f
	$(CONTAINER_RUNTIME) image prune -f

# Development
dev-server: ## Run server in development mode
	go run ./cmd/server

dev-client: ## Run client in development mode
	go run ./cmd/client -help

# Health checks
health: ## Check service health
	@echo "Checking gRPC server health..."
	@curl -f http://localhost:8081/health || echo "Server not running"
	@echo "Checking Prometheus..."
	@curl -f http://localhost:9090/-/healthy || echo "Prometheus not running"
	@echo "Checking Grafana..."
	@curl -f http://localhost:3000/api/health || echo "Grafana not running"

check-server: ## Test gRPC server connectivity
	./scripts/check-server.sh

# Utilities
format: ## Format Go code
	go fmt ./...

lint: ## Lint Go code (requires golangci-lint)
	golangci-lint run

# Show project status
status: ## Show running containers and services
	$(COMPOSE_CMD) -f $(COMPOSE_FILE) ps
