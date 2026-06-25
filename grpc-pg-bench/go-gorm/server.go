package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	benchv1 "github.com/beam/grpc-pg-bench/gen/benchv1"

	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

// Serve builds the grpc-go server with the same transport tuning as go-pgx
// (so the only difference between the two stacks is GORM vs hand-written pgx),
// registers the service + health, and serves until SIGINT/SIGTERM drains
// in-flight RPCs.
func Serve(cfg Config, db *Db) error {
	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	lis, err := net.Listen("tcp", cfg.ListenAddr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", cfg.ListenAddr, err)
	}

	grpcServer := grpc.NewServer(
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		// 1 MiB HTTP/2 windows (vs 64 KiB default) so short RPCs don't stall on
		// WINDOW_UPDATE frames at high concurrency — matches go-pgx.
		grpc.InitialWindowSize(1<<20),
		grpc.InitialConnWindowSize(1<<20),
		grpc.ReadBufferSize(64<<10),
		grpc.WriteBufferSize(64<<10),
		grpc.SharedWriteBuffer(true),
		// Bounded stream-worker pool sized to 4x GOMAXPROCS, same as go-pgx.
		grpc.NumStreamWorkers(uint32(runtime.GOMAXPROCS(0))*4),
	)
	benchv1.RegisterCommandServiceServer(grpcServer, &commandService{db: db})

	// Standard gRPC health service — wired for parity with what we'd ship.
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus(benchv1.CommandService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthSrv)

	slog.Info("go-gorm server listening",
		"addr", cfg.ListenAddr,
		"pool_min", cfg.PoolMin,
		"pool_max", cfg.PoolMax,
		"gomaxprocs", runtime.GOMAXPROCS(0),
	)

	serveErr := make(chan error, 1)
	go func() { serveErr <- grpcServer.Serve(lis) }()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-rootCtx.Done():
		slog.Info("shutdown signal received, draining in-flight RPCs")
	}

	// GracefulStop with a hard 15s ceiling so a wedged client can't hang the
	// orchestrator (matches go-pgx).
	done := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("graceful stop complete")
	case <-time.After(15 * time.Second):
		slog.Warn("graceful stop timed out, forcing stop")
		grpcServer.Stop()
		<-done
	}

	if err := <-serveErr; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
