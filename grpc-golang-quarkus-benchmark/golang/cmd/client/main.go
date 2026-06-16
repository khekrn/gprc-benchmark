package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	benchmarkpb "github.com/benchmark/golang"
	"github.com/benchmark/golang/internal/service"
)

type BenchmarkConfig struct {
	ServerAddr   string
	TLS          bool
	Concurrency  int
	Duration     time.Duration
	UnaryRPS     int
	StreamingRPS int
	SaveToDB     bool
	TargetServer string // "golang" or "quarkus"
	ClientID     string
}

type BenchmarkResults struct {
	TotalRequests  int64
	SuccessfulReqs int64
	FailedReqs     int64
	AvgLatency     time.Duration
	MinLatency     time.Duration
	MaxLatency     time.Duration
	P95Latency     time.Duration
	P99Latency     time.Duration
	Throughput     float64
	ErrorRate      float64
}

func main() {
	config := parseFlags()

	log.Printf("Starting Golang gRPC Benchmark Client")
	log.Printf("Target: %s", config.ServerAddr)
	log.Printf("Target Server: %s", config.TargetServer)
	log.Printf("Concurrency: %d", config.Concurrency)
	log.Printf("Duration: %v", config.Duration)
	log.Printf("Unary RPS: %d", config.UnaryRPS)
	log.Printf("Streaming RPS: %d", config.StreamingRPS)
	log.Printf("Save to DB: %v", config.SaveToDB)

	// Create gRPC connection
	conn, err := createConnection(config.ServerAddr, config.TLS)
	if err != nil {
		log.Fatalf("Failed to connect: %v", err)
	}
	defer conn.Close()

	client := benchmarkpb.NewBenchmarkServiceClient(conn)

	// Health check
	if err := healthCheck(client); err != nil {
		log.Printf("Health check failed: %v", err)
	}

	// Run benchmarks
	ctx, cancel := context.WithTimeout(context.Background(), config.Duration+30*time.Second)
	defer cancel()

	var wg sync.WaitGroup

	// Unary benchmark
	if config.UnaryRPS > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := runUnaryBenchmark(ctx, client, config)
			printResults("Unary", results)
		}()
	}

	// Streaming benchmark
	if config.StreamingRPS > 0 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results := runStreamingBenchmark(ctx, client, config)
			printResults("Streaming", results)
		}()
	}

	wg.Wait()
	log.Printf("Benchmark completed")
}

func parseFlags() *BenchmarkConfig {
	config := &BenchmarkConfig{}

	flag.StringVar(&config.ServerAddr, "addr", "localhost:50051", "Server address")
	flag.BoolVar(&config.TLS, "tls", false, "Use TLS")
	flag.IntVar(&config.Concurrency, "c", 10, "Number of concurrent clients")
	flag.DurationVar(&config.Duration, "d", 30*time.Second, "Benchmark duration")
	flag.IntVar(&config.UnaryRPS, "unary-rps", 100, "Unary requests per second (0 to disable)")
	flag.IntVar(&config.StreamingRPS, "streaming-rps", 50, "Streaming requests per second (0 to disable)")
	flag.BoolVar(&config.SaveToDB, "save-db", true, "Save results to database")
	flag.StringVar(&config.TargetServer, "target", "golang", "Target server type (golang or quarkus)")
	flag.StringVar(&config.ClientID, "client-id", "benchmark-client", "Client identifier")

	flag.Parse()
	return config
}

func createConnection(addr string, useTLS bool) (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if useTLS {
		creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})
		opts = append(opts, grpc.WithTransportCredentials(creds))
	} else {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	opts = append(opts,
		grpc.WithMaxMsgSize(1024*1024*10), // 10MB
	)

	return grpc.Dial(addr, opts...)
}

func healthCheck(client benchmarkpb.BenchmarkServiceClient) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := client.HealthCheck(ctx, &benchmarkpb.HealthRequest{
		Service: "benchmark",
	})
	if err != nil {
		return err
	}

	log.Printf("Health check successful: %s (Server: %s)", resp.Status, resp.ServerType)
	return nil
}

func runUnaryBenchmark(ctx context.Context, client benchmarkpb.BenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	results := &BenchmarkResults{
		MinLatency: time.Hour, // Initialize with large value
	}

	var latencies []time.Duration
	var mu sync.Mutex

	ticker := time.NewTicker(time.Second / time.Duration(config.UnaryRPS))
	defer ticker.Stop()

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, config.Concurrency)

	endTime := time.Now().Add(config.Duration)

	sequenceNum := int64(0)
	for time.Now().Before(endTime) {
		select {
		case <-ctx.Done():
			goto done
		case <-ticker.C:
			semaphore <- struct{}{}
			wg.Add(1)

			sequenceNum++
			go func(seq int64) {
				defer func() {
					<-semaphore
					wg.Done()
				}()

				start := time.Now()
				message := service.GenerateTestMessage(config.ClientID, seq, "unary")

				_, err := client.ProcessUnary(ctx, &benchmarkpb.UnaryRequest{
					Message:  message,
					SaveToDb: config.SaveToDB,
				})

				latency := time.Since(start)

				mu.Lock()
				results.TotalRequests++
				if err != nil {
					results.FailedReqs++
				} else {
					results.SuccessfulReqs++
					latencies = append(latencies, latency)

					if latency < results.MinLatency {
						results.MinLatency = latency
					}
					if latency > results.MaxLatency {
						results.MaxLatency = latency
					}
				}
				mu.Unlock()
			}(sequenceNum)
		}
	}

done:
	wg.Wait()

	if len(latencies) > 0 {
		results.AvgLatency = calculateAverage(latencies)
		results.P95Latency = calculatePercentile(latencies, 0.95)
		results.P99Latency = calculatePercentile(latencies, 0.99)
	}

	results.Throughput = float64(results.SuccessfulReqs) / config.Duration.Seconds()
	if results.TotalRequests > 0 {
		results.ErrorRate = float64(results.FailedReqs) / float64(results.TotalRequests) * 100
	}

	return results
}

func runStreamingBenchmark(ctx context.Context, client benchmarkpb.BenchmarkServiceClient, config *BenchmarkConfig) *BenchmarkResults {
	results := &BenchmarkResults{
		MinLatency: time.Hour,
	}

	var latencies []time.Duration
	var mu sync.Mutex

	var wg sync.WaitGroup
	semaphore := make(chan struct{}, config.Concurrency)

	endTime := time.Now().Add(config.Duration)
	requestInterval := time.Second / time.Duration(config.StreamingRPS)

	for i := 0; i < config.Concurrency; i++ {
		semaphore <- struct{}{}
		wg.Add(1)

		go func(clientNum int) {
			defer func() {
				<-semaphore
				wg.Done()
			}()

			stream, err := client.ProcessStreaming(ctx)
			if err != nil {
				log.Printf("Failed to create stream: %v", err)
				return
			}

			// Start receiving responses
			go func() {
				for {
					_, err := stream.Recv()
					if err == io.EOF {
						return
					}
					if err != nil {
						log.Printf("Stream receive error: %v", err)
						return
					}
				}
			}()

			ticker := time.NewTicker(requestInterval)
			defer ticker.Stop()

			sequenceNum := int64(clientNum * 10000)
			for time.Now().Before(endTime) {
				select {
				case <-ctx.Done():
					stream.CloseSend()
					return
				case <-ticker.C:
					start := time.Now()
					sequenceNum++

					message := service.GenerateTestMessage(config.ClientID, sequenceNum, "streaming")

					err := stream.Send(&benchmarkpb.StreamingRequest{
						Message:   message,
						SaveToDb:  config.SaveToDB,
						BatchSize: 1,
					})

					latency := time.Since(start)

					mu.Lock()
					results.TotalRequests++
					if err != nil {
						results.FailedReqs++
					} else {
						results.SuccessfulReqs++
						latencies = append(latencies, latency)

						if latency < results.MinLatency {
							results.MinLatency = latency
						}
						if latency > results.MaxLatency {
							results.MaxLatency = latency
						}
					}
					mu.Unlock()
				}
			}

			stream.CloseSend()
		}(i)
	}

	wg.Wait()

	if len(latencies) > 0 {
		results.AvgLatency = calculateAverage(latencies)
		results.P95Latency = calculatePercentile(latencies, 0.95)
		results.P99Latency = calculatePercentile(latencies, 0.99)
	}

	results.Throughput = float64(results.SuccessfulReqs) / config.Duration.Seconds()
	if results.TotalRequests > 0 {
		results.ErrorRate = float64(results.FailedReqs) / float64(results.TotalRequests) * 100
	}

	return results
}

func calculateAverage(latencies []time.Duration) time.Duration {
	var total time.Duration
	for _, latency := range latencies {
		total += latency
	}
	return total / time.Duration(len(latencies))
}

func calculatePercentile(latencies []time.Duration, percentile float64) time.Duration {
	if len(latencies) == 0 {
		return 0
	}

	// Simple percentile calculation (should sort for accuracy)
	index := int(float64(len(latencies)) * percentile)
	if index >= len(latencies) {
		index = len(latencies) - 1
	}

	return latencies[index]
}

func printResults(testType string, results *BenchmarkResults) {
	fmt.Printf("\n=== %s Benchmark Results ===\n", testType)
	fmt.Printf("Total Requests: %d\n", results.TotalRequests)
	fmt.Printf("Successful: %d\n", results.SuccessfulReqs)
	fmt.Printf("Failed: %d\n", results.FailedReqs)
	fmt.Printf("Error Rate: %.2f%%\n", results.ErrorRate)
	fmt.Printf("Throughput: %.2f req/s\n", results.Throughput)
	fmt.Printf("Average Latency: %v\n", results.AvgLatency)
	fmt.Printf("Min Latency: %v\n", results.MinLatency)
	fmt.Printf("Max Latency: %v\n", results.MaxLatency)
	fmt.Printf("P95 Latency: %v\n", results.P95Latency)
	fmt.Printf("P99 Latency: %v\n", results.P99Latency)
	fmt.Printf("================================\n")
}
