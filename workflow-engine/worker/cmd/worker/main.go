package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/keepalive"

	"workflow-worker/internal/config"
	"workflow-worker/internal/engine"
	workergrpc "workflow-worker/internal/grpc"
	redisclient "workflow-worker/internal/redis"
	pb "workflow-worker/proto"
)

// RegisterWorkflowEndpointsRequest represents the request payload for workflow endpoint registration
type RegisterWorkflowEndpointsRequest struct {
	WorkflowName string   `json:"workflow_name"`
	Endpoints    []string `json:"endpoints"`
}

// RegisterWorkflowEndpointsResponse represents the response from workflow endpoint registration
type RegisterWorkflowEndpointsResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// registerWorkflowsWithServer registers the worker's workflows with the server via HTTP API
func registerWorkflowsWithServer(serverEndpoint string, workflows []string, workerEndpoint string) error {
	for _, workflow := range workflows {
		req := RegisterWorkflowEndpointsRequest{
			WorkflowName: workflow,
			Endpoints:    []string{workerEndpoint},
		}

		jsonData, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("failed to marshal request: %v", err)
		}

		url := fmt.Sprintf("http://%s/api/v1/workflows/endpoints", serverEndpoint)
		resp, err := http.Post(url, "application/json", bytes.NewBuffer(jsonData))
		if err != nil {
			return fmt.Errorf("failed to register workflow %s: %v", workflow, err)
		}
		defer resp.Body.Close()

		var response RegisterWorkflowEndpointsResponse
		if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
			return fmt.Errorf("failed to decode response for workflow %s: %v", workflow, err)
		}

		if !response.Success {
			return fmt.Errorf("failed to register workflow %s: %s", workflow, response.Message)
		}

		log.Printf("Successfully registered workflow %s with server at %s", workflow, serverEndpoint)
	}

	return nil
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file")
	flag.Parse()

	// Load environment variables from .env file if it exists
	godotenv.Load()

	// Load configuration
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	log.Printf("Starting workflow worker: %s", cfg.Worker.Name)

	// Create Redis client for worker registration
	redisClient, err := redisclient.NewClient(&cfg.Redis)
	if err != nil {
		log.Fatalf("Failed to create Redis client: %v", err)
	}
	defer redisClient.Close()

	// Create workflow engine
	workflowEngine := engine.NewWorkflowEngine()

	// Log registered workflows
	workflows := workflowEngine.GetRegisteredWorkflows()
	log.Printf("Registered workflows: %v", workflows)

	// Create worker gRPC server
	workerServer := workergrpc.NewWorkerServer(workflowEngine)

	// Start gRPC server with high-performance settings
	lis, err := net.Listen("tcp", ":"+cfg.Worker.Port)
	if err != nil {
		log.Fatalf("Failed to listen on port %s: %v", cfg.Worker.Port, err)
	}

	// High-performance gRPC server options
	s := grpc.NewServer(
		grpc.MaxConcurrentStreams(1000),   // Support many concurrent streams
		grpc.MaxRecvMsgSize(16*1024*1024), // 16MB max message size
		grpc.MaxSendMsgSize(16*1024*1024), // 16MB max message size
		grpc.KeepaliveParams(keepalive.ServerParameters{
			MaxConnectionIdle:     time.Minute * 15, // Close idle connections after 15 min
			MaxConnectionAge:      time.Hour,        // Rotate connections hourly
			MaxConnectionAgeGrace: time.Minute * 5,  // Grace period for draining
			Time:                  time.Minute * 5,  // Send keepalive every 5 min
			Timeout:               time.Second * 1,  // Keepalive timeout
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             time.Second * 10, // Min time between keepalives
			PermitWithoutStream: true,             // Allow keepalive without streams
		}),
	)
	pb.RegisterWorkerServiceServer(s, workerServer)

	log.Printf("Worker gRPC server listening on port %s", cfg.Worker.Port)

	// Start gRPC server in a goroutine
	go func() {
		if err := s.Serve(lis); err != nil {
			log.Fatalf("Failed to serve: %v", err)
		}
	}()

	// Register worker with Redis
	workerInfo := redisclient.WorkerInfo{
		Name:          cfg.Worker.Name,
		Endpoint:      cfg.Worker.Endpoint,
		WorkflowTypes: workflows, // Pass workflows as string array
		Capacity:      "64",      // High throughput capacity
		Metadata: map[string]string{
			"port": cfg.Worker.Port,
		},
	}

	err = redisClient.RegisterWorker(workerInfo)
	if err != nil {
		log.Fatalf("Failed to register worker with Redis: %v", err)
	}

	// Start heartbeat to keep worker alive in Redis
	redisClient.StartHeartbeat(cfg.Worker.Name, 30*time.Second)

	log.Printf("Worker registered with Redis and ready for connections from workflow servers")

	// Register workflows with server via HTTP API
	if len(cfg.Servers.Endpoints) > 0 {
		// Convert gRPC address to HTTP address (assuming HTTP server runs on port 8080)
		// If server address is localhost:9090, convert to localhost:8080
		httpServerEndpoint := "localhost:8080" // Default HTTP server endpoint

		err = registerWorkflowsWithServer(httpServerEndpoint, workflows, cfg.Worker.Endpoint)
		if err != nil {
			log.Printf("Warning: Failed to register workflows with server: %v", err)
		} else {
			log.Printf("Successfully registered all workflows with server at %s", httpServerEndpoint)
		}
	} else {
		log.Printf("No server endpoints configured, skipping workflow registration")
	}

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Worker started successfully. Waiting for workflow server connections...")
	<-sigCh

	log.Printf("Shutting down worker...")

	// Unregister worker from Redis
	err = redisClient.UnregisterWorker(cfg.Worker.Name)
	if err != nil {
		log.Printf("Failed to unregister worker from Redis: %v", err)
	}

	// Gracefully stop the gRPC server
	s.GracefulStop()
}
