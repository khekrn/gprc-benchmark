package main

import (
	"os"
	"strconv"
)

// Config is sourced entirely from environment variables, with defaults that
// match the other stacks (scripts/config.sh). No config file.
type Config struct {
	// libpq-style connection string; pgx (under GORM) parses it.
	DSN string
	// host:port the gRPC server binds to.
	ListenAddr string
	// Upper bound on pooled connections (matches PG_POOL_MAX). Maps to
	// database/sql SetMaxOpenConns.
	PoolMax int
	// Idle connections kept open (matches PG_POOL_MIN). Maps to SetMaxIdleConns.
	PoolMin int
}

func ConfigFromEnv() Config {
	return Config{
		DSN:        envOr("DATABASE_URL", "postgres://postgres:sam@127.0.0.1:5432/bench?sslmode=disable"),
		ListenAddr: envOr("LISTEN_ADDR", "127.0.0.1:50054"),
		PoolMax:    envIntOr("PG_POOL_MAX", 16),
		PoolMin:    envIntOr("PG_POOL_MIN", 4),
	}
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
