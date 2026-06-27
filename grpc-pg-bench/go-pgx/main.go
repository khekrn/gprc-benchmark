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
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"runtime/pprof"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	benchv1 "github.com/beam/grpc-pg-bench/gen/benchv1"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
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

// SQL kept byte-identical to kotlin-vertx/.../Db.kt so the two stacks
// hit the planner with the same prepared-statement text.
const insertCommandSQL = `INSERT INTO commands (workflow_id, command_type, payload, seq, checksum) VALUES ($1, $2, $3, $4, $5) RETURNING id`

const upsertStateSQL = `INSERT INTO workflow_state (workflow_id, state, version, updated_at) VALUES ($1, $2, 1, now()) ON CONFLICT (workflow_id) DO UPDATE SET state = EXCLUDED.state, version = workflow_state.version + 1, updated_at = now()`

const insertOutboxSQL = `INSERT INTO outbox (workflow_id, event_type, payload) VALUES ($1, $2, $3)`

const selectStateSQL = `SELECT state, version, (EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT AS updated_at_micros FROM workflow_state WHERE workflow_id = $1`

type server struct {
	benchv1.UnimplementedCommandServiceServer
	pool *pgxpool.Pool
	// skipRecvTs (env SKIP_RECV_TS=1) drops the per-call
	// time.Now().UnixMicro() that fills ReceivedAtMicros. The benchmark
	// loadgen never reads that field (it times each RPC client-side), and on
	// a 2-core box that time.Now() shows up at a few % of the Execute CPU.
	// OFF by default so the production response stays fully populated.
	skipRecvTs bool
	// rdb is the Redis read-through cache for workflow_state. nil unless
	// REDIS_ENABLED=true, in which case GetState reads through it (the Go twin
	// of spring-vt's RedisStateCache, for the runtime A/B). Same key format
	// (wf:state:{id}) and compact value (version|micros|state) as spring-vt.
	rdb           *redis.Client
	cacheTTL      time.Duration
	lastRedisWarn atomic.Int64 // ns of last warn, throttles a Redis-outage log to 1/s
}

func stateKey(workflowID string) string { return "wf:state:" + workflowID }

// encodeState packs the row as version|micros|state (state last so it may
// contain the delimiter), byte-identical to spring-vt's RedisStateCache format.
func encodeState(version, micros int64, state string) string {
	return strconv.FormatInt(version, 10) + "|" + strconv.FormatInt(micros, 10) + "|" + state
}

// decodeState parses an encodeState value; ok=false on malformed (treated as miss).
func decodeState(workflowID, v string) (*benchv1.StateResponse, bool) {
	p1 := strings.IndexByte(v, '|')
	if p1 < 0 {
		return nil, false
	}
	rest := v[p1+1:]
	p2 := strings.IndexByte(rest, '|')
	if p2 < 0 {
		return nil, false
	}
	version, err1 := strconv.ParseInt(v[:p1], 10, 64)
	micros, err2 := strconv.ParseInt(rest[:p2], 10, 64)
	if err1 != nil || err2 != nil {
		return nil, false
	}
	return &benchv1.StateResponse{
		Found:           true,
		WorkflowId:      workflowID,
		State:           rest[p2+1:],
		Version:         version,
		UpdatedAtMicros: micros,
	}, true
}

// warnRedis logs at most one WARN/second so a Redis outage can't flood the log.
func (s *server) warnRedis(op string, err error) {
	now := time.Now().UnixNano()
	prev := s.lastRedisWarn.Load()
	if now-prev > int64(time.Second) && s.lastRedisWarn.CompareAndSwap(prev, now) {
		slog.Warn("redis op failed, degrading to postgres", "op", op, "err", err)
	}
}

// fnv1a returns FNV-1a 32 over s, inlined over the string to avoid both the
// hash.Hash32 interface alloc and the []byte(s) copy that hash/fnv would force.
func fnv1a(s string) uint32 {
	h := fnvOffset32
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= fnvPrime32
	}
	return h
}

// Execute is the original single-INSERT autocommit hot path.
func (s *server) Execute(ctx context.Context, req *benchv1.CommandRequest) (*benchv1.CommandResponse, error) {
	var recv int64
	if !s.skipRecvTs {
		recv = time.Now().UnixMicro()
	}
	checksum := fnv1a(req.Payload)

	var id int64
	err := s.pool.QueryRow(ctx, insertCommandSQL,
		req.WorkflowId,
		req.CommandType,
		req.Payload,
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

// ExecuteTx runs the three statements (INSERT command + UPSERT state +
// INSERT outbox) atomically inside one transaction.
//
// Each statement is sent and awaited *sequentially* — BEGIN, INSERT, UPSERT,
// INSERT, COMMIT — i.e. five separate round trips to Postgres. This is
// deliberately NOT pipelined via pgx.Batch: the JVM stacks driven by JDBC on
// virtual threads (spring-vt, quarkus-vt) physically cannot pipeline a
// transaction (JDBC is one statement per round trip), so the only model all
// five stacks can share is sequential statements. Matching kotlin-vertx's
// per-statement transaction here keeps ExecuteTx an apples-to-apples
// comparison across every stack rather than handing pgx a wire-cost advantage
// no JDBC stack can match.
func (s *server) ExecuteTx(ctx context.Context, req *benchv1.CommandRequest) (*benchv1.CommandResponse, error) {
	recv := time.Now().UnixMicro()
	checksum := fnv1a(req.Payload)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	// Best-effort rollback on any error path. Commit() makes Rollback a no-op,
	// so an unconditional defer is safe here.
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback(ctx)
		}
	}()

	var id int64
	if err := tx.QueryRow(ctx, insertCommandSQL,
		req.WorkflowId, req.CommandType, req.Payload, req.Seq, int64(checksum),
	).Scan(&id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, upsertStateSQL, req.WorkflowId, req.CommandType); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, insertOutboxSQL, req.WorkflowId, req.CommandType, req.Payload); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	committed = true

	// workflow_state changed → evict the cached entry (cache-aside on write);
	// next GetState repopulates from PG. Post-commit so a rolled-back TX never
	// evicts. Best-effort; TTL is the backstop if this DEL is lost.
	if s.rdb != nil {
		if err := s.rdb.Del(ctx, stateKey(req.WorkflowId)).Err(); err != nil {
			s.warnRedis("del", err)
		}
	}

	return &benchv1.CommandResponse{
		Id:               id,
		Checksum:         checksum,
		ReceivedAtMicros: recv,
	}, nil
}

// GetState is the dominant read shape: single SELECT by primary key, read
// THROUGH the Redis cache when enabled. updated_at is converted to micros
// server-side to skip TIMESTAMPTZ marshal.
func (s *server) GetState(ctx context.Context, req *benchv1.GetStateRequest) (*benchv1.StateResponse, error) {
	// Cache lookup. A Redis error degrades to Postgres (never fails the RPC).
	if s.rdb != nil {
		v, err := s.rdb.Get(ctx, stateKey(req.WorkflowId)).Result()
		if err == nil {
			if resp, ok := decodeState(req.WorkflowId, v); ok {
				return resp, nil
			}
		} else if err != redis.Nil {
			s.warnRedis("get", err)
		}
	}

	var state string
	var version int64
	var updatedAtMicros int64
	err := s.pool.QueryRow(ctx, selectStateSQL, req.WorkflowId).
		Scan(&state, &version, &updatedAtMicros)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return &benchv1.StateResponse{Found: false, WorkflowId: req.WorkflowId}, nil
		}
		return nil, err
	}
	// Populate the cache for existing rows (misses are not cached). Best-effort.
	if s.rdb != nil {
		if err := s.rdb.Set(ctx, stateKey(req.WorkflowId),
			encodeState(version, updatedAtMicros, state), s.cacheTTL).Err(); err != nil {
			s.warnRedis("set", err)
		}
	}
	return &benchv1.StateResponse{
		Found:           true,
		WorkflowId:      req.WorkflowId,
		State:           state,
		Version:         version,
		UpdatedAtMicros: updatedAtMicros,
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

	// --- Opt-in profiling (OFF unless the env var is set). None of this runs
	// on the production path; it exists only for the performance-tuning study.
	//
	//   PPROF_ADDR=127.0.0.1:6060   -> serves net/http/pprof (CPU/heap/block/
	//                                  mutex/goroutine) on that address.
	//   CPUPROFILE=/path/cpu.pprof  -> writes a CPU profile for the process
	//                                  lifetime (stopped on graceful shutdown).
	//   BLOCKPROFILE_RATE=1         -> runtime.SetBlockProfileRate(n) so the
	//                                  /debug/pprof/block profile captures
	//                                  off-CPU waits (pool-acquire, lock waits).
	//   MUTEXPROFILE_FRACTION=1     -> runtime.SetMutexProfileFraction(n) for
	//                                  the /debug/pprof/mutex profile.
	if r := envIntOr("BLOCKPROFILE_RATE", 0); r > 0 {
		runtime.SetBlockProfileRate(r)
		slog.Info("block profiling enabled", "rate", r)
	}
	if f := envIntOr("MUTEXPROFILE_FRACTION", 0); f > 0 {
		runtime.SetMutexProfileFraction(f)
		slog.Info("mutex profiling enabled", "fraction", f)
	}
	if addr := os.Getenv("PPROF_ADDR"); addr != "" {
		go func() {
			slog.Info("pprof http server listening", "addr", addr)
			if err := http.ListenAndServe(addr, nil); err != nil {
				slog.Warn("pprof http server stopped", "err", err)
			}
		}()
	}
	if path := os.Getenv("CPUPROFILE"); path != "" {
		f, err := os.Create(path)
		if err != nil {
			return fmt.Errorf("create cpuprofile: %w", err)
		}
		if err := pprof.StartCPUProfile(f); err != nil {
			f.Close()
			return fmt.Errorf("start cpuprofile: %w", err)
		}
		slog.Info("cpu profiling enabled", "path", path)
		defer func() {
			pprof.StopCPUProfile()
			f.Close()
			slog.Info("cpu profile written", "path", path)
		}()
	}

	// Co-hosted REST /health (net/http) — fairness with spring-vt's Jetty and
	// rust-tokio's axum. Served on a separate port on the same goroutine runtime
	// (GOMAXPROCS), so the HTTP and gRPC surfaces contend for the same cores.
	// Opt-in via HTTP_PORT (unset => gRPC-only, the original behaviour). A
	// dedicated mux (not DefaultServeMux) so it never exposes the pprof handlers.
	if httpPort := os.Getenv("HTTP_PORT"); httpPort != "" {
		mux := http.NewServeMux()
		mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("UP"))
		})
		go func() {
			slog.Info("co-hosted REST /health listening", "port", httpPort)
			if err := http.ListenAndServe("0.0.0.0:"+httpPort, mux); err != nil {
				slog.Warn("health http server stopped", "err", err)
			}
		}()
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
	//
	// PGX_NO_EXPIRY=1 (opt-in tuning knob) zeroes MaxConnLifetime/IdleTime so
	// pgxpool skips the per-acquire/per-release isExpired() time.Now() calls
	// (visible at a few % of CPU on a 2-core box). Default keeps the
	// production-safe recycling behavior.
	if os.Getenv("PGX_NO_EXPIRY") == "1" {
		// Zeroing lifetime/idle disables the per-acquire & per-release
		// isExpired() time.Now() calls. HealthCheckPeriod must stay > 0:
		// pgxpool panics (non-positive NewTicker interval) on 0, and with
		// lifetime/idle zeroed the background check has nothing to expire,
		// so its cost is negligible.
		cfg.MaxConnLifetime = 0
		cfg.MaxConnIdleTime = 0
		cfg.HealthCheckPeriod = 1 * time.Minute
	} else {
		cfg.MaxConnLifetime = 30 * time.Minute
		cfg.MaxConnIdleTime = 5 * time.Minute
		cfg.HealthCheckPeriod = 30 * time.Second
	}
	// pgx v5 statement cache is on by default. Mirrored on Vert.x via
	// setCachePreparedStatements(true).
	//
	// PGX_EXEC_MODE (opt-in tuning knob; default = pgx default
	// QueryExecModeCacheStatement): "describe" switches to
	// QueryExecModeCacheDescribe (cache the type oids, let the server
	// auto-prepare). Lets the A/B study compare the two without a rebuild.
	switch os.Getenv("PGX_EXEC_MODE") {
	case "describe":
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheDescribe
	case "exec":
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	case "simple":
		cfg.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}

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

	// Optional Redis read-through cache for GetState (REDIS_ENABLED=true). The
	// Go twin of spring-vt's RedisStateCache, for the runtime A/B. go-redis is
	// the idiomatic Go client (pooled, synchronous) — the analog of Lettuce.
	// Pool sized to the read concurrency so it doesn't head-of-line block reads
	// (Lettuce multiplexes one connection; go-redis pools, so it needs ~conc
	// connections to match). 500ms timeouts mirror spring.data.redis.timeout.
	var rdb *redis.Client
	cacheTTL := time.Duration(envIntOr("REDIS_TTL_SECONDS", 300)) * time.Second
	if os.Getenv("REDIS_ENABLED") == "true" {
		rdb = redis.NewClient(&redis.Options{
			Addr:         envOr("REDIS_HOST", "127.0.0.1") + ":" + envOr("REDIS_PORT", "6379"),
			PoolSize:     envIntOr("REDIS_POOL_SIZE", 32),
			ReadTimeout:  500 * time.Millisecond,
			WriteTimeout: 500 * time.Millisecond,
		})
		rPingCtx, rPingCancel := context.WithTimeout(rootCtx, 5*time.Second)
		rPingErr := rdb.Ping(rPingCtx).Err()
		rPingCancel()
		if rPingErr != nil {
			return fmt.Errorf("ping redis: %w", rPingErr)
		}
		defer rdb.Close()
		slog.Info("redis read-through cache enabled",
			"addr", rdb.Options().Addr, "pool_size", rdb.Options().PoolSize, "ttl", cacheTTL)
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
		// GRPC_STREAM_WORKERS overrides for the A/B study (0 = grpc default,
		// fresh goroutine per stream).
		grpc.NumStreamWorkers(uint32(envIntOr("GRPC_STREAM_WORKERS", runtime.GOMAXPROCS(0)*4))),
	)
	benchv1.RegisterCommandServiceServer(grpcServer, &server{
		pool:       pool,
		skipRecvTs: os.Getenv("SKIP_RECV_TS") == "1",
		rdb:        rdb,
		cacheTTL:   cacheTTL,
	})

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
