package db

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

type BenchmarkRecord struct {
	ID            string    `json:"id"`
	Timestamp     time.Time `json:"timestamp"`
	OperationType string    `json:"operation_type"`
	ServerType    string    `json:"server_type"`
	ClientID      string    `json:"client_id"`
	SequenceNum   int64     `json:"sequence_number"`
	CorrelationID string    `json:"correlation_id"`
	PayloadSize   int       `json:"payload_size"`
	ProcessingMS  int64     `json:"processing_time_ms"`
	Headers       Headers   `json:"headers"`
	CreatedAt     time.Time `json:"created_at"`
}

type Headers map[string]string

func (h Headers) Value() (driver.Value, error) {
	return json.Marshal(h)
}

func (h *Headers) Scan(value interface{}) error {
	if value == nil {
		*h = make(Headers)
		return nil
	}
	switch v := value.(type) {
	case []byte:
		return json.Unmarshal(v, h)
	case string:
		return json.Unmarshal([]byte(v), h)
	default:
		return fmt.Errorf("cannot scan %T into Headers", value)
	}
}

func NewDB(ctx context.Context, databaseURL string) (*DB, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{pool: pool}
	if err := db.initSchema(ctx); err != nil {
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

func (db *DB) initSchema(ctx context.Context) error {
	schema := `
	CREATE TABLE IF NOT EXISTS benchmark_records (
		id VARCHAR(36) PRIMARY KEY,
		timestamp TIMESTAMPTZ NOT NULL,
		operation_type VARCHAR(20) NOT NULL,
		server_type VARCHAR(20) NOT NULL,
		client_id VARCHAR(36) NOT NULL,
		sequence_number BIGINT NOT NULL,
		correlation_id VARCHAR(36) NOT NULL,
		payload_size INTEGER NOT NULL,
		processing_time_ms BIGINT NOT NULL,
		headers JSONB,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE INDEX IF NOT EXISTS idx_benchmark_records_timestamp ON benchmark_records(timestamp);
	CREATE INDEX IF NOT EXISTS idx_benchmark_records_operation_type ON benchmark_records(operation_type);
	CREATE INDEX IF NOT EXISTS idx_benchmark_records_server_type ON benchmark_records(server_type);
	CREATE INDEX IF NOT EXISTS idx_benchmark_records_client_id ON benchmark_records(client_id);
	`

	_, err := db.pool.Exec(ctx, schema)
	return err
}

func (db *DB) SaveBenchmarkRecord(ctx context.Context, record *BenchmarkRecord) error {
	if record.ID == "" {
		record.ID = uuid.New().String()
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = time.Now()
	}

	query := `
		INSERT INTO benchmark_records (
			id, timestamp, operation_type, server_type, client_id,
			sequence_number, correlation_id, payload_size, processing_time_ms,
			headers, created_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`

	headersJSON, err := json.Marshal(record.Headers)
	if err != nil {
		return fmt.Errorf("failed to marshal headers: %w", err)
	}

	_, err = db.pool.Exec(ctx, query,
		record.ID, record.Timestamp, record.OperationType, record.ServerType,
		record.ClientID, record.SequenceNum, record.CorrelationID,
		record.PayloadSize, record.ProcessingMS, headersJSON, record.CreatedAt,
	)

	return err
}

func (db *DB) GetBenchmarkStats(ctx context.Context, serverType string, operationType string, since time.Time) (*BenchmarkStats, error) {
	query := `
		SELECT 
			COUNT(*) as total_requests,
			AVG(processing_time_ms) as avg_processing_time,
			MIN(processing_time_ms) as min_processing_time,
			MAX(processing_time_ms) as max_processing_time,
			PERCENTILE_CONT(0.5) WITHIN GROUP (ORDER BY processing_time_ms) as p50_processing_time,
			PERCENTILE_CONT(0.95) WITHIN GROUP (ORDER BY processing_time_ms) as p95_processing_time,
			PERCENTILE_CONT(0.99) WITHIN GROUP (ORDER BY processing_time_ms) as p99_processing_time
		FROM benchmark_records 
		WHERE server_type = $1 AND operation_type = $2 AND timestamp >= $3
	`

	var stats BenchmarkStats
	err := db.pool.QueryRow(ctx, query, serverType, operationType, since).Scan(
		&stats.TotalRequests,
		&stats.AvgProcessingTime,
		&stats.MinProcessingTime,
		&stats.MaxProcessingTime,
		&stats.P50ProcessingTime,
		&stats.P95ProcessingTime,
		&stats.P99ProcessingTime,
	)

	return &stats, err
}

type BenchmarkStats struct {
	TotalRequests     int64   `json:"total_requests"`
	AvgProcessingTime float64 `json:"avg_processing_time"`
	MinProcessingTime int64   `json:"min_processing_time"`
	MaxProcessingTime int64   `json:"max_processing_time"`
	P50ProcessingTime float64 `json:"p50_processing_time"`
	P95ProcessingTime float64 `json:"p95_processing_time"`
	P99ProcessingTime float64 `json:"p99_processing_time"`
}

func (db *DB) Close() {
	db.pool.Close()
}
