#!/bin/bash

# Build script for gRPC Golang vs Quarkus benchmark
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting gRPC Benchmark Build Process${NC}"

# Check if we're in the right directory
if [ ! -f "buf.yaml" ]; then
    echo -e "${RED}Error: buf.yaml not found. Please run this script from the project root.${NC}"
    exit 1
fi

# Function to check if command exists
command_exists() {
    command -v "$1" >/dev/null 2>&1
}

# Check dependencies
echo -e "${YELLOW}Checking dependencies...${NC}"

if ! command_exists buf; then
    echo -e "${RED}Error: buf is not installed. Please install it from https://buf.build${NC}"
    exit 1
fi

if ! command_exists go; then
    echo -e "${RED}Error: Go is not installed. Please install Go 1.21+${NC}"
    exit 1
fi

if ! command_exists mvn; then
    echo -e "${RED}Error: Maven is not installed. Please install Maven 3.9+${NC}"
    exit 1
fi

if ! command_exists podman; then
    echo -e "${RED}Error: Podman is not installed. Please install Podman${NC}"
    exit 1
fi

echo -e "${GREEN}All dependencies found!${NC}"

# Generate protobuf files
echo -e "${YELLOW}Generating protobuf files...${NC}"
buf generate

# Build Golang service
echo -e "${YELLOW}Building Golang service...${NC}"
cd golang
go mod tidy
go build -o ../bin/golang-server ./cmd/server
go build -o ../bin/golang-client ./cmd/client
cd ..

echo -e "${GREEN}Golang service built successfully!${NC}"

# Build Quarkus service
echo -e "${YELLOW}Building Quarkus service...${NC}"
cd quarkus
mvn clean package -DskipTests
cd ..

echo -e "${GREEN}Quarkus service built successfully!${NC}"

# Build Docker images
echo -e "${YELLOW}Building Docker images...${NC}"

# Build Golang image
podman build -f docker/Dockerfile.golang -t benchmark-golang:latest .
echo -e "${GREEN}Golang Docker image built!${NC}"

# Build Quarkus image
podman build -f docker/Dockerfile.quarkus -t benchmark-quarkus:latest .
echo -e "${GREEN}Quarkus Docker image built!${NC}"

echo -e "${GREEN}Build process completed successfully!${NC}"
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Start the services: ./scripts/start.sh"
echo "2. Run benchmark: ./scripts/benchmark.sh"
echo "3. View results: open http://localhost:3000 (Grafana)"
