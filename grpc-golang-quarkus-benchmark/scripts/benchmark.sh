#!/bin/bash

# Comprehensive benchmark script for gRPC Golang vs Quarkus
set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

# Default values
DURATION="60s"
CONCURRENCY=50
UNARY_RPS=100
STREAMING_RPS=50
WARMUP_TIME=30
SAVE_TO_DB=true
OUTPUT_DIR="benchmark-results"

# Parse command line arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        -d|--duration)
            DURATION="$2"
            shift 2
            ;;
        -c|--concurrency)
            CONCURRENCY="$2"
            shift 2
            ;;
        --unary-rps)
            UNARY_RPS="$2"
            shift 2
            ;;
        --streaming-rps)
            STREAMING_RPS="$2"
            shift 2
            ;;
        --warmup)
            WARMUP_TIME="$2"
            shift 2
            ;;
        --no-db)
            SAVE_TO_DB=false
            shift
            ;;
        -o|--output)
            OUTPUT_DIR="$2"
            shift 2
            ;;
        -h|--help)
            echo "Usage: $0 [options]"
            echo "Options:"
            echo "  -d, --duration DURATION     Benchmark duration (default: 60s)"
            echo "  -c, --concurrency NUM        Concurrent clients (default: 50)"
            echo "  --unary-rps NUM             Unary requests per second (default: 100)"
            echo "  --streaming-rps NUM         Streaming requests per second (default: 50)"
            echo "  --warmup SECONDS            Warmup time in seconds (default: 30)"
            echo "  --no-db                     Don't save to database"
            echo "  -o, --output DIR            Output directory (default: benchmark-results)"
            echo "  -h, --help                  Show this help"
            exit 0
            ;;
        *)
            echo -e "${RED}Unknown option: $1${NC}"
            exit 1
            ;;
    esac
done

echo -e "${GREEN}Starting Comprehensive gRPC Benchmark${NC}"
echo -e "${BLUE}Configuration:${NC}"
echo "  Duration: $DURATION"
echo "  Concurrency: $CONCURRENCY"
echo "  Unary RPS: $UNARY_RPS"
echo "  Streaming RPS: $STREAMING_RPS"
echo "  Warmup Time: ${WARMUP_TIME}s"
echo "  Save to DB: $SAVE_TO_DB"
echo "  Output Directory: $OUTPUT_DIR"
echo ""

# Create output directory
mkdir -p "$OUTPUT_DIR"
TIMESTAMP=$(date +"%Y%m%d_%H%M%S")
RESULT_FILE="$OUTPUT_DIR/benchmark_${TIMESTAMP}.json"
LOG_FILE="$OUTPUT_DIR/benchmark_${TIMESTAMP}.log"

# Function to log with timestamp
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" | tee -a "$LOG_FILE"
}

# Function to check if services are healthy
check_services() {
    log "Checking service health..."
    
    # Check Golang server
    if ! curl -s http://localhost:8080/metrics > /dev/null; then
        echo -e "${RED}Error: Golang server is not responding${NC}"
        exit 1
    fi
    
    # Check Quarkus server
    if ! curl -s http://localhost:8081/health > /dev/null; then
        echo -e "${RED}Error: Quarkus server is not responding${NC}"
        exit 1
    fi
    
    log "All services are healthy!"
}

# Function to run benchmark against a specific server
run_benchmark() {
    local server_type=$1
    local server_addr=$2
    local result_prefix=$3
    
    log "Running benchmark against $server_type server ($server_addr)..."
    
    # Warmup phase
    if [ $WARMUP_TIME -gt 0 ]; then
        log "Warming up $server_type server for ${WARMUP_TIME}s..."
        timeout ${WARMUP_TIME}s ./bin/golang-client \
            -addr="$server_addr" \
            -c=10 \
            -d="${WARMUP_TIME}s" \
            -unary-rps=50 \
            -streaming-rps=25 \
            -target="$server_type" \
            -save-db=false \
            > "$OUTPUT_DIR/${result_prefix}_warmup.log" 2>&1 || true
        log "Warmup completed for $server_type"
    fi
    
    # Main benchmark - Unary only
    log "Running Unary benchmark against $server_type..."
    ./bin/golang-client \
        -addr="$server_addr" \
        -c="$CONCURRENCY" \
        -d="$DURATION" \
        -unary-rps="$UNARY_RPS" \
        -streaming-rps=0 \
        -target="$server_type" \
        -save-db="$SAVE_TO_DB" \
        > "$OUTPUT_DIR/${result_prefix}_unary.log" 2>&1
    
    # Main benchmark - Streaming only
    log "Running Streaming benchmark against $server_type..."
    ./bin/golang-client \
        -addr="$server_addr" \
        -c="$CONCURRENCY" \
        -d="$DURATION" \
        -unary-rps=0 \
        -streaming-rps="$STREAMING_RPS" \
        -target="$server_type" \
        -save-db="$SAVE_TO_DB" \
        > "$OUTPUT_DIR/${result_prefix}_streaming.log" 2>&1
    
    # Combined benchmark
    log "Running Combined benchmark against $server_type..."
    ./bin/golang-client \
        -addr="$server_addr" \
        -c="$CONCURRENCY" \
        -d="$DURATION" \
        -unary-rps="$UNARY_RPS" \
        -streaming-rps="$STREAMING_RPS" \
        -target="$server_type" \
        -save-db="$SAVE_TO_DB" \
        > "$OUTPUT_DIR/${result_prefix}_combined.log" 2>&1
    
    log "Completed benchmark against $server_type"
}

# Function to collect system metrics
collect_system_metrics() {
    local output_file=$1
    log "Collecting system metrics..."
    
    {
        echo "=== System Information ==="
        echo "Timestamp: $(date)"
        echo "CPU Info:"
        sysctl -n machdep.cpu.brand_string
        echo "CPU Cores: $(sysctl -n hw.ncpu)"
        echo "Memory: $(sysctl -n hw.memsize | awk '{print $1/1024/1024/1024 " GB"}')"
        echo ""
        
        echo "=== Docker/Podman Container Stats ==="
        if command -v podman >/dev/null 2>&1; then
            podman stats --no-stream --format "table {{.Container}}\t{{.CPUPerc}}\t{{.MemUsage}}\t{{.NetIO}}\t{{.BlockIO}}" || true
        fi
        echo ""
        
        echo "=== Prometheus Metrics Sample ==="
        echo "Golang Server Metrics:"
        curl -s http://localhost:8080/metrics | grep -E "(grpc_requests_total|grpc_request_duration|processing_latency)" | head -10 || true
        echo ""
        echo "Quarkus Server Metrics:"
        curl -s http://localhost:8081/metrics | grep -E "(grpc_requests_total|grpc_request_duration|processing_latency)" | head -10 || true
        echo ""
        
    } > "$output_file"
}

# Function to parse and summarize results
summarize_results() {
    log "Generating benchmark summary..."
    
    local summary_file="$OUTPUT_DIR/summary_${TIMESTAMP}.txt"
    
    {
        echo "==============================================="
        echo "    gRPC Benchmark Results Summary"
        echo "==============================================="
        echo "Timestamp: $(date)"
        echo "Duration: $DURATION"
        echo "Concurrency: $CONCURRENCY"
        echo "Unary RPS: $UNARY_RPS"
        echo "Streaming RPS: $STREAMING_RPS"
        echo ""
        
        echo "=== Golang Server Results ==="
        if [ -f "$OUTPUT_DIR/golang_unary.log" ]; then
            echo "Unary Results:"
            grep -E "(Total Requests|Throughput|Average Latency|P95 Latency|Error Rate)" "$OUTPUT_DIR/golang_unary.log" || echo "No results found"
            echo ""
        fi
        
        if [ -f "$OUTPUT_DIR/golang_streaming.log" ]; then
            echo "Streaming Results:"
            grep -E "(Total Requests|Throughput|Average Latency|P95 Latency|Error Rate)" "$OUTPUT_DIR/golang_streaming.log" || echo "No results found"
            echo ""
        fi
        
        echo "=== Quarkus Server Results ==="
        if [ -f "$OUTPUT_DIR/quarkus_unary.log" ]; then
            echo "Unary Results:"
            grep -E "(Total Requests|Throughput|Average Latency|P95 Latency|Error Rate)" "$OUTPUT_DIR/quarkus_unary.log" || echo "No results found"
            echo ""
        fi
        
        if [ -f "$OUTPUT_DIR/quarkus_streaming.log" ]; then
            echo "Streaming Results:"
            grep -E "(Total Requests|Throughput|Average Latency|P95 Latency|Error Rate)" "$OUTPUT_DIR/quarkus_streaming.log" || echo "No results found"
            echo ""
        fi
        
        echo "==============================================="
        echo "Results saved to: $OUTPUT_DIR"
        echo "View detailed metrics at: http://localhost:3000"
        echo "==============================================="
        
    } > "$summary_file"
    
    cat "$summary_file"
}

# Main execution
main() {
    log "Starting benchmark execution..."
    
    # Check if client binary exists
    if [ ! -f "./bin/golang-client" ]; then
        echo -e "${RED}Error: Client binary not found. Please run ./scripts/build.sh first${NC}"
        exit 1
    fi
    
    # Check services
    check_services
    
    # Collect initial system metrics
    collect_system_metrics "$OUTPUT_DIR/system_metrics_before_${TIMESTAMP}.txt"
    
    # Run benchmarks
    echo -e "${PURPLE}Starting Golang server benchmark...${NC}"
    run_benchmark "golang" "localhost:50051" "golang"
    
    echo -e "${PURPLE}Starting Quarkus server benchmark...${NC}"
    run_benchmark "quarkus" "localhost:50052" "quarkus"
    
    # Collect final system metrics
    collect_system_metrics "$OUTPUT_DIR/system_metrics_after_${TIMESTAMP}.txt"
    
    # Generate summary
    summarize_results
    
    log "Benchmark completed successfully!"
    echo -e "${GREEN}Benchmark completed! Check results in: $OUTPUT_DIR${NC}"
}

# Run main function
main "$@"
