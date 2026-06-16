// Package db wires the jackc/pgx connection pool and the repository.
package db

import (
	"context"
	"fmt"

	"github.com/example/users-go/internal/config"
	"github.com/jackc/pgx/v5/pgxpool"
)

// ConnString builds a libpq-style URL from the DB config. sslmode=disable is
// fine for local/dev; set it via the URL or DB options for production.
func ConnString(c config.DB) string {
	return fmt.Sprintf("postgres://%s:%s@%s:%d/%s?sslmode=disable",
		c.User, c.Password, c.Host, c.Port, c.Database)
}

// NewPool builds a pgx pool. pgx caches prepared statements per connection by
// default (QueryExecModeCacheStatement) — the analogue of the Vert.x
// prepared-statement cache — and pipelines a Batch over one connection.
func NewPool(ctx context.Context, c config.DB) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(ConnString(c))
	if err != nil {
		return nil, err
	}
	if c.PoolMaxSize > 0 {
		poolCfg.MaxConns = c.PoolMaxSize
	}
	return pgxpool.NewWithConfig(ctx, poolCfg)
}
