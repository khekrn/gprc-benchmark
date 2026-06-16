#!/bin/bash

# Stop script for gRPC Golang vs Quarkus benchmark
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${YELLOW}Stopping gRPC Benchmark Services${NC}"

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

# Change to docker directory
cd docker

echo -e "${YELLOW}Stopping all services...${NC}"
$COMPOSE_CMD down

echo -e "${YELLOW}Removing unused containers and networks...${NC}"
$COMPOSE_CMD down --remove-orphans

# Optional: Remove volumes (uncomment if you want to reset data)
# echo -e "${YELLOW}Removing volumes...${NC}"
# $COMPOSE_CMD down -v

echo -e "${GREEN}All services stopped successfully!${NC}"
