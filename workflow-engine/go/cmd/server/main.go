package main

import (
	"flag"
	"log"
	"net"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"

	"workflow-engine/internal/config"
	"workflow-engine/internal/database"
	grpcserver "workflow-engine/internal/grpc"
	"workflow-engine/internal/handlers"
	pb "workflow-engine/proto"
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

	// Initialize database connection
	db, err := database.NewDBFromDSN(cfg.GetDatabaseDSN())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	log.Println("Connected to database successfully")

	// Initialize gRPC server
	grpcServer := grpcserver.NewWorkflowServer(db)

	// Start gRPC server
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		startGRPCServer(cfg.GetGRPCAddress(), grpcServer)
	}()

	// Start HTTP server
	go func() {
		defer wg.Done()
		startHTTPServer(cfg.GetHTTPAddress(), db, grpcServer)
	}()

	log.Println("Workflow Engine Server started successfully")
	log.Printf("HTTP Server running on port %d", cfg.Server.HTTP.Port)
	log.Printf("gRPC Server running on port %d", cfg.Server.GRPC.Port)

	wg.Wait()
}

func startGRPCServer(address string, workflowServer *grpcserver.WorkflowServer) {
	lis, err := net.Listen("tcp", address)
	if err != nil {
		log.Fatalf("Failed to listen on address %s: %v", address, err)
	}

	s := grpc.NewServer()
	pb.RegisterWorkflowServiceServer(s, workflowServer)

	// Enable reflection for testing with grpcurl
	reflection.Register(s)

	log.Printf("gRPC server listening on %s", address)
	if err := s.Serve(lis); err != nil {
		log.Fatalf("Failed to serve gRPC: %v", err)
	}
}

func startHTTPServer(address string, db *database.DB, grpcServer *grpcserver.WorkflowServer) {
	// Initialize HTTP handlers
	httpServer := handlers.NewHTTPServer(db, grpcServer)

	// Setup Gin router
	router := gin.Default()

	// Add middleware
	router.Use(gin.Logger())
	router.Use(gin.Recovery())

	// Health check endpoint
	router.GET("/health", httpServer.HealthCheck)

	// API v1 endpoints
	v1 := router.Group("/api/v1")
	{
		v1.POST("/workflows/start", httpServer.StartWorkflow)
		v1.GET("/workflows/:id", httpServer.GetWorkflowStatus)
		v1.GET("/connections", httpServer.GetActiveConnections)
	}

	log.Printf("HTTP server listening on %s", address)
	if err := http.ListenAndServe(address, router); err != nil {
		log.Fatalf("Failed to serve HTTP: %v", err)
	}
}
