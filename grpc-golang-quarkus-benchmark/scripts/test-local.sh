#!/bin/bash

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Testing gRPC services locally (without containers)${NC}"

# Set working directory
cd "$(dirname "$0")/.."

# Test Golang service
echo -e "${YELLOW}Testing Golang service...${NC}"
cd golang

# Check if binary exists
if [ ! -f "bin/server" ]; then
    echo -e "${RED}Golang server binary not found. Building...${NC}"
    go build -o bin/server ./cmd/server
fi

if [ ! -f "bin/client" ]; then
    echo -e "${RED}Golang client binary not found. Building...${NC}"
    go build -o bin/client ./cmd/client
fi

# Start Golang server in background
echo -e "${YELLOW}Starting Golang server...${NC}"
export PORT=8080
export DB_URL="postgres://benchmark:benchmark123@localhost:5432/benchmark?sslmode=disable"
./bin/server &
GOLANG_PID=$!

# Wait for server to start
sleep 3

# Test Golang server
echo -e "${YELLOW}Testing Golang server health...${NC}"
if ./bin/client -server=localhost:8080 -operation=health -count=1; then
    echo -e "${GREEN}Golang server test passed!${NC}"
else
    echo -e "${RED}Golang server test failed!${NC}"
fi

# Stop Golang server
kill $GOLANG_PID 2>/dev/null || true

cd ..

# Test Quarkus service
echo -e "${YELLOW}Testing Quarkus service...${NC}"
cd quarkus

# Check if JAR exists
if [ ! -f "target/quarkus-app/quarkus-run.jar" ]; then
    echo -e "${RED}Quarkus JAR not found. Building...${NC}"
    mvn clean package -DskipTests
fi

# Start Quarkus server in background
echo -e "${YELLOW}Starting Quarkus server...${NC}"
java -Dquarkus.http.port=8081 -Dquarkus.grpc.server.port=9090 -jar target/quarkus-app/quarkus-run.jar &
QUARKUS_PID=$!

# Wait for server to start
sleep 5

# Test Quarkus server using curl for health check
echo -e "${YELLOW}Testing Quarkus server health...${NC}"
if curl -f http://localhost:8081/q/health 2>/dev/null; then
    echo -e "${GREEN}Quarkus server test passed!${NC}"
else
    echo -e "${RED}Quarkus server test failed!${NC}"
fi

# Stop Quarkus server
kill $QUARKUS_PID 2>/dev/null || true

cd ..

echo -e "${GREEN}Local testing completed!${NC}"
echo -e "${YELLOW}Note: For full testing with database, use Docker/Podman setup${NC}"
