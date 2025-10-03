package database

import (
	"database/sql"
	"fmt"
	"time"

	"workflow-engine/internal/models"

	_ "github.com/lib/pq"
)

type DB struct {
	*sql.DB
}

// NewDB creates a new database connection
func NewDB(host, port, user, password, dbname string) (*DB, error) {
	psqlInfo := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=disable",
		host, port, user, password, dbname)

	db, err := sql.Open("postgres", psqlInfo)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}

	return &DB{db}, nil
}

// NewDBFromDSN creates a new database connection from DSN string
func NewDBFromDSN(dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}

	if err = db.Ping(); err != nil {
		return nil, err
	}

	return &DB{db}, nil

	return &DB{db}, nil
}

// CreateEndpoint creates a new endpoint in the database
func (db *DB) CreateEndpoint(name, endpoint string) (*models.Endpoint, error) {
	query := `
		INSERT INTO waves.endpoint (name, endpoint, created_at, updated_at)
		VALUES ($1, $2, $3, $4)
		RETURNING id, name, endpoint, version, created_at, updated_at`

	now := time.Now()
	var ep models.Endpoint

	err := db.QueryRow(query, name, endpoint, now, now).Scan(
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
	err := db.QueryRow(query, name).Scan(
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

	err := db.QueryRow(query, name, rid, workflowType, models.WorkflowStatusPending, now, now).Scan(
		&wf.ID, &wf.Name, &wf.RID, &wf.Type, &wf.Status, &wf.Version, &wf.CreatedAt, &wf.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &wf, nil
}

// UpdateWorkflowStatus updates the workflow status
func (db *DB) UpdateWorkflowStatus(id int64, status string) error {
	query := `UPDATE waves.workflow SET status = $1, updated_at = $2 WHERE id = $3`
	_, err := db.Exec(query, status, time.Now(), id)
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

	err := db.QueryRow(query, workflowID, name, stateType, status, now, now).Scan(
		&state.ID, &state.WorkflowID, &state.Name, &state.Type, &state.Status,
		&state.Version, &state.CreatedAt, &state.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &state, nil
}

// UpdateStateStatus updates the state status
func (db *DB) UpdateStateStatus(workflowID int64, name, status string) error {
	query := `UPDATE waves.state SET status = $1, updated_at = $2 
			  WHERE workflow_id = $3 AND name = $4`
	_, err := db.Exec(query, status, time.Now(), workflowID, name)
	return err
}

// CreateOrUpdateVariables creates or updates variables in the database
func (db *DB) CreateOrUpdateVariables(workflowID int64, lastTaskName string, data models.JSONB) error {
	// Check if variables exist for this workflow
	var count int
	checkQuery := `SELECT COUNT(*) FROM waves.variables WHERE workflow_id = $1`
	err := db.QueryRow(checkQuery, workflowID).Scan(&count)
	if err != nil {
		return err
	}

	now := time.Now()

	if count > 0 {
		// Update existing variables
		updateQuery := `UPDATE waves.variables SET last_task_name = $1, data = $2, updated_at = $3 
						WHERE workflow_id = $4`
		_, err = db.Exec(updateQuery, lastTaskName, data, now, workflowID)
	} else {
		// Create new variables
		insertQuery := `INSERT INTO waves.variables (workflow_id, last_task_name, data, created_at, updated_at)
						VALUES ($1, $2, $3, $4, $5)`
		_, err = db.Exec(insertQuery, workflowID, lastTaskName, data, now, now)
	}

	return err
}

// GetWorkflowByID retrieves a workflow by ID
func (db *DB) GetWorkflowByID(id int64) (*models.Workflow, error) {
	query := `SELECT id, name, rid, type, status, version, created_at, updated_at 
			  FROM waves.workflow WHERE id = $1`

	var wf models.Workflow
	err := db.QueryRow(query, id).Scan(
		&wf.ID, &wf.Name, &wf.RID, &wf.Type, &wf.Status, &wf.Version, &wf.CreatedAt, &wf.UpdatedAt)

	if err != nil {
		return nil, err
	}

	return &wf, nil
}
