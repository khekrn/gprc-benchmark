package main

import (
	"flag"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"google.golang.org/grpc"

	"workflow-worker/internal/config"
	"workflow-worker/internal/engine"
	grpcClient "workflow-worker/internal/grpc"
	pb "workflow-worker/proto"
)

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

	// Create load balancing gRPC client
	lbClient, err := grpcClient.NewLoadBalancingClient(cfg)
	if err != nil {
		log.Fatalf("Failed to create load balancing client: %v", err)
	}
	defer lbClient.Close()

	// Register endpoint with all servers (unary call)
	_, err = lbClient.RegisterEndpoint(cfg.Worker.Name, cfg.Worker.Endpoint)
	if err != nil {
		log.Fatalf("Failed to register endpoint: %v", err)
	}

	log.Printf("Worker endpoint registered successfully: %s -> %s", cfg.Worker.Name, cfg.Worker.Endpoint)

	// Create workflow engine using the load balancing client
	workflowEngine := engine.NewWorkflowEngine(lbClient)

	// Set the workflow engine on the client so it can execute workflows from stream
	lbClient.SetWorkflowEngine(workflowEngine)

	// Log registered workflows
	workflows := workflowEngine.GetRegisteredWorkflows()
	log.Printf("Registered workflows: %v", workflows)

	// Establish persistent stream connection to server for receiving workflow requests
	log.Printf("Establishing persistent stream connection to server...")
	err = lbClient.StartStream()
	if err != nil {
		log.Fatalf("Failed to establish stream connection: %v", err)
	}

	log.Printf("Stream connection established. Worker ready for workflow executions...")

	// Remove the separate gRPC server since we're now using the stream for communication
	// go startWorkerGRPCServer(cfg, workflowEngine, client)

	// Give the gRPC server a moment to start
	time.Sleep(1 * time.Second)

	// Worker is now ready - it will accept stream connections from server when workflows start
	log.Printf("Worker ready and waiting for workflow execution requests...")

	// Wait for interrupt signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	log.Printf("Worker started successfully. Waiting for workflows...")
	<-sigCh

	log.Printf("Shutting down worker...")
}

// startWorkerGRPCServer starts the gRPC server for receiving workflow execution requests
func startWorkerGRPCServer(cfg *config.Config, workflowEngine *engine.WorkflowEngine, client *grpcClient.Client) {
	log.Printf("Attempting to start gRPC server on port %s", cfg.Worker.Port)

	// Parse the worker port from endpoint
	lis, err := net.Listen("tcp", ":"+cfg.Worker.Port)
	if err != nil {
		log.Fatalf("Failed to listen on port %s: %v", cfg.Worker.Port, err)
	}

	log.Printf("Successfully bound to port %s", cfg.Worker.Port)

	// Create gRPC server
	grpcServer := grpc.NewServer()

	// Create and register worker server
	workerServer := grpcClient.NewWorkerServer(workflowEngine, client, cfg)
	pb.RegisterWorkerServiceServer(grpcServer, workerServer)

	log.Printf("Worker gRPC server listening on port %s", cfg.Worker.Port)

	// Start serving
	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Failed to serve gRPC server: %v", err)
	}
}
