// go-gorm is a Go gRPC server for bench.v1.CommandService whose data layer is
// the **GORM** ORM (gorm.io/gorm) over the **jackc/pgx** driver
// (gorm.io/driver/postgres uses pgx/v5's stdlib driver internally). It is the
// "ORM cost on Go" counterpart to go-pgx: same gRPC contract, same SQL/tables,
// same pgx wire — only the data-access layer differs (GORM `Create()` /
// `Transaction()` vs hand-written pgx `QueryRow`).
//
// Like the other stacks the logic is split across small files (config, fnv,
// model, db, service, server) rather than one main.go — `main` owns only the
// bootstrap and startup sequence.
package main

import (
	"log/slog"
	"os"
	"runtime"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	// Respect the 2-core constraint. The run script also sets GOMAXPROCS, but
	// default it here too for safety.
	if os.Getenv("GOMAXPROCS") == "" {
		runtime.GOMAXPROCS(2)
	}

	if err := run(); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

// run wires config -> GORM/pgx pool -> gRPC server and serves until a signal
// drains in-flight RPCs. Kept separate from main so `defer` (pool close) fires
// on every exit path.
func run() error {
	cfg := ConfigFromEnv()

	db, err := ConnectDB(cfg)
	if err != nil {
		return err
	}
	defer db.Close()

	return Serve(cfg, db)
}
