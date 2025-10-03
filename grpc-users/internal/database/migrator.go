package database

import (
	"context"
	"fmt"
	"time"

	"coding2fun.in/grpc-users/internal/config"
	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"
)

type Migrator struct {
	logger *zap.Logger
	cfg    *config.DatabaseConfig
}

func NewMigrator(cfg *config.DatabaseConfig, logger *zap.Logger) *Migrator {
	return &Migrator{
		cfg:    cfg,
		logger: logger,
	}
}

func (m *Migrator) Up() error {
	// Create schema first if not public
	if err := m.ensureSchemaExists(); err != nil {
		return fmt.Errorf("failed to ensure schema exists: %w", err)
	}

	dsn := GetDSNWithSchema(m.cfg)

	// Create migrate instance
	migration, err := migrate.New(
		"file://internal/database/migrations",
		dsn,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer migration.Close()

	m.logger.Info("Starting database migrations...",
		zap.String("schema", m.cfg.Schema),
		zap.String("database", m.cfg.DBName))

	// Get current version
	currentVersion, dirty, err := migration.Version()
	if err != nil && err != migrate.ErrNilVersion {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	if dirty {
		m.logger.Warn("Database is in dirty state, attempting to force version",
			zap.Uint("version", currentVersion),
			zap.String("schema", m.cfg.Schema))
		if err := migration.Force(int(currentVersion)); err != nil {
			return fmt.Errorf("failed to force migration version: %w", err)
		}
	}

	// Run migrations
	if err := migration.Up(); err != nil {
		if err == migrate.ErrNoChange {
			m.logger.Info("No new migrations to apply", zap.String("schema", m.cfg.Schema))
			return nil
		}
		return fmt.Errorf("failed to run migrations: %w", err)
	}

	// Get new version
	newVersion, _, err := migration.Version()
	if err != nil {
		return fmt.Errorf("failed to get new migration version: %w", err)
	}

	m.logger.Info("Database migrations completed successfully",
		zap.String("schema", m.cfg.Schema),
		zap.Uint("from_version", currentVersion),
		zap.Uint("to_version", newVersion))

	return nil
}

func (m *Migrator) Down() error {
	dsn := GetDSNWithSchema(m.cfg)

	migration, err := migrate.New(
		"file://internal/database/migrations",
		dsn,
	)
	if err != nil {
		return fmt.Errorf("failed to create migrate instance: %w", err)
	}
	defer migration.Close()

	m.logger.Info("Rolling back database migrations...", zap.String("schema", m.cfg.Schema))

	if err := migration.Down(); err != nil {
		if err == migrate.ErrNoChange {
			m.logger.Info("No migrations to rollback", zap.String("schema", m.cfg.Schema))
			return nil
		}
		return fmt.Errorf("failed to rollback migrations: %w", err)
	}

	m.logger.Info("Database migrations rolled back successfully", zap.String("schema", m.cfg.Schema))
	return nil
}

func (m *Migrator) WaitForDatabase(maxRetries int, retryInterval time.Duration) error {
	m.logger.Info("Waiting for database to be ready...",
		zap.Int("max_retries", maxRetries),
		zap.Duration("retry_interval", retryInterval),
		zap.String("schema", m.cfg.Schema))

	for i := 0; i < maxRetries; i++ {
		dsn := GetDSN(m.cfg) // Use basic DSN for connection test
		migration, err := migrate.New(
			"file://internal/database/migrations",
			dsn,
		)
		if err == nil {
			migration.Close()
			m.logger.Info("Database is ready", zap.String("schema", m.cfg.Schema))
			return nil
		}

		m.logger.Warn("Database not ready, retrying...",
			zap.Int("attempt", i+1),
			zap.String("schema", m.cfg.Schema),
			zap.Error(err))

		time.Sleep(retryInterval)
	}

	return fmt.Errorf("database not ready after %d attempts", maxRetries)
}

func (m *Migrator) ensureSchemaExists() error {
	if m.cfg.Schema == "" || m.cfg.Schema == "public" {
		return nil // Public schema always exists
	}

	// Create a temporary connection to create the schema
	tempPool, err := NewConnection(&config.DatabaseConfig{
		Host:     m.cfg.Host,
		Port:     m.cfg.Port,
		User:     m.cfg.User,
		Password: m.cfg.Password,
		DBName:   m.cfg.DBName,
		Schema:   "public", // Connect to public schema first
		SSLMode:  m.cfg.SSLMode,
	})
	if err != nil {
		return fmt.Errorf("failed to create temporary connection: %w", err)
	}
	defer tempPool.Close()

	// Create schema if it doesn't exist
	createSchemaQuery := fmt.Sprintf(`CREATE SCHEMA IF NOT EXISTS "%s"`, m.cfg.Schema)
	_, err = tempPool.Exec(context.Background(), createSchemaQuery)
	if err != nil {
		return fmt.Errorf("failed to create schema %s: %w", m.cfg.Schema, err)
	}

	m.logger.Info("Schema ensured", zap.String("schema", m.cfg.Schema))
	return nil
}
