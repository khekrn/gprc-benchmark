#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting gRPC Benchmark Build Process (with certificate fix)${NC}"

# Set working directory
cd "$(dirname "$0")/.."

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
echo -e "${GREEN}Protobuf files generated!${NC}"

# Build Golang service
echo -e "${YELLOW}Building Golang service...${NC}"
cd golang
mkdir -p bin
go mod tidy
go build -o bin/server ./cmd/server
go build -o bin/client ./cmd/client
cd ..

echo -e "${GREEN}Golang service built successfully!${NC}"

# Build Quarkus service
echo -e "${YELLOW}Building Quarkus service...${NC}"
cd quarkus
mvn clean package -DskipTests
cd ..

echo -e "${GREEN}Quarkus service built successfully!${NC}"

# Build Docker images with certificate fix
echo -e "${YELLOW}Building Docker images (with --tls-verify=false)...${NC}"

# Build Golang image
echo -e "${YELLOW}Building Golang Docker image...${NC}"
podman build --tls-verify=false -f docker/Dockerfile.golang -t benchmark-golang:latest .
echo -e "${GREEN}Golang Docker image built!${NC}"

# Build Quarkus image
echo -e "${YELLOW}Building Quarkus Docker image...${NC}"
podman build --tls-verify=false -f docker/Dockerfile.quarkus -t benchmark-quarkus:latest .
echo -e "${GREEN}Quarkus Docker image built!${NC}"

echo -e "${GREEN}Build process completed successfully!${NC}"
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Start the services: ./scripts/start.sh"
echo "2. Run benchmark: ./scripts/benchmark.sh"
echo "3. View results: open http://localhost:3000 (Grafana)"
echo ""
echo -e "${YELLOW}Note: Database setup:${NC}"
echo "podman run -d --name postgres-benchmark \\"
echo "  -e POSTGRES_USER=benchmark \\"
echo "  -e POSTGRES_PASSWORD=benchmark123 \\"
echo "  -e POSTGRES_DB=benchmark \\"
echo "  -p 5432:5432 \\"
echo "  --tls-verify=false docker.io/library/postgres:15"
