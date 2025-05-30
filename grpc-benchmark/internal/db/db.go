package db

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog/log"
)

type DB struct {
	pool *pgxpool.Pool
}

type BenchmarkRecord struct {
	ID                 int               `json:"id"`
	RecordID           string            `json:"record_id"`
	RequestType        string            `json:"request_type"`
	SequenceID         *int32            `json:"sequence_id,omitempty"`
	UserID             string            `json:"user_id,omitempty"`
	SessionID          string            `json:"session_id,omitempty"`
	Payload            string            `json:"payload,omitempty"`
	Metadata           map[string]string `json:"metadata,omitempty"`
	ProcessingTimeNs   int64             `json:"processing_time_ns"`
	QueueTimeNs        int64             `json:"queue_time_ns"`
	ConcurrentRequests int32             `json:"concurrent_requests"`
	CreatedAt          time.Time         `json:"created_at"`
	UpdatedAt          time.Time         `json:"updated_at"`
}

type DataChunk struct {
	ID          int    `json:"id"`
	RecordID    string `json:"record_id"`
	ChunkID     int32  `json:"chunk_id"`
	Filename    string `json:"filename,omitempty"`
	DataSize    int32  `json:"data_size"`
	TotalChunks int32  `json:"total_chunks"`
	CreatedAt   time.Time `json:"created_at"`
}

func NewDB() (*DB, error) {
	databaseURL := os.Getenv("DATABASE_URL")
	if databaseURL == "" {
		return nil, fmt.Errorf("DATABASE_URL environment variable is required")
	}

	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database URL: %w", err)
	}

	// Configure connection pool
	if maxConns := os.Getenv("DB_MAX_CONNECTIONS"); maxConns != "" {
		if val, err := strconv.Atoi(maxConns); err == nil {
			config.MaxConns = int32(val)
		}
	}

	if minConns := os.Getenv("DB_MIN_CONNECTIONS"); minConns != "" {
		if val, err := strconv.Atoi(minConns); err == nil {
			config.MinConns = int32(val)
		}
	}

	if maxConnLifetime := os.Getenv("DB_MAX_CONN_LIFETIME"); maxConnLifetime != "" {
		if duration, err := time.ParseDuration(maxConnLifetime); err == nil {
			config.MaxConnLifetime = duration
		}
	}

	if maxConnIdleTime := os.Getenv("DB_MAX_CONN_IDLE_TIME"); maxConnIdleTime != "" {
		if duration, err := time.ParseDuration(maxConnIdleTime); err == nil {
			config.MaxConnIdleTime = duration
		}
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	// Test the connection
	if err := pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	log.Info().Msg("Database connection established successfully")
	return &DB{pool: pool}, nil
}

func (db *DB) Close() {
	if db.pool != nil {
		db.pool.Close()
	}
}

func (db *DB) Ping(ctx context.Context) error {
	return db.pool.Ping(ctx)
}

func (db *DB) InsertBenchmarkRecord(ctx context.Context, record *BenchmarkRecord) error {
	metadataJSON, err := json.Marshal(record.Metadata)
	if err != nil {
		return fmt.Errorf("failed to marshal metadata: %w", err)
	}

	query := `
		INSERT INTO benchmark.benchmark_records (
			record_id, request_type, sequence_id, user_id, session_id, 
			payload, metadata, processing_time_ns, queue_time_ns, concurrent_requests
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (record_id) DO UPDATE SET
			updated_at = NOW()
	`

	_, err = db.pool.Exec(ctx, query,
		record.RecordID,
		record.RequestType,
		record.SequenceID,
		record.UserID,
		record.SessionID,
		record.Payload,
		metadataJSON,
		record.ProcessingTimeNs,
		record.QueueTimeNs,
		record.ConcurrentRequests,
	)

	if err != nil {
		return fmt.Errorf("failed to insert benchmark record: %w", err)
	}

	return nil
}

func (db *DB) InsertDataChunk(ctx context.Context, chunk *DataChunk) error {
	query := `
		INSERT INTO benchmark.data_chunks (record_id, chunk_id, filename, data_size, total_chunks)
		VALUES ($1, $2, $3, $4, $5)
	`

	_, err := db.pool.Exec(ctx, query,
		chunk.RecordID,
		chunk.ChunkID,
		chunk.Filename,
		chunk.DataSize,
		chunk.TotalChunks,
	)

	if err != nil {
		return fmt.Errorf("failed to insert data chunk: %w", err)
	}

	return nil
}

func (db *DB) GetBenchmarkRecord(ctx context.Context, recordID string) (*BenchmarkRecord, error) {
	query := `
		SELECT id, record_id, request_type, sequence_id, user_id, session_id,
			   payload, metadata, processing_time_ns, queue_time_ns, concurrent_requests,
			   created_at, updated_at
		FROM benchmark.benchmark_records 
		WHERE record_id = $1
	`

	row := db.pool.QueryRow(ctx, query, recordID)

	var record BenchmarkRecord
	var metadataJSON []byte

	err := row.Scan(
		&record.ID,
		&record.RecordID,
		&record.RequestType,
		&record.SequenceID,
		&record.UserID,
		&record.SessionID,
		&record.Payload,
		&metadataJSON,
		&record.ProcessingTimeNs,
		&record.QueueTimeNs,
		&record.ConcurrentRequests,
		&record.CreatedAt,
		&record.UpdatedAt,
	)

	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, fmt.Errorf("benchmark record not found: %s", recordID)
		}
		return nil, fmt.Errorf("failed to get benchmark record: %w", err)
	}

	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &record.Metadata); err != nil {
			log.Error().Err(err).Msg("Failed to unmarshal metadata")
		}
	}

	return &record, nil
}

func (db *DB) GetStats(ctx context.Context) (map[string]interface{}, error) {
	query := `
		SELECT 
			COUNT(*) as total_records,
			COUNT(DISTINCT user_id) as unique_users,
			COUNT(DISTINCT session_id) as unique_sessions,
			AVG(processing_time_ns) as avg_processing_time,
			MAX(processing_time_ns) as max_processing_time,
			MIN(processing_time_ns) as min_processing_time,
			request_type,
			COUNT(*) as count_by_type
		FROM benchmark.benchmark_records 
		GROUP BY request_type
	`

	rows, err := db.pool.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}
	defer rows.Close()

	stats := make(map[string]interface{})
	var totalRecords, uniqueUsers, uniqueSessions int64
	var avgProcessingTime, maxProcessingTime, minProcessingTime float64
	var requestType string
	var countByType int64

	typeStats := make(map[string]int64)

	for rows.Next() {
		err := rows.Scan(&totalRecords, &uniqueUsers, &uniqueSessions,
			&avgProcessingTime, &maxProcessingTime, &minProcessingTime,
			&requestType, &countByType)
		if err != nil {
			return nil, fmt.Errorf("failed to scan stats row: %w", err)
		}
		typeStats[requestType] = countByType
	}

	stats["total_records"] = totalRecords
	stats["unique_users"] = uniqueUsers
	stats["unique_sessions"] = uniqueSessions
	stats["avg_processing_time_ns"] = avgProcessingTime
	stats["max_processing_time_ns"] = maxProcessingTime
	stats["min_processing_time_ns"] = minProcessingTime
	stats["type_distribution"] = typeStats

	return stats, nil
}
