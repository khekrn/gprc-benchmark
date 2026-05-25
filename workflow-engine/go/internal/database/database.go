package database

import (
	"context"
	"fmt"
	"time"

	"workflow-engine/internal/models"

	"github.com/jackc/pgx/v5/pgxpool"
)

type DB struct {
	pool *pgxpool.Pool
}

// NewDB creates a new database connection pool
func NewDB(host, port, user, password, dbname string) (*DB, error) {
	// Build connection string with production-ready pool configuration
	connString := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable&pool_max_conns=30&pool_min_conns=10&pool_max_conn_lifetime=1h&pool_max_conn_idle_time=30m&pool_health_check_period=1m",
		user, password, host, port, dbname)

	config, err := pgxpool.ParseConfig(connString)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	// Configure pool settings for high throughput production workload
	config.MaxConns = 30                      // High concurrency support
	config.MinConns = 10                      // Keep connections warm
	config.MaxConnLifetime = time.Hour        // Rotate connections hourly
	config.MaxConnIdleTime = time.Minute * 30 // Close idle connections
	config.HealthCheckPeriod = time.Minute    // Regular health checks

	// Production connection timeout
	config.ConnConfig.Config.ConnectTimeout = time.Second * 5

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %v", err)
	}

	// Test connection
	if err = pool.Ping(context.Background()); err != nil {
		return nil, fmt.Errorf("failed to ping database: %v", err)
	}

	return &DB{pool: pool}, nil
}

// NewDBFromDSN creates a new database connection pool from DSN string
func NewDBFromDSN(dsn string) (*DB, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse config: %v", err)
	}

	// Configure pool settings for high throughput production workload
	config.MaxConns = 30                      // High concurrency support
	config.MinConns = 10                      // Keep connections warm
	config.MaxConnLifetime = time.Hour        // Rotate connections hourly
	config.MaxConnIdleTime = time.Minute * 30 // Close idle connections
	config.HealthCheckPeriod = time.Minute    // Regular health checks

	// Production connection timeout
	config.ConnConfig.Config.ConnectTimeout = time.Second * 5

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %v", err)
	}

	return &DB{pool: pool}, nil
}

// Close closes the database connection pool
func (db *DB) Close() {
	db.pool.Close()
}

// GetPoolStats returns connection pool statistics for monitoring
func (db *DB) GetPoolStats() map[string]interface{} {
	stat := db.pool.Stat()
	return map[string]interface{}{
		"total_connections":          stat.TotalConns(),
		"idle_connections":           stat.IdleConns(),
		"acquired_connections":       stat.AcquiredConns(),
		"constructing_connections":   stat.ConstructingConns(),
		"max_connections":            stat.MaxConns(),
		"acquire_count":              stat.AcquireCount(),
		"acquire_duration":           stat.AcquireDuration().String(),
		"canceled_acquire_count":     stat.CanceledAcquireCount(),
		"empty_acquire_count":        stat.EmptyAcquireCount(),
		"max_lifetime_destroy_count": stat.MaxLifetimeDestroyCount(),
		"max_idle_destroy_count":     stat.MaxIdleDestroyCount(),
	}
}

// CreateEndpoint creates a new endpoint in the database
func (db *DB) CreateEndpoint(name, endpoint string) (*models.Endpoint, error) {
	query := `
		INSERT INTO waves.endpoint (name, endpoint, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, endpoint, version, created_at, updated_at`

	now := time.Now()
	var ep models.Endpoint

	err := db.pool.QueryRow(context.Background(), query, name, endpoint, now, now).Scan(
		&ep.ID, &ep.Name, &ep.Endpoint, &ep.Version, &ep.CreatedAt, &ep.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &ep, nil
}

// GetEndpointByName retrieves an endpoint by name
func (db *DB) GetEndpointByName(name string) (*models.Endpoint, error) {
	query := `SELECT id, name, endpoint, version, created_at, updated_at 
			  FROM waves.endpoint WHERE name = $1`

	var ep models.Endpoint
	err := db.pool.QueryRow(context.Background(), query, name).Scan(
		&ep.ID, &ep.Name, &ep.Endpoint, &ep.Version, &ep.CreatedAt, &ep.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &ep, nil
}

// CreateWorkflow creates a new workflow in the database
func (db *DB) CreateWorkflow(name, rid, workflowType string) (*models.Workflow, error) {
	query := `
		INSERT INTO waves.workflow (name, rid, type, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, name, rid, type, status, version, created_at, updated_at`

	now := time.Now()
	var wf models.Workflow

	err := db.pool.QueryRow(context.Background(), query, name, rid, workflowType, models.WorkflowStatusPending, now, now).Scan(
		&wf.ID, &wf.Name, &wf.RID, &wf.Type, &wf.Status, &wf.Version, &wf.CreatedAt, &wf.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &wf, nil
}

// UpdateWorkflowStatus updates the workflow status
func (db *DB) UpdateWorkflowStatus(id int64, status string) error {
	query := `UPDATE waves.workflow SET status = $1, updated_at = $2 WHERE id = $3`

	_, err := db.pool.Exec(context.Background(), query, status, time.Now(), id)
	return err
}

// CreateState creates a new state in the database
func (db *DB) CreateState(workflowID int64, name, stateType, status string) (*models.State, error) {
	query := `
		INSERT INTO waves.state (workflow_id, name, type, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		RETURNING id, workflow_id, name, type, status, version, created_at, updated_at`

	now := time.Now()
	var state models.State

	err := db.pool.QueryRow(context.Background(), query, workflowID, name, stateType, status, now, now).Scan(
		&state.ID, &state.WorkflowID, &state.Name, &state.Type, &state.Status, &state.Version, &state.CreatedAt, &state.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &state, nil
}

// UpdateStateStatus updates the state status
// UpdateStateStatus updates the status of a state
func (db *DB) UpdateStateStatus(workflowID int64, name, status string) error {
	query := `UPDATE waves.state SET status = $1, updated_at = $2 WHERE workflow_id = $3 AND name = $4`

	_, err := db.pool.Exec(context.Background(), query, status, time.Now(), workflowID, name)
	return err
}

// CreateOrUpdateVariables creates or updates variables in the database
func (db *DB) CreateOrUpdateVariables(workflowID int64, lastTaskName string, data models.JSONB) error {
	// Check if variables exist for this workflow
	var count int
	checkQuery := `SELECT COUNT(*) FROM waves.variables WHERE workflow_id = $1`

	err := db.pool.QueryRow(context.Background(), checkQuery, workflowID).Scan(&count)
	if err != nil {
		return err
	}

	now := time.Now()

	if count > 0 {
		// Update existing variables
		updateQuery := `UPDATE waves.variables SET last_task_name = $1, data = $2, updated_at = $3 
						WHERE workflow_id = $4`
		_, err = db.pool.Exec(context.Background(), updateQuery, lastTaskName, data, now, workflowID)
	} else {
		// Create new variables
		insertQuery := `INSERT INTO waves.variables (workflow_id, last_task_name, data, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5)`
		_, err = db.pool.Exec(context.Background(), insertQuery, workflowID, lastTaskName, data, now, now)
	}

	return err
}

// GetWorkflowByID retrieves a workflow by ID
func (db *DB) GetWorkflowByID(id int64) (*models.Workflow, error) {
	query := `SELECT id, name, rid, type, status, version, created_at, updated_at 
			  FROM waves.workflow WHERE id = $1`

	var wf models.Workflow
	err := db.pool.QueryRow(context.Background(), query, id).Scan(
		&wf.ID, &wf.Name, &wf.RID, &wf.Type, &wf.Status, &wf.Version, &wf.CreatedAt, &wf.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &wf, nil
}
