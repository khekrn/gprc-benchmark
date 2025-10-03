package models

import (
	"database/sql/driver"
	"encoding/json"
	"time"
)

// WorkflowStatus constants
const (
	WorkflowStatusPending = "p"
	WorkflowStatusSuccess = "s"
	WorkflowStatusFailed  = "f"
	WorkflowStatusRunning = "r"
)

// StateStatus constants
const (
	StateStatusPending = "p"
	StateStatusSuccess = "s"
	StateStatusFailed  = "f"
	StateStatusRunning = "r"
)

// JSONB represents a PostgreSQL JSONB field
type JSONB map[string]interface{}

// Value implements the driver.Valuer interface
func (j JSONB) Value() (driver.Value, error) {
	return json.Marshal(j)
}

// Scan implements the sql.Scanner interface
func (j *JSONB) Scan(value interface{}) error {
	if value == nil {
		*j = make(JSONB)
		return nil
	}

	bytes, ok := value.([]byte)
	if !ok {
		return nil
	}

	return json.Unmarshal(bytes, j)
}

// Endpoint represents the endpoint table
type Endpoint struct {
	ID        int64     `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	Endpoint  string    `json:"endpoint" db:"endpoint"`
	Version   int       `json:"version" db:"version"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// Workflow represents the workflow table
type Workflow struct {
	ID        int64     `json:"id" db:"id"`
	Name      string    `json:"name" db:"name"`
	RID       string    `json:"rid" db:"rid"`
	Type      string    `json:"type" db:"type"`
	Status    string    `json:"status" db:"status"`
	Version   int       `json:"version" db:"version"`
	CreatedAt time.Time `json:"created_at" db:"created_at"`
	UpdatedAt time.Time `json:"updated_at" db:"updated_at"`
}

// State represents the state table
type State struct {
	ID         int64     `json:"id" db:"id"`
	WorkflowID int64     `json:"workflow_id" db:"workflow_id"`
	Name       string    `json:"name" db:"name"`
	Type       string    `json:"type" db:"type"`
	Status     string    `json:"status" db:"status"`
	Version    int       `json:"version" db:"version"`
	CreatedAt  time.Time `json:"created_at" db:"created_at"`
	UpdatedAt  time.Time `json:"updated_at" db:"updated_at"`
}

// Variables represents the variables table
type Variables struct {
	ID           int64     `json:"id" db:"id"`
	WorkflowID   int64     `json:"workflow_id" db:"workflow_id"`
	LastTaskName string    `json:"last_task_name" db:"last_task_name"`
	Data         JSONB     `json:"data" db:"data"`
	Version      int       `json:"version" db:"version"`
	CreatedAt    time.Time `json:"created_at" db:"created_at"`
	UpdatedAt    time.Time `json:"updated_at" db:"updated_at"`
}

// StateType constants
const (
	StateTypeTask      = "task"
	StateTypeCondition = "condition"
)
