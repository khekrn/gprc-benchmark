package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"workflow-engine/internal/config"
	"workflow-engine/internal/database"
	"workflow-engine/internal/handlers"
	redis_client "workflow-engine/internal/redis"
	"workflow-engine/internal/wpool"
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

	log.Printf("Loaded config: Server=%s HTTP=:%d gRPC=:%d", cfg.Server.Name, cfg.Server.HTTP.Port, cfg.Server.GRPC.Port)

	// Initialize database connection
	db, err := database.NewDBFromDSN(cfg.GetDatabaseDSN())
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	log.Println("Connected to database successfully")

	// Initialize Redis client for worker discovery
	redisClient, err := redis_client.NewClient(&cfg.Redis)
	if err != nil {
		log.Fatalf("Failed to connect to Redis: %v", err)
	}
	defer redisClient.Close()

	log.Printf("Connected to Redis at %s:%d", cfg.Redis.Host, cfg.Redis.Port)
	log.Println("Connected to Redis successfully")

	// Initialize worker pool manager for handling multiple workflows
	workerPoolManager := wpool.NewWorkerPoolManager(db, redisClient)
	defer workerPoolManager.Close()

	log.Println("Worker pool manager initialized and monitoring for workers")

	// Initialize HTTP server with worker pool integration
	httpHandler := handlers.NewHTTPHandler(db, workerPoolManager)

	// Start HTTP server
	router := gin.Default()

	// Workflow execution endpoints
	router.POST("/api/v1/workflows/start", httpHandler.StartWorkflow)
	router.POST("/api/v1/workflows/start-sync", httpHandler.StartWorkflowSync) // New sync endpoint
	router.GET("/api/v1/workflows/:id", httpHandler.GetWorkflow)

	// Workflow endpoint management (for testing and configuration)
	router.POST("/api/v1/workflows/endpoints", httpHandler.RegisterWorkflowEndpoints)
	router.GET("/api/v1/workflows/mappings", httpHandler.GetWorkflowMappings)

	// System endpoints
	router.GET("/api/v1/connections", httpHandler.GetConnections)
	router.GET("/api/v1/metrics", httpHandler.GetMetrics)
	router.GET("/health", httpHandler.Health)

	log.Printf("Starting HTTP server on port %d", cfg.Server.HTTP.Port)
	log.Printf("Server name: %s", cfg.Server.Name)

	// Start HTTP server in background
	go func() {
		if err := router.Run(fmt.Sprintf(":%d", cfg.Server.HTTP.Port)); err != nil {
			log.Fatalf("Failed to start HTTP server: %v", err)
		}
	}()

	log.Println("Server started successfully!")
	log.Println("New Architecture:")
	log.Println("- Workers register themselves in Redis when they start")
	log.Println("- Server discovers workers via Redis pub/sub")
	log.Println("- Server establishes connections TO workers (reverse of old pattern)")
	log.Println("- Multiple servers can run - workers are distributed via Redis")

	// Wait for interrupt signal
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	log.Println("Shutting down server...")
}
