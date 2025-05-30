package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/benchmark/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type BenchmarkConfig struct {
	ServerAddr      string
	Duration        time.Duration
	Concurrency     int
	MessageSize     int
	TestType        string
	StreamCount     int
	ChunkSize       int
	ReportInterval  time.Duration
}

type BenchmarkResults struct {
	TotalRequests   int64
	SuccessCount    int64
	ErrorCount      int64
	TotalDuration   time.Duration
	MinLatency      time.Duration
	MaxLatency      time.Duration
	AvgLatency      time.Duration
	P95Latency      time.Duration
	P99Latency      time.Duration
	Throughput      float64
	BytesTransferred int64
}

func main() {
	config := parseFlags()
	
	fmt.Printf("Starting gRPC Benchmark Client\n")
	fmt.Printf("Server: %s\n", config.ServerAddr)
	fmt.Printf("Test Type: %s\n", config.TestType)
	fmt.Printf("Duration: %v\n", config.Duration)
	fmt.Printf("Concurrency: %d\n", config.Concurrency)
	fmt.Printf("Message Size: %d bytes\n", config.MessageSize)
	fmt.Printf("Stream Count: %d\n", config.StreamCount)
	fmt.Printf("----------------------------------------\n")

	conn, err := grpc.Dial(config.ServerAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := proto.NewStreamingBenchmarkServiceClient(conn)

	var results *BenchmarkResults
	switch config.TestType {
	case "echo":
		results = runEchoBenchmark(client, config)
	case "client-stream":
		results = runClientStreamBenchmark(client, config)
	case "server-stream":
		results = runServerStreamBenchmark(client, config)
	case "bidi-stream":
		results = runBidirectionalStreamBenchmark(client, config)
	case "large-data":
		results = runLargeDataStreamBenchmark(client, config)
	case "all":
		runAllBenchmarks(client, config)
		return
	default:
		log.Fatalf("Unknown test type: %s", config.TestType)
	}

	printResults(config.TestType, results)
}

func parseFlags() *BenchmarkConfig {
	config := &BenchmarkConfig{}
	
	flag.StringVar(&config.ServerAddr, "server", "localhost:8080", "gRPC server address")
	flag.DurationVar(&config.Duration, "duration", 30*time.Second, "Test duration")
	flag.IntVar(&config.Concurrency, "concurrency", 10, "Number of concurrent clients")
	flag.IntVar(&config.MessageSize, "message-size", 1024, "Message size in bytes")
	flag.StringVar(&config.TestType, "test", "echo", "Test type: echo, client-stream, server-stream, bidi-stream, large-data, all")
	flag.IntVar(&config.StreamCount, "stream-count", 100, "Number of messages per stream")
	flag.IntVar(&config.ChunkSize, "chunk-size", 8192, "Chunk size for large data transfer")
	flag.DurationVar(&config.ReportInterval, "report-interval", 5*time.Second, "Reporting interval")
	
	flag.Parse()
	return config
}

func runEchoBenchmark(client proto.StreamingBenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	fmt.Println("Running Echo (Unary) Benchmark...")
	
	var wg sync.WaitGroup
	results := &BenchmarkResults{}
	latencies := make([]time.Duration, 0)
	var latencyMutex sync.Mutex
	
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()

	// Create test payload
	payload := generatePayload(config.MessageSize)
	
	startTime := time.Now()
	
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for {
				select {
				case <-ctx.Done():
					return
				default:
					requestStart := time.Now()
					
					req := &proto.EchoRequest{
						Message:   payload,
						Timestamp: time.Now().UnixNano(),
					}
					
					_, err := client.Echo(context.Background(), req)
					
					latency := time.Since(requestStart)
					
					latencyMutex.Lock()
					if err != nil {
						results.ErrorCount++
					} else {
						results.SuccessCount++
						latencies = append(latencies, latency)
					}
					results.TotalRequests++
					results.BytesTransferred += int64(len(payload))
					latencyMutex.Unlock()
				}
			}
		}(i)
	}
	
	wg.Wait()
	results.TotalDuration = time.Since(startTime)
	
	// Calculate statistics
	calculateLatencyStats(results, latencies)
	results.Throughput = float64(results.SuccessCount) / results.TotalDuration.Seconds()
	
	return results
}

func runClientStreamBenchmark(client proto.StreamingBenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	fmt.Println("Running Client Stream Benchmark...")
	
	var wg sync.WaitGroup
	results := &BenchmarkResults{}
	latencies := make([]time.Duration, 0)
	var latencyMutex sync.Mutex
	
	payload := generatePayload(config.MessageSize)
	startTime := time.Now()
	
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			streamStart := time.Now()
			stream, err := client.ClientStream(context.Background())
			if err != nil {
				latencyMutex.Lock()
				results.ErrorCount++
				results.TotalRequests++
				latencyMutex.Unlock()
				return
			}
			
			// Send multiple requests
			for j := 0; j < config.StreamCount; j++ {
				req := &proto.StreamRequest{
					SequenceId: int32(j),
					Payload:    payload,
					Timestamp:  time.Now().UnixNano(),
					Type:       proto.MessageType_NORMAL,
					UserId:     fmt.Sprintf("user_%d", workerID),
					SessionId:  fmt.Sprintf("session_%d_%d", workerID, time.Now().Unix()),
					Metadata:   map[string]string{"worker_id": fmt.Sprintf("%d", workerID)},
				}
				
				if err := stream.Send(req); err != nil {
					latencyMutex.Lock()
					results.ErrorCount++
					latencyMutex.Unlock()
					return
				}
				
				latencyMutex.Lock()
				results.BytesTransferred += int64(len(payload))
				latencyMutex.Unlock()
			}
			
			_, err = stream.CloseAndRecv()
			streamLatency := time.Since(streamStart)
			
			latencyMutex.Lock()
			if err != nil {
				results.ErrorCount++
			} else {
				results.SuccessCount++
				latencies = append(latencies, streamLatency)
			}
			results.TotalRequests++
			latencyMutex.Unlock()
		}(i)
	}
	
	wg.Wait()
	results.TotalDuration = time.Since(startTime)
	
	calculateLatencyStats(results, latencies)
	results.Throughput = float64(results.SuccessCount) / results.TotalDuration.Seconds()
	
	return results
}

func runServerStreamBenchmark(client proto.StreamingBenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	fmt.Println("Running Server Stream Benchmark...")
	
	var wg sync.WaitGroup
	results := &BenchmarkResults{}
	latencies := make([]time.Duration, 0)
	var latencyMutex sync.Mutex
	
	payload := generatePayload(config.MessageSize)
	startTime := time.Now()
	
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			streamStart := time.Now()
			
			req := &proto.StreamRequest{
				SequenceId: int32(workerID),
				Payload:    payload,
				Timestamp:  time.Now().UnixNano(),
				Type:       proto.MessageType_NORMAL,
				UserId:     fmt.Sprintf("user_%d", workerID),
				SessionId:  fmt.Sprintf("session_%d_%d", workerID, time.Now().Unix()),
				Metadata:   map[string]string{"worker_id": fmt.Sprintf("%d", workerID)},
			}
			
			stream, err := client.ServerStream(context.Background(), req)
			if err != nil {
				latencyMutex.Lock()
				results.ErrorCount++
				results.TotalRequests++
				latencyMutex.Unlock()
				return
			}
			
			responseCount := 0
			for {
				_, err := stream.Recv()
				if err == io.EOF {
					break
				}
				if err != nil {
					latencyMutex.Lock()
					results.ErrorCount++
					latencyMutex.Unlock()
					return
				}
				responseCount++
				
				latencyMutex.Lock()
				results.BytesTransferred += int64(len(payload))
				latencyMutex.Unlock()
			}
			
			streamLatency := time.Since(streamStart)
			
			latencyMutex.Lock()
			results.SuccessCount++
			results.TotalRequests++
			latencies = append(latencies, streamLatency)
			latencyMutex.Unlock()
		}(i)
	}
	
	wg.Wait()
	results.TotalDuration = time.Since(startTime)
	
	calculateLatencyStats(results, latencies)
	results.Throughput = float64(results.SuccessCount) / results.TotalDuration.Seconds()
	
	return results
}

func runBidirectionalStreamBenchmark(client proto.StreamingBenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	fmt.Println("Running Bidirectional Stream Benchmark...")
	
	var wg sync.WaitGroup
	results := &BenchmarkResults{}
	latencies := make([]time.Duration, 0)
	var latencyMutex sync.Mutex
	
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration)
	defer cancel()
	
	payload := generatePayload(config.MessageSize)
	startTime := time.Now()
	
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			for {
				select {
				case <-ctx.Done():
					return
				default:
					streamStart := time.Now()
					stream, err := client.BidirectionalStream(context.Background())
					if err != nil {
						latencyMutex.Lock()
						results.ErrorCount++
						results.TotalRequests++
						latencyMutex.Unlock()
						continue
					}
					
					// Send and receive concurrently
					var streamWg sync.WaitGroup
					sendCount := config.StreamCount
					receiveCount := 0
					
					// Sender goroutine
					streamWg.Add(1)
					go func() {
						defer streamWg.Done()
						defer stream.CloseSend()
						
						for j := 0; j < sendCount; j++ {
							req := &proto.StreamRequest{
								SequenceId: int32(j),
								Payload:    payload,
								Timestamp:  time.Now().UnixNano(),
								Type:       proto.MessageType_NORMAL,
								UserId:     fmt.Sprintf("user_%d", workerID),
								SessionId:  fmt.Sprintf("session_%d_%d", workerID, time.Now().Unix()),
								Metadata:   map[string]string{"worker_id": fmt.Sprintf("%d", workerID)},
							}
							
							if err := stream.Send(req); err != nil {
								return
							}
							
							latencyMutex.Lock()
							results.BytesTransferred += int64(len(payload))
							latencyMutex.Unlock()
						}
					}()
					
					// Receiver goroutine
					streamWg.Add(1)
					go func() {
						defer streamWg.Done()
						
						for {
							_, err := stream.Recv()
							if err == io.EOF {
								break
							}
							if err != nil {
								return
							}
							receiveCount++
						}
					}()
					
					streamWg.Wait()
					streamLatency := time.Since(streamStart)
					
					latencyMutex.Lock()
					if receiveCount > 0 {
						results.SuccessCount++
						latencies = append(latencies, streamLatency)
					} else {
						results.ErrorCount++
					}
					results.TotalRequests++
					latencyMutex.Unlock()
				}
			}
		}(i)
	}
	
	wg.Wait()
	results.TotalDuration = time.Since(startTime)
	
	calculateLatencyStats(results, latencies)
	results.Throughput = float64(results.SuccessCount) / results.TotalDuration.Seconds()
	
	return results
}

func runLargeDataStreamBenchmark(client proto.StreamingBenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	fmt.Println("Running Large Data Stream Benchmark...")
	
	var wg sync.WaitGroup
	results := &BenchmarkResults{}
	latencies := make([]time.Duration, 0)
	var latencyMutex sync.Mutex
	
	// Create large data (1MB file simulation)
	totalSize := 1024 * 1024 // 1MB
	chunkSize := config.ChunkSize
	totalChunks := (totalSize + chunkSize - 1) / chunkSize
	
	startTime := time.Now()
	
	for i := 0; i < config.Concurrency; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			streamStart := time.Now()
			stream, err := client.LargeDataStream(context.Background())
			if err != nil {
				latencyMutex.Lock()
				results.ErrorCount++
				results.TotalRequests++
				latencyMutex.Unlock()
				return
			}
			
			// Send chunks
			for chunkID := 0; chunkID < totalChunks; chunkID++ {
				var chunkData []byte
				if chunkID == totalChunks-1 {
					// Last chunk might be smaller
					remaining := totalSize - (chunkID * chunkSize)
					chunkData = generateRandomBytes(remaining)
				} else {
					chunkData = generateRandomBytes(chunkSize)
				}
				
				chunk := &proto.DataChunk{
					ChunkId:     int32(chunkID),
					Data:        chunkData,
					TotalChunks: int32(totalChunks),
					Filename:    fmt.Sprintf("test_file_%d.dat", workerID),
				}
				
				if err := stream.Send(chunk); err != nil {
					latencyMutex.Lock()
					results.ErrorCount++
					latencyMutex.Unlock()
					return
				}
				
				latencyMutex.Lock()
				results.BytesTransferred += int64(len(chunkData))
				latencyMutex.Unlock()
			}
			
			_, err = stream.CloseAndRecv()
			streamLatency := time.Since(streamStart)
			
			latencyMutex.Lock()
			if err != nil {
				results.ErrorCount++
			} else {
				results.SuccessCount++
				latencies = append(latencies, streamLatency)
			}
			results.TotalRequests++
			latencyMutex.Unlock()
		}(i)
	}
	
	wg.Wait()
	results.TotalDuration = time.Since(startTime)
	
	calculateLatencyStats(results, latencies)
	results.Throughput = float64(results.SuccessCount) / results.TotalDuration.Seconds()
	
	return results
}

func runAllBenchmarks(client proto.StreamingBenchmarkServiceClient, config *BenchmarkConfig) {
	tests := []string{"echo", "client-stream", "server-stream", "bidi-stream", "large-data"}
	
	fmt.Println("Running All Benchmarks...")
	fmt.Println("========================================")
	
	for _, testType := range tests {
		config.TestType = testType
		
		var results *BenchmarkResults
		switch testType {
		case "echo":
			results = runEchoBenchmark(client, config)
		case "client-stream":
			results = runClientStreamBenchmark(client, config)
		case "server-stream":
			results = runServerStreamBenchmark(client, config)
		case "bidi-stream":
			results = runBidirectionalStreamBenchmark(client, config)
		case "large-data":
			results = runLargeDataStreamBenchmark(client, config)
		}
		
		printResults(testType, results)
		fmt.Println("----------------------------------------")
		
		// Wait between tests
		time.Sleep(2 * time.Second)
	}
}

func calculateLatencyStats(results *BenchmarkResults, latencies []time.Duration) {
	if len(latencies) == 0 {
		return
	}
	
	// Sort latencies for percentile calculation
	for i := 0; i < len(latencies); i++ {
		for j := i + 1; j < len(latencies); j++ {
			if latencies[i] > latencies[j] {
				latencies[i], latencies[j] = latencies[j], latencies[i]
			}
		}
	}
	
	results.MinLatency = latencies[0]
	results.MaxLatency = latencies[len(latencies)-1]
	
	// Calculate average
	var total time.Duration
	for _, latency := range latencies {
		total += latency
	}
	results.AvgLatency = total / time.Duration(len(latencies))
	
	// Calculate percentiles
	p95Index := int(float64(len(latencies)) * 0.95)
	p99Index := int(float64(len(latencies)) * 0.99)
	
	if p95Index >= len(latencies) {
		p95Index = len(latencies) - 1
	}
	if p99Index >= len(latencies) {
		p99Index = len(latencies) - 1
	}
	
	results.P95Latency = latencies[p95Index]
	results.P99Latency = latencies[p99Index]
}

func printResults(testType string, results *BenchmarkResults) {
	fmt.Printf("\n=== %s Benchmark Results ===\n", testType)
	fmt.Printf("Total Requests:      %d\n", results.TotalRequests)
	fmt.Printf("Successful:          %d\n", results.SuccessCount)
	fmt.Printf("Errors:              %d\n", results.ErrorCount)
	fmt.Printf("Success Rate:        %.2f%%\n", float64(results.SuccessCount)/float64(results.TotalRequests)*100)
	fmt.Printf("Total Duration:      %v\n", results.TotalDuration)
	fmt.Printf("Throughput:          %.2f req/sec\n", results.Throughput)
	fmt.Printf("Data Transferred:    %.2f MB\n", float64(results.BytesTransferred)/(1024*1024))
	
	if results.SuccessCount > 0 {
		fmt.Printf("\nLatency Statistics:\n")
		fmt.Printf("  Min:               %v\n", results.MinLatency)
		fmt.Printf("  Max:               %v\n", results.MaxLatency)
		fmt.Printf("  Average:           %v\n", results.AvgLatency)
		fmt.Printf("  95th Percentile:   %v\n", results.P95Latency)
		fmt.Printf("  99th Percentile:   %v\n", results.P99Latency)
	}
	fmt.Println()
}

func generatePayload(size int) string {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	result := make([]byte, size)
	for i := range result {
		result[i] = chars[i%len(chars)]
	}
	return string(result)
}

func generateRandomBytes(size int) []byte {
	data := make([]byte, size)
	rand.Read(data)
	return data
}
