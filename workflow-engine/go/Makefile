.PHONY: build run test clean proto docker-build docker-run deps

# Variables
BINARY_NAME=workflow-engine
DOCKER_IMAGE=workflow-engine:latest

# Build the application
build:
	go build -o bin/$(BINARY_NAME) ./cmd/server

# Run the application
run:
	go run ./cmd/server

# Run tests
test:
	go test -v ./...

# Clean build artifacts
clean:
	rm -rf bin/
	go clean

# Generate protobuf files
proto:
	protoc --go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/workflow.proto

# Download dependencies
deps:
	go mod download
	go mod tidy

# Build Docker image
docker-build:
	docker build -t $(DOCKER_IMAGE) .

# Run with Docker Compose
docker-run:
	docker-compose up --build

# Stop Docker Compose
docker-stop:
	docker-compose down

# Run database migrations
migrate-up:
	docker-compose exec postgres psql -U postgres -d workflow_engine -f /docker-entrypoint-initdb.d/init.sql

# Development setup
dev-setup: deps proto build

# Check for required tools
check-tools:
	@which protoc >/dev/null || (echo "protoc is required" && exit 1)
	@which docker >/dev/null || (echo "docker is required" && exit 1)
	@which docker-compose >/dev/null || (echo "docker-compose is required" && exit 1)

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Full development cycle
dev: check-tools dev-setup fmt test

help:
	@echo "Available commands:"
	@echo "  build        - Build the application"
	@echo "  run          - Run the application"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  proto        - Generate protobuf files"
	@echo "  deps         - Download dependencies"
	@echo "  docker-build - Build Docker image"
	@echo "  docker-run   - Run with Docker Compose"
	@echo "  docker-stop  - Stop Docker Compose"
	@echo "  dev-setup    - Setup development environment"
	@echo "  check-tools  - Check for required tools"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  dev          - Full development cycle"
	@echo "  help         - Show this help"