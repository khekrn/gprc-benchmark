package main

import (
	"context"
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// SQL kept byte-identical to go-pgx / the JVM stacks. GORM uses `?` placeholders
// and rewrites them to `$N` for Postgres, so these differ from go-pgx only in
// the placeholder syntax — same statement text reaches the planner.
const upsertStateSQL = `INSERT INTO workflow_state (workflow_id, state, version, updated_at) VALUES (?, ?, 1, now()) ON CONFLICT (workflow_id) DO UPDATE SET state = EXCLUDED.state, version = workflow_state.version + 1, updated_at = now()`

const selectStateSQL = `SELECT state, version, (EXTRACT(EPOCH FROM updated_at) * 1000000)::BIGINT AS updated_at_micros FROM workflow_state WHERE workflow_id = ?`

// Db is the GORM-backed data-access layer.
type Db struct {
	gdb *gorm.DB
}

// ConnectDB opens GORM over the pgx driver and sizes the pool.
//
// gorm.io/driver/postgres uses **github.com/jackc/pgx/v5** (its stdlib driver,
// registered as "pgx") by default — so the wire/driver is the same as go-pgx;
// only the data-access layer (GORM) differs.
func ConnectDB(cfg Config) (*Db, error) {
	gdb, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		// No per-query logging — it would skew the benchmark.
		Logger: logger.Default.LogMode(logger.Silent),
		// Cache prepared statements (server-side prepare). pgx, pgjdbc
		// (prepareThreshold=1) and Vert.x all prepare-and-cache, so enabling
		// this keeps GORM on an even footing rather than re-parsing per call.
		PrepareStmt: true,
		// SkipDefaultTransaction is left FALSE (GORM's default): each Create
		// wraps in BEGIN/COMMIT. That is the honest out-of-the-box GORM write
		// cost — the same transactional-write shape as spring-data-jdbc's
		// save() — so this stack measures the ORM abstraction, not a hand-tuned
		// bypass of it.
	})
	if err != nil {
		return nil, fmt.Errorf("gorm open: %w", err)
	}

	sqlDB, err := gdb.DB()
	if err != nil {
		return nil, fmt.Errorf("gorm DB(): %w", err)
	}
	// Pool sizing maps onto database/sql, matching pgxpool MaxConns/MinConns and
	// HikariCP max/min. Recycle windows large enough to never fire mid-phase.
	sqlDB.SetMaxOpenConns(cfg.PoolMax)
	sqlDB.SetMaxIdleConns(cfg.PoolMin)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)
	sqlDB.SetConnMaxIdleTime(5 * time.Minute)

	// Warm to pool_min so the first measured phase isn't paying for lazy
	// connection creation (mirrors pgxpool MinConns / the JVM pool warmups).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := sqlDB.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping db: %w", err)
	}
	for i := 0; i < cfg.PoolMin; i++ {
		if err := sqlDB.PingContext(ctx); err != nil {
			return nil, fmt.Errorf("warmup ping: %w", err)
		}
	}
	return &Db{gdb: gdb}, nil
}

// Close releases the underlying connection pool.
func (d *Db) Close() {
	if sqlDB, err := d.gdb.DB(); err == nil {
		_ = sqlDB.Close()
	}
}

// InsertCommand is the Execute hot path: one GORM Create. With the default
// transaction wrapper this is BEGIN + INSERT ... RETURNING "id" + COMMIT; the
// generated id is written back into the model.
func (d *Db) InsertCommand(ctx context.Context, workflowID, commandType, payload string, seq, checksum int64) (int64, error) {
	c := Command{
		WorkflowID:  workflowID,
		CommandType: commandType,
		Payload:     payload,
		Seq:         seq,
		Checksum:    checksum,
	}
	if err := d.gdb.WithContext(ctx).Create(&c).Error; err != nil {
		return 0, err
	}
	return c.ID, nil
}

// ExecuteTx runs INSERT command + UPSERT state + INSERT outbox in one
// transaction via GORM's managed `Transaction` closure (BEGIN/COMMIT around the
// three statements, awaited sequentially — the per-statement model every stack
// shares). The UPSERT uses raw SQL because its `version = version + 1` can't be
// expressed through `Create`.
func (d *Db) ExecuteTx(ctx context.Context, workflowID, commandType, payload string, seq, checksum int64) (int64, error) {
	var id int64
	err := d.gdb.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		c := Command{
			WorkflowID:  workflowID,
			CommandType: commandType,
			Payload:     payload,
			Seq:         seq,
			Checksum:    checksum,
		}
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		id = c.ID
		if err := tx.Exec(upsertStateSQL, workflowID, commandType).Error; err != nil {
			return err
		}
		ob := OutboxEvent{WorkflowID: workflowID, EventType: commandType, Payload: payload}
		return tx.Create(&ob).Error
	})
	return id, err
}

// GetState is the read shape: single lookup by primary key. Raw query so the
// timestamp-to-micros conversion happens server-side, like the other stacks.
// Returns nil if no row exists.
func (d *Db) GetState(ctx context.Context, workflowID string) (*StateRow, error) {
	var row StateRow
	res := d.gdb.WithContext(ctx).Raw(selectStateSQL, workflowID).Scan(&row)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil
	}
	row.WorkflowID = workflowID
	return &row, nil
}
