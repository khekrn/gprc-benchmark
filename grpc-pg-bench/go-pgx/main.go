// Command server is the Go side of the benchmark.
//
// It exposes the same bench.v1.CommandService as the Kotlin/Vert.x service:
// on each Execute call it does a tiny CPU touch (FNV-1a checksum of the
// payload) and inserts one row into Postgres using jackc/pgx (pgxpool).
//
// Concurrency is bounded by GOMAXPROCS (set to 2 by the run script) and the
// pgx pool size; gRPC itself serves each call on its own goroutine.
//
// Production-relevant pieces:
//   - SIGINT/SIGTERM triggers grpcServer.GracefulStop so in-flight RPCs finish
//     before the process exits — the benchmark orchestrator relies on this.
//   - gRPC keepalive (server enforcement) so misbehaving clients don't pin
//     half-open connections during a long sweep.
//   - grpc.health.v1 service registered, useful both for orchestration and
//     for clients that probe readiness.
//   - pgxpool tuned with MaxConnLifetime / MaxConnIdleTime / HealthCheckPeriod
//     so stale connections don't linger between runs.
//   - slog for structured logs; one record per lifecycle event, no per-RPC
//     logging (would skew the benchmark).
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	benchv1 "github.com/beam/grpc-pg-bench/gen/benchv1"

	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

// FNV-1a 32-bit constants. Inlined to avoid the per-call hash.Hash32
// interface allocation that hash/fnv.New32a forces.
const (
	fnvOffset32 uint32 = 2166136261
	fnvPrime32  uint32 = 16777619
)

const insertSQL = `
INSERT INTO commands (workflow_id, command_type, payload, seq, checksum)
VALUES ($1, $2, $3, $4, $5)
RETURNING id`

type server struct {
	benchv1.UnimplementedCommandServiceServer
	pool *pgxpool.Pool
}

// Execute is the hot path. Keep allocations and indirection minimal.
func (s *server) Execute(ctx context.Context, req *benchv1.CommandRequest) (*benchv1.CommandResponse, error) {
	recv := time.Now().UnixMicro()

	// "Small processing": FNV-1a over the payload bytes. Inlined over the
	// string to skip both the hash.Hash32 interface alloc and the
	// []byte(req.Payload) copy that hash/fnv would force.
	payload := req.Payload
	checksum := fnvOffset32
	for i := 0; i < len(payload); i++ {
		checksum ^= uint32(payload[i])
		checksum *= fnvPrime32
	}

	var id int64
	err := s.pool.QueryRow(ctx, insertSQL,
		req.WorkflowId,
		req.CommandType,
		payload,
		req.Seq,
		int64(checksum),
	).Scan(&id)
	if err != nil {
		return nil, err
	}

	return &benchv1.CommandResponse{
		Id:               id,
		Checksum:         checksum,
		ReceivedAtMicros: recv,
	}, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envIntOr(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func run() error {
	// Respect the 2-core constraint. The run script also sets GOMAXPROCS,
	// but we default it here too for safety.
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(2)
	}

	dsn := envOr("DATABASE_URL",
		"postgres://bench:bench@127.0.0.1:5432/bench?sslmode=disable")
	addr := envOr("LISTEN_ADDR", "127.0.0.1:50051")
	poolMax := envIntOr("PG_POOL_MAX", 16)
	poolMin := envIntOr("PG_POOL_MIN", 4)

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse dsn: %w", err)
	}
	cfg.MaxConns = int32(poolMax)
	cfg.MinConns = int32(poolMin)
	// Recycle connections occasionally so PG-side restarts / parameter
	// changes don't leave stale handles in the pool. Numbers picked to be
	// large enough that they never fire during a 30s benchmark phase.
	cfg.MaxConnLifetime = 30 * time.Minute
	cfg.MaxConnIdleTime = 5 * time.Minute
	cfg.HealthCheckPeriod = 30 * time.Second
	// pgx v5 statement cache is on by default. Mirrored on Vert.x via
	// setCachePreparedStatements(true).

	rootCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.NewWithConfig(rootCtx, cfg)
	if err != nil {
		return fmt.Errorf("create pool: %w", err)
	}
	defer pool.Close()

	pingCtx, pingCancel := context.WithTimeout(rootCtx, 5*time.Second)
	pingErr := pool.Ping(pingCtx)
	pingCancel()
	if pingErr != nil {
		return fmt.Errorf("ping db: %w", pingErr)
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", addr, err)
	}

	grpcServer := grpc.NewServer(
		// Server keepalive: ping idle connections every 30s, fail at 10s
		// without a pong. Without this, half-open TCP connections (e.g. a
		// client gone away unceremoniously) would tie up a slot until the
		// kernel's much-longer TCP timeout fires.
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		// Enforce a floor on how often clients are allowed to ping. The
		// benchmark client is well-behaved; this prevents abusive clients
		// from triggering keepalive storms during a long run.
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             10 * time.Second,
			PermitWithoutStream: true,
		}),
		// HTTP/2 flow-control windows. Defaults (64 KiB stream / 64 KiB conn)
		// throttle short-RPC throughput at high concurrency because the
		// client has to wait for WINDOW_UPDATE frames between batches. 1 MiB
		// each removes flow-control as a serializer for the benchmark.
		grpc.InitialWindowSize(1<<20),
		grpc.InitialConnWindowSize(1<<20),
		// Larger transport read/write buffers reduce the number of
		// syscalls per stream under load.
		grpc.ReadBufferSize(64<<10),
		grpc.WriteBufferSize(64<<10),
		// Share the per-stream write buffer across active streams on the
		// same connection — lowers per-call allocation overhead.
		grpc.SharedWriteBuffer(true),
		// Bounded pool of stream workers. Default (0) spawns a fresh
		// goroutine per stream; pinning a small pool amortizes goroutine
		// setup over the lifetime of the benchmark. Sized at 4x GOMAXPROCS
		// so PG-blocked workers don't head-of-line block CPU-ready ones.
		grpc.NumStreamWorkers(uint32(runtime.GOMAXPROCS(0))*4),
	)
	benchv1.RegisterCommandServiceServer(grpcServer, &server{pool: pool})

	// Standard gRPC health service. The orchestrator could probe this if
	// it wanted readiness-gated start; for now it's wired up for parity
	// with what we'd actually ship.
	healthSrv := health.NewServer()
	healthSrv.SetServingStatus(benchv1.CommandService_ServiceDesc.ServiceName, healthpb.HealthCheckResponse_SERVING)
	healthSrv.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthpb.RegisterHealthServer(grpcServer, healthSrv)

	slog.Info("go-pgx server listening",
		"addr", addr,
		"pool_min", poolMin,
		"pool_max", poolMax,
		"gomaxprocs", runtime.GOMAXPROCS(0),
	)

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- grpcServer.Serve(lis)
	}()

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-rootCtx.Done():
		slog.Info("shutdown signal received, draining in-flight RPCs")
	}

	// GracefulStop waits for in-flight RPCs to complete but stops accepting
	// new ones. With a hard ceiling so a wedged client can't block forever.
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

	// Drain whatever Serve returned (will be ErrServerStopped after Stop).
	if err := <-serveErr; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
		return fmt.Errorf("serve: %w", err)
	}
	return nil
}
