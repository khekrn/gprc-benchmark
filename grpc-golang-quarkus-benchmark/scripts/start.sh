#!/bin/bash

# Start script for gRPC Golang vs Quarkus benchmark
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

echo -e "${GREEN}Starting gRPC Benchmark Services${NC}"

# Check if we're in the right directory
if [ ! -f "docker/docker-compose.yml" ]; then
    echo -e "${RED}Error: docker-compose.yml not found. Please run this script from the project root.${NC}"
    exit 1
fi

# Function to check if podman-compose is available
check_compose() {
    if command -v podman-compose >/dev/null 2>&1; then
        echo "podman-compose"
    elif command -v docker-compose >/dev/null 2>&1; then
        echo "docker-compose"
    else
        echo -e "${RED}Error: Neither podman-compose nor docker-compose found${NC}"
        exit 1
    fi
}

COMPOSE_CMD=$(check_compose)
echo -e "${BLUE}Using: $COMPOSE_CMD${NC}"

# Create necessary directories
mkdir -p bin benchmark-results

# Change to docker directory
cd docker

echo -e "${YELLOW}Starting PostgreSQL database...${NC}"
$COMPOSE_CMD up -d postgres

echo -e "${YELLOW}Waiting for PostgreSQL to be ready...${NC}"
sleep 10

echo -e "${YELLOW}Starting Prometheus and Grafana...${NC}"
$COMPOSE_CMD up -d prometheus grafana

echo -e "${YELLOW}Starting Golang gRPC server...${NC}"
$COMPOSE_CMD up -d golang-server

echo -e "${YELLOW}Starting Quarkus gRPC server...${NC}"
$COMPOSE_CMD up -d quarkus-server

echo -e "${YELLOW}Waiting for services to start...${NC}"
sleep 15

# Check service health
echo -e "${BLUE}Checking service health...${NC}"

# Check Golang server
if curl -s http://localhost:8080/metrics > /dev/null; then
    echo -e "${GREEN}✓ Golang server is healthy${NC}"
else
    echo -e "${RED}✗ Golang server health check failed${NC}"
fi

# Check Quarkus server
if curl -s http://localhost:8081/health > /dev/null; then
    echo -e "${GREEN}✓ Quarkus server is healthy${NC}"
else
    echo -e "${RED}✗ Quarkus server health check failed${NC}"
fi

# Check Prometheus
if curl -s http://localhost:9090/-/healthy > /dev/null; then
    echo -e "${GREEN}✓ Prometheus is healthy${NC}"
else
    echo -e "${RED}✗ Prometheus health check failed${NC}"
fi

# Check Grafana
if curl -s http://localhost:3000/api/health > /dev/null; then
    echo -e "${GREEN}✓ Grafana is healthy${NC}"
else
    echo -e "${RED}✗ Grafana health check failed${NC}"
fi

echo -e "${GREEN}All services started!${NC}"
echo ""
echo -e "${BLUE}Service URLs:${NC}"
echo "• Golang gRPC Server: localhost:50051 (metrics: http://localhost:8080/metrics)"
echo "• Quarkus gRPC Server: localhost:50052 (health: http://localhost:8081/health)"
echo "• Prometheus: http://localhost:9090"
echo "• Grafana: http://localhost:3000 (admin/admin)"
echo ""
echo -e "${YELLOW}Next steps:${NC}"
echo "1. Run benchmark: ../scripts/benchmark.sh"
echo "2. View logs: $COMPOSE_CMD logs -f [service-name]"
echo "3. Stop services: ../scripts/stop.sh"
