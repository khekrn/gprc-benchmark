// Package migrate applies the idempotent schema on startup. For a real system
// prefer a dedicated migration tool (golang-migrate, goose); this mirrors the
// Kotlin DbMigrator which runs the same SQL.
package migrate

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5/pgxpool"
)

//go:embed schema.sql
var schemaSQL string

func Run(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, schemaSQL)
	return err
}
