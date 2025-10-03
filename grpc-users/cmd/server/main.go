package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"coding2fun.in/grpc-users/internal/config"
	"coding2fun.in/grpc-users/internal/database"
	handler "coding2fun.in/grpc-users/internal/handlers"
	"coding2fun.in/grpc-users/internal/repository"
	"coding2fun.in/grpc-users/internal/service"
	pb "coding2fun.in/grpc-users/pkg/user/v1"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/logger"
	"github.com/gofiber/fiber/v2/middleware/recover"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	// Load configuration
	cfg, err := config.LoadConfig(".")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Initialize logger
	zapLogger, err := zap.NewProduction()
	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}
	defer zapLogger.Sync()

	// Run database migrations if enabled
	if cfg.Migration.AutoMigrate {
		migrator := database.NewMigrator(&cfg.Database, zapLogger)

		// Wait for database to be ready
		retryInterval := time.Duration(cfg.Migration.RetryInterval) * time.Second
		if err := migrator.WaitForDatabase(cfg.Migration.MaxRetries, retryInterval); err != nil {
			zapLogger.Fatal("Database is not ready", zap.Error(err))
		}

		// Run migrations
		if err := migrator.Up(); err != nil {
			zapLogger.Fatal("Failed to run database migrations", zap.Error(err))
		}
	} else {
		zapLogger.Info("Auto-migration is disabled")
	}

	// Connect to database
	db, err := database.NewConnection(&cfg.Database)
	if err != nil {
		zapLogger.Fatal("Failed to connect to database", zap.Error(err))
	}
	defer db.Close()

	// Initialize repositories
	userRepo := repository.NewUserRepository(db)

	// Initialize services
	userService := service.NewUserService(userRepo, zapLogger)

	// Initialize handlers
	userHandler := handler.NewUserHandler(userService, zapLogger)
	grpcUserHandler := handler.NewGRPCUserHandler(userService, zapLogger)

	// Create a wait group to manage both servers
	var wg sync.WaitGroup

	// Start gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		startGRPCServer(cfg, grpcUserHandler, zapLogger)
	}()

	// Start REST server
	wg.Add(1)
	go func() {
		defer wg.Done()
		startRESTServer(cfg, userHandler, db, zapLogger)
	}()

	// Wait for interrupt signal to gracefully shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	zapLogger.Info("Shutting down servers...")
}

func startGRPCServer(cfg *config.Config, grpcUserHandler *handler.GRPCUserHandler, logger *zap.Logger) {
	grpcServerAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.GRPCPort)
	
	listener, err := net.Listen("tcp", grpcServerAddr)
	if err != nil {
		logger.Fatal("Failed to listen for gRPC server", zap.Error(err))
	}

	// Create gRPC server with options
	grpcServer := grpc.NewServer()
	
	// Register the UserService
	pb.RegisterUserServiceServer(grpcServer, grpcUserHandler)
	
	// Enable gRPC reflection for development/debugging
	reflection.Register(grpcServer)

	logger.Info("Starting gRPC server", zap.String("address", grpcServerAddr))
	
	if err := grpcServer.Serve(listener); err != nil {
		logger.Fatal("Failed to start gRPC server", zap.Error(err))
	}
}

func startRESTServer(cfg *config.Config, userHandler *handler.UserHandler, db interface{ Ping(context.Context) error }, logger *zap.Logger) {
	// Initialize Fiber app
	app := fiber.New(fiber.Config{
		ErrorHandler: func(c *fiber.Ctx, err error) error {
			code := fiber.StatusInternalServerError
			if e, ok := err.(*fiber.Error); ok {
				code = e.Code
			}

			logger.Error("HTTP error",
				zap.String("method", c.Method()),
				zap.String("path", c.Path()),
				zap.Int("status", code),
				zap.Error(err))

			return c.Status(code).JSON(fiber.Map{
				"error": err.Error(),
			})
		},
	})

	// Middleware
	app.Use(recover.New())
	app.Use(logger.New())
	app.Use(cors.New(cors.Config{
		AllowOrigins: "*",
		AllowMethods: "GET,POST,PUT,DELETE,OPTIONS",
		AllowHeaders: "Origin,Content-Type,Accept,Authorization",
	}))

	// Health check endpoint
	app.Get("/health", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{
			"status":    "ok",
			"service":   "grpc-users",
			"timestamp": time.Now().UTC(),
		})
	})

	// Database health check endpoint
	app.Get("/health/db", func(c *fiber.Ctx) error {
		ctx, cancel := context.WithTimeout(c.Context(), 5*time.Second)
		defer cancel()

		if err := db.Ping(ctx); err != nil {
			logger.Error("Database health check failed", zap.Error(err))
			return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
				"status": "unhealthy",
				"error":  "database connection failed",
			})
		}

		return c.JSON(fiber.Map{
			"status":   "healthy",
			"database": "connected",
		})
	})

	// Register routes
	userHandler.RegisterRoutes(app)

	// Start server
	serverAddr := fmt.Sprintf("%s:%d", cfg.Server.Host, cfg.Server.Port)

	logger.Info("Starting REST server",
		zap.String("address", serverAddr),
		zap.Bool("auto_migrate", cfg.Migration.AutoMigrate))
	
	if err := app.Listen(serverAddr); err != nil {
		logger.Fatal("Failed to start REST server", zap.Error(err))
	}
}
