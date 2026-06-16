package main

import (
	"context"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	benchmarkpb "github.com/benchmark/golang"
	"github.com/benchmark/golang/internal/db"
	"github.com/benchmark/golang/internal/metrics"
	"github.com/benchmark/golang/internal/service"
)

const (
	defaultPort        = "50051"
	defaultMetricsPort = "8080"
	defaultDBURL       = "postgres://benchmark:password@localhost:5432/benchmark?sslmode=disable"
)

func main() {
	// Get configuration from environment variables
	port := getEnv("GRPC_PORT", defaultPort)
	metricsPort := getEnv("METRICS_PORT", defaultMetricsPort)
	dbURL := getEnv("DATABASE_URL", defaultDBURL)

	log.Printf("Starting Golang gRPC Benchmark Server")
	log.Printf("gRPC Port: %s", port)
	log.Printf("Metrics Port: %s", metricsPort)
	log.Printf("Database URL: %s", maskDBURL(dbURL))

	// Initialize database connection
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	database, err := db.NewDB(ctx, dbURL)
	if err != nil {
		log.Printf("Warning: Failed to connect to database: %v", err)
		log.Printf("Continuing without database support")
		database = nil
	}

	if database != nil {
		defer database.Close()
		log.Printf("Database connection established")
	}

	// Start metrics server
	metrics.StartMetricsServer(metricsPort)
	log.Printf("Metrics server started on port %s", metricsPort)

	// Create gRPC server
	grpcServer := grpc.NewServer(
		grpc.MaxRecvMsgSize(1024*1024*10), // 10MB
		grpc.MaxSendMsgSize(1024*1024*10), // 10MB
	)

	// Register benchmark service
	benchmarkService := service.NewBenchmarkServer(database)
	benchmarkpb.RegisterBenchmarkServiceServer(grpcServer, benchmarkService)

	// Enable reflection for easier debugging
	reflection.Register(grpcServer)

	// Create listener
	listener, err := net.Listen("tcp", ":"+port)
	if err != nil {
		log.Fatalf("Failed to listen on port %s: %v", port, err)
	}

	// Handle graceful shutdown
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
		<-sigChan

		log.Printf("Shutting down server gracefully...")
		grpcServer.GracefulStop()
		log.Printf("Server stopped")
	}()

	// Start server
	log.Printf("Golang gRPC Benchmark Server listening on :%s", port)
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("Failed to serve: %v", err)
	}
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func maskDBURL(dbURL string) string {
	// Simple masking for logging purposes
	if len(dbURL) > 20 {
		return dbURL[:20] + "..."
	}
	return dbURL
}
