#!/bin/bash

# Load testing script for gRPC benchmark
set -e

# Default values
SERVER_HOST="localhost:8080"
TEST_DURATION="60s"
CONCURRENCY=10
TEST_TYPE="all"
VERBOSE=false

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_info() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to show usage
show_usage() {
    echo "Usage: $0 [OPTIONS]"
    echo ""
    echo "Options:"
    echo "  -h, --help              Show this help message"
    echo "  -s, --server HOST:PORT  gRPC server address (default: localhost:8080)"
    echo "  -d, --duration DURATION Test duration (default: 60s)"
    echo "  -c, --concurrency NUM   Number of concurrent clients (default: 10)"
    echo "  -t, --test TYPE         Test type: echo|client-stream|server-stream|bidi-stream|large-data|all (default: all)"
    echo "  -v, --verbose           Verbose output"
    echo ""
    echo "Examples:"
    echo "  $0                                          # Run all tests with default settings"
    echo "  $0 -t echo -c 20 -d 120s                   # Run echo test with 20 clients for 2 minutes"
    echo "  $0 -s localhost:9090 -t bidi-stream        # Run bidirectional stream test on port 9090"
}

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -h|--help)
            show_usage
            exit 0
            ;;
        -s|--server)
            SERVER_HOST="$2"
            shift 2
            ;;
        -d|--duration)
            TEST_DURATION="$2"
            shift 2
            ;;
        -c|--concurrency)
            CONCURRENCY="$2"
            shift 2
            ;;
        -t|--test)
            TEST_TYPE="$2"
            shift 2
            ;;
        -v|--verbose)
            VERBOSE=true
            shift
            ;;
        *)
            print_error "Unknown option: $1"
            show_usage
            exit 1
            ;;
    esac
done

# Function to check if server is running
check_server() {
    print_info "Checking if gRPC server is running on $SERVER_HOST..."
    
    # Build client if needed
    if [ ! -f "./bin/client" ]; then
        print_info "Building client binary..."
        if ! make build >/dev/null 2>&1; then
            print_error "Failed to build client binary"
            return 1
        fi
    fi
    
    # Use the same test as check-server.sh
    print_info "Testing gRPC connectivity..."
    if ./bin/client -server=$SERVER_HOST -test=echo -duration=1s -concurrency=1 >/dev/null 2>&1; then
        print_success "Server is running and responding on $SERVER_HOST"
        return 0
    else
        print_error "gRPC test failed"
        print_info "Please ensure the server is running and accessible:"
        print_info "  ./bin/server"
        print_info "  # or"
        print_info "  go run ./cmd/server"
        print_info ""
        print_info "You can also test manually with:"
        print_info "  make check-server"
        return 1
    fi
}

# Function to check database connection
check_database() {
    print_info "Checking database connection..."
    
    # Load environment variables
    if [ -f .env ]; then
        source .env
    else
        print_error ".env file not found"
        return 1
    fi
    
    # Extract database details from DATABASE_URL
    if [ -z "$DATABASE_URL" ]; then
        print_error "DATABASE_URL not set in .env file"
        return 1
    fi
    
    # Simple check - try to connect using psql
    DB_HOST=$(echo $DATABASE_URL | sed -n 's/.*@\([^:]*\):.*/\1/p')
    DB_PORT=$(echo $DATABASE_URL | sed -n 's/.*:\([0-9]*\)\/.*/\1/p')
    DB_USER=$(echo $DATABASE_URL | sed -n 's/.*:\/\/\([^:]*\):.*/\1/p')
    DB_NAME=$(echo $DATABASE_URL | sed -n 's/.*\/\([^?]*\).*/\1/p')
    
    if pg_isready -h $DB_HOST -p $DB_PORT -U $DB_USER >/dev/null 2>&1; then
        print_success "Database is accessible"
        return 0
    else
        print_error "Cannot connect to database"
        print_info "Please ensure PostgreSQL is running and the 'proddb' database exists"
        return 1
    fi
}

# Function to run a specific test
run_test() {
    local test_type=$1
    local test_name=$2
    
    print_info "Running $test_name test..."
    print_info "  Server: $SERVER_HOST"
    print_info "  Duration: $TEST_DURATION"
    print_info "  Concurrency: $CONCURRENCY"
    
    local cmd="./bin/client -server=$SERVER_HOST -test=$test_type -duration=$TEST_DURATION -concurrency=$CONCURRENCY"
    
    if [ "$VERBOSE" = true ]; then
        print_info "Command: $cmd"
    fi
    
    echo "----------------------------------------"
    if $cmd; then
        print_success "$test_name test completed successfully"
    else
        print_error "$test_name test failed"
        return 1
    fi
    echo "----------------------------------------"
    echo ""
}

# Function to run all tests
run_all_tests() {
    print_info "Running comprehensive load test suite..."
    
    # Run different test types
    # run_test "echo" "Echo (Unary)"
    # run_test "client-stream" "Client Streaming"
    # run_test "server-stream" "Server Streaming"
    run_test "bidi-stream" "Bidirectional Streaming"
    # run_test "large-data" "Large Data Streaming"
    
    print_success "All tests completed!"
}

# Function to show system info
show_system_info() {
    print_info "System Information:"
    echo "  OS: $(uname -s) $(uname -r)"
    echo "  Go version: $(go version 2>/dev/null || echo 'Go not found')"
    echo "  gRPC Benchmark version: $(git describe --tags 2>/dev/null || echo 'development')"
    echo ""
}

# Main execution
main() {
    print_info "gRPC Benchmark Load Testing"
    print_info "============================"
    echo ""
    
    if [ "$VERBOSE" = true ]; then
        show_system_info
    fi
    
    # Pre-flight checks
    if ! check_server; then
        exit 1
    fi
    
    if ! check_database; then
        exit 1
    fi
    
    # Build client if needed
    if [ ! -f "./bin/client" ]; then
        print_info "Building client binary..."
        make build
    fi
    
    # Run tests based on type
    case $TEST_TYPE in
        "all")
            run_all_tests
            ;;
        "echo"|"client-stream"|"server-stream"|"bidi-stream"|"large-data")
            run_test "$TEST_TYPE" "$(echo $TEST_TYPE | sed 's/-/ /g' | awk '{for(i=1;i<=NF;i++){$i=toupper(substr($i,1,1)) substr($i,2)}} 1')"
            ;;
        *)
            print_error "Invalid test type: $TEST_TYPE"
            print_info "Valid types: echo, client-stream, server-stream, bidi-stream, large-data, all"
            exit 1
            ;;
    esac
    
    print_success "Load testing completed successfully!"
    print_info "Check database for recorded metrics and Prometheus for performance data"
}

# Run main function
main "$@"
