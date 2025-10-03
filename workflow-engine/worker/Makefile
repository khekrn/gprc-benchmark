.PHONY: build run test clean proto deps

# Variables
BINARY_NAME=workflow-worker

# Build the application
build:
	go build -o bin/$(BINARY_NAME) ./cmd/worker

# Run the application
run:
	go run ./cmd/worker

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

# Development setup
dev-setup: deps proto build

# Format code
fmt:
	go fmt ./...

# Run linter
lint:
	golangci-lint run

# Full development cycle
dev: dev-setup fmt test

help:
	@echo "Available commands:"
	@echo "  build        - Build the application"
	@echo "  run          - Run the application"
	@echo "  test         - Run tests"
	@echo "  clean        - Clean build artifacts"
	@echo "  proto        - Generate protobuf files"
	@echo "  deps         - Download dependencies"
	@echo "  dev-setup    - Setup development environment"
	@echo "  fmt          - Format code"
	@echo "  lint         - Run linter"
	@echo "  dev          - Full development cycle"
	@echo "  help         - Show this help"