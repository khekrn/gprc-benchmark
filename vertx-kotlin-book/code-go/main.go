// Command users-go is the Go port of the Kotlin/Vert.x users service: the same
// REST + gRPC surface, backed by jackc/pgx. Composition root — everything
// wires up here, top to bottom, no DI framework.
package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	usersv1 "github.com/example/users-go/gen/usersv1"
	"github.com/example/users-go/internal/config"
	"github.com/example/users-go/internal/db"
	"github.com/example/users-go/internal/grpcserver"
	"github.com/example/users-go/internal/httpserver"
	"github.com/example/users-go/internal/migrate"
	"github.com/example/users-go/internal/observability"
	"github.com/example/users-go/internal/service"
	"github.com/gofiber/fiber/v3"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(log); err != nil {
		log.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(log *slog.Logger) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	log.Info("config loaded",
		"http", cfg.HTTP.Port, "grpc", cfg.GRPC.Port,
		"db", cfg.DB.Host+":"+strconv.Itoa(cfg.DB.Port)+"/"+cfg.DB.Database)

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startCtx, cancel := context.WithTimeout(rootCtx, 15*time.Second)
	defer cancel()

	pool, err := db.NewPool(startCtx, cfg.DB)
	if err != nil {
		return err
	}
	defer pool.Close()

	if cfg.DB.SchemaOnStartup {
		if err := migrate.Run(startCtx, pool); err != nil {
			return err
		}
		log.Info("schema applied")
	}

	repo := db.NewRepository(pool, db.ConnString(cfg.DB))
	svc := service.New(repo)
	metrics := observability.New()

	// ---- gRPC ------------------------------------------------------------
	grpcSrv := grpc.NewServer(
		grpc.ChainUnaryInterceptor(metrics.UnaryServerInterceptor()),
		grpc.ChainStreamInterceptor(metrics.StreamServerInterceptor()),
	)
	usersv1.RegisterUsersServer(grpcSrv, grpcserver.New(svc))
	reflection.Register(grpcSrv) // grpcurl without the .proto

	grpcLn, err := net.Listen("tcp", net.JoinHostPort("", strconv.Itoa(cfg.GRPC.Port)))
	if err != nil {
		return err
	}

	// ---- HTTP (Fiber) ----------------------------------------------------
	httpAddr := net.JoinHostPort(cfg.HTTP.Host, strconv.Itoa(cfg.HTTP.Port))
	fiberApp := httpserver.New(svc, metrics).App()

	errCh := make(chan error, 2)
	go func() {
		log.Info("gRPC server listening", "port", cfg.GRPC.Port)
		if err := grpcSrv.Serve(grpcLn); err != nil {
			errCh <- err
		}
	}()
	go func() {
		log.Info("HTTP server listening", "addr", httpAddr)
		if err := fiberApp.Listen(httpAddr, fiber.ListenConfig{DisableStartupMessage: true}); err != nil {
			errCh <- err
		}
	}()

	select {
	case <-rootCtx.Done():
		log.Info("shutdown signal received")
	case err := <-errCh:
		log.Error("server error", "err", err)
	}

	// ---- graceful shutdown ----------------------------------------------
	grace := time.Duration(cfg.ShutdownGracePeriodMs) * time.Millisecond
	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), grace)
	defer cancelShutdown()

	grpcSrv.GracefulStop()
	if err := fiberApp.ShutdownWithContext(shutdownCtx); err != nil {
		log.Error("http shutdown error", "err", err)
	}
	log.Info("shutdown complete")
	return nil
}
