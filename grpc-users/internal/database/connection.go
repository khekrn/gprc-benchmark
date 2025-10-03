package database

import (
	"context"
	"fmt"
	"time"

	"coding2fun.in/grpc-users/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
)

func NewConnection(cfg *config.DatabaseConfig) (*pgxpool.Pool, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		cfg.Host, cfg.Port, cfg.User, cfg.Password, cfg.DBName, cfg.SSLMode,
	)

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database config: %w", err)
	}

	// Configure connection pool
	poolConfig.MaxConns = 10
	poolConfig.MinConns = 2
	poolConfig.MaxConnLifetime = time.Hour
	poolConfig.MaxConnIdleTime = time.Minute * 30

	pool, err := pgxpool.NewWithConfig(context.Background(), poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test the connection
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Set up schema
	if err := setupSchema(ctx, pool, cfg.Schema); err != nil {
		return nil, fmt.Errorf("failed to setup schema: %w", err)
	}

	return pool, nil
}

func GetDSN(cfg *config.DatabaseConfig) string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		cfg.User, cfg.Password, cfg.Host, cfg.Port, cfg.DBName, cfg.SSLMode,
	)
}

func GetDSNWithSchema(cfg *config.DatabaseConfig) string {
	dsn := GetDSN(cfg)
	if cfg.Schema != "" && cfg.Schema != "public" {
		dsn += fmt.Sprintf("&search_path=%s", cfg.Schema)
	}
	return dsn
}

func setupSchema(ctx context.Context, pool *pgxpool.Pool, schema string) error {
	if schema == "" || schema == "public" {
		// No need to create public schema, it exists by default
		return nil
	}

	// Create schema if it doesn't exist
	createSchemaQuery := fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, schema)
	if _, err := pool.Exec(ctx, createSchemaQuery); err != nil {
		return fmt.Errorf("failed to create schema %s: %w", schema, err)
	}

	// Set search_path for this connection pool
	setSearchPathQuery := fmt.Sprintf(`SET search_path TO "%s", public`, schema)
	if _, err := pool.Exec(ctx, setSearchPathQuery); err != nil {
		return fmt.Errorf("failed to set search_path to %s: %w", schema, err)
	}

	return nil
}

// NewConnectionWithLogger creates a new database connection with logger
func NewConnectionWithLogger(cfg *config.DatabaseConfig, logger *zap.Logger) (*pgxpool.Pool, error) {
	logger.Info("Connecting to database",
		zap.String("host", cfg.Host),
		zap.Int("port", cfg.Port),
		zap.String("database", cfg.DBName),
		zap.String("schema", cfg.Schema),
		zap.String("user", cfg.User))

	pool, err := NewConnection(cfg)
	if err != nil {
		return nil, err
	}

	logger.Info("Successfully connected to database with schema", zap.String("schema", cfg.Schema))
	return pool, nil
}
