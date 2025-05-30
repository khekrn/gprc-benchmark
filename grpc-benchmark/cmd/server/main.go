package main

import (
	"context"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/benchmark/internal/db"
	"github.com/benchmark/internal/metrics"
	"github.com/benchmark/internal/service"
	"github.com/benchmark/proto"
	"github.com/joho/godotenv"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/reflection"
)

func main() {
	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Warn().Err(err).Msg("No .env file found, using system environment variables")
	}

	// Configure logging
	setupLogging()

	// Initialize database
	database, err := db.NewDB()
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to initialize database")
	}
	defer database.Close()

	// Create gRPC server with configuration
	grpcServer := createGRPCServer()

	// Register services
	benchmarkService := service.NewBenchmarkService(database)
	proto.RegisterStreamingBenchmarkServiceServer(grpcServer, benchmarkService)

	// Enable reflection for easier testing
	reflection.Register(grpcServer)

	// Start metrics server in goroutine
	go func() {
		metricsPort := getEnv("METRICS_PORT", "8081")
		log.Info().Str("port", metricsPort).Msg("Starting metrics server")
		if err := metrics.StartMetricsServer(metricsPort); err != nil {
			log.Error().Err(err).Msg("Failed to start metrics server")
		}
	}()

	// Start gRPC server
	serverPort := getEnv("SERVER_PORT", "8080")
	listener, err := net.Listen("tcp", ":"+serverPort)
	if err != nil {
		log.Fatal().Err(err).Str("port", serverPort).Msg("Failed to listen")
	}

	log.Info().Str("port", serverPort).Msg("Starting gRPC server")

	// Start server in goroutine
	go func() {
		if err := grpcServer.Serve(listener); err != nil {
			log.Error().Err(err).Msg("Failed to serve gRPC")
		}
	}()

	// Wait for interrupt signal to gracefully shutdown the server
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Info().Msg("Shutting down server...")

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create a channel to signal when graceful stop is completed
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	// Wait for graceful stop or timeout
	select {
	case <-stopped:
		log.Info().Msg("Server gracefully stopped")
	case <-ctx.Done():
		log.Warn().Msg("Server shutdown timeout, forcing stop")
		grpcServer.Stop()
	}
}

func setupLogging() {
	logLevel := getEnv("LOG_LEVEL", "info")
	logFormat := getEnv("LOG_FORMAT", "json")

	// Set log level
	level, err := zerolog.ParseLevel(logLevel)
	if err != nil {
		level = zerolog.InfoLevel
	}
	zerolog.SetGlobalLevel(level)

	// Set log format
	if logFormat == "console" {
		log.Logger = log.Output(zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339})
	}

	log.Info().
		Str("level", level.String()).
		Str("format", logFormat).
		Msg("Logging configured")
}

func createGRPCServer() *grpc.Server {
	// Parse configuration
	maxConcurrentStreams := getEnvAsInt("MAX_CONCURRENT_STREAMS", 1000)
	keepAliveTime := getEnvAsDuration("KEEP_ALIVE", "30s")
	keepAliveTimeout := getEnvAsDuration("KEEP_ALIVE_TIMEOUT", "5s")
	maxConnectionIdle := getEnvAsDuration("MAX_CONNECTION_IDLE", "15m")
	maxConnectionAge := getEnvAsDuration("MAX_CONNECTION_AGE", "30m")

	// Server options
	opts := []grpc.ServerOption{
		grpc.MaxConcurrentStreams(uint32(maxConcurrentStreams)),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    keepAliveTime,
			Timeout: keepAliveTimeout,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.ConnectionTimeout(60 * time.Second),
		grpc.MaxRecvMsgSize(4 * 1024 * 1024),  // 4MB
		grpc.MaxSendMsgSize(4 * 1024 * 1024),  // 4MB
	}

	log.Info().
		Int("max_concurrent_streams", maxConcurrentStreams).
		Dur("keep_alive_time", keepAliveTime).
		Dur("keep_alive_timeout", keepAliveTimeout).
		Dur("max_connection_idle", maxConnectionIdle).
		Dur("max_connection_age", maxConnectionAge).
		Msg("gRPC server configuration")

	return grpc.NewServer(opts...)
}

func getEnv(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getEnvAsInt(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if intValue, err := parseInt(value); err == nil {
			return intValue
		}
	}
	return defaultValue
}

func getEnvAsDuration(key, defaultValue string) time.Duration {
	if value := os.Getenv(key); value != "" {
		if duration, err := time.ParseDuration(value); err == nil {
			return duration
		}
	}
	duration, _ := time.ParseDuration(defaultValue)
	return duration
}

func parseInt(s string) (int, error) {
	result := 0
	for _, digit := range s {
		if digit < '0' || digit > '9' {
			return 0, &customError{"invalid number format"}
		}
		result = result*10 + int(digit-'0')
	}
	return result, nil
}

type customError struct {
	msg string
}

func (e *customError) Error() string {
	return e.msg
}
