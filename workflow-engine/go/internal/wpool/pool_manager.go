package wpool

import (
	"fmt"
	"log"
	"sync"
	"time"

	"workflow-engine/internal/database"
	redis_client "workflow-engine/internal/redis"
)

// WorkflowResult represents the result of a synchronous workflow execution
type WorkflowResult struct {
	Success    bool                   `json:"success"`
	Status     string                 `json:"status"`
	Result     map[string]interface{} `json:"result,omitempty"`
	Error      string                 `json:"error,omitempty"`
	WorkflowID int64                  `json:"workflow_id"`
	RequestID  string                 `json:"request_id"`
	States     []StateUpdate          `json:"states,omitempty"`
}

// StateUpdate represents a workflow state update
type StateUpdate struct {
	StateName string                 `json:"state_name"`
	StateType string                 `json:"state_type"`
	Status    string                 `json:"status"`
	Data      map[string]interface{} `json:"data,omitempty"`
	Timestamp time.Time              `json:"timestamp"`
}

// WorkerPoolManager manages multiple workflow-specific worker pools
type WorkerPoolManager struct {
	pools           map[string]*WorkerPool // workflow_name -> WorkerPool
	workflowMapping map[string][]string    // workflow_name -> []endpoints
	mu              sync.RWMutex
	db              *database.DB
	redisClient     *redis_client.Client
}

// WorkflowPoolConfig represents configuration for a workflow's worker pool
type WorkflowPoolConfig struct {
	WorkflowName string `json:"workflow_name"`
	MinInstances int    `json:"min_instances"`
	MaxInstances int    `json:"max_instances"`
	Enabled      bool   `json:"enabled"`
}

// NewWorkerPoolManager creates a new manager for workflow-specific worker pools
func NewWorkerPoolManager(db *database.DB, redisClient *redis_client.Client) *WorkerPoolManager {
	return &WorkerPoolManager{
		pools:           make(map[string]*WorkerPool),
		workflowMapping: make(map[string][]string),
		db:              db,
		redisClient:     redisClient,
	}
}

// GetOrCreatePool gets an existing pool or creates a new one for the workflow
func (wpm *WorkerPoolManager) GetOrCreatePool(workflowName string) *WorkerPool {
	wpm.mu.Lock()
	defer wpm.mu.Unlock()

	// Check if pool already exists
	if pool, exists := wpm.pools[workflowName]; exists {
		return pool
	}

	// Create new workflow-specific pool
	log.Printf("Creating new worker pool for workflow: %s", workflowName)
	pool := NewWorkflowSpecificPool(wpm.redisClient, workflowName, 1, 64) // Default min=1, max=64 for high throughput
	wpm.pools[workflowName] = pool

	return pool
}

// GetPool returns the worker pool for a specific workflow
func (wpm *WorkerPoolManager) GetPool(workflowName string) (*WorkerPool, error) {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	pool, exists := wpm.pools[workflowName]
	if !exists {
		return nil, fmt.Errorf("no worker pool found for workflow: %s", workflowName)
	}

	return pool, nil
}

// ExecuteWorkflowSync routes workflow execution to the appropriate worker pool and waits for result
func (wpm *WorkerPoolManager) ExecuteWorkflowSync(workflowName, requestID string, workflowID int64, payload string, timeout time.Duration) (*WorkflowResult, error) {
	log.Printf("Executing workflow %s (ID: %d) synchronously with request ID: %s", workflowName, workflowID, requestID)

	// Step 1: Fetch available workers for this workflow from Redis
	workers, err := wpm.redisClient.GetWorkersForWorkflow(workflowName)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch workers for workflow %s: %v", workflowName, err)
	}

	if len(workers) == 0 {
		return nil, fmt.Errorf("no workers available for workflow: %s", workflowName)
	}

	log.Printf("Found %d workers for workflow %s: %v", len(workers), workflowName, workers)

	// Step 2: Get or create a workflow pool for this workflow type
	pool := wpm.GetOrCreatePool(workflowName)

	// Step 3: Execute workflow synchronously
	return pool.ExecuteWorkflowSync(workflowName, requestID, workflowID, payload, timeout)
}

// ExecuteWorkflow routes workflow execution to the appropriate worker pool
func (wpm *WorkerPoolManager) ExecuteWorkflow(workflowName, requestID string, workflowID int64, payload string) error {
	log.Printf("Executing workflow %s (ID: %d) with request ID: %s", workflowName, workflowID, requestID)

	// Step 1: Fetch available workers for this workflow from Redis
	workers, err := wpm.redisClient.GetWorkersForWorkflow(workflowName)
	if err != nil {
		return fmt.Errorf("failed to fetch workers for workflow %s: %v", workflowName, err)
	}

	if len(workers) == 0 {
		return fmt.Errorf("no workers available for workflow: %s", workflowName)
	}

	log.Printf("Found %d workers for workflow %s: %v", len(workers), workflowName, workers)

	// Step 2: Get or create a simple workflow executor
	executor := NewWorkflowExecutor(wpm.db, wpm.redisClient, workflowName)
	// Note: executor will close itself when workflow completes

	// Step 3: Execute workflow with available workers
	return executor.Execute(requestID, workflowID, payload, workers)
}

// GetAllPools returns all worker pools
func (wpm *WorkerPoolManager) GetAllPools() map[string]*WorkerPool {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	pools := make(map[string]*WorkerPool)
	for name, pool := range wpm.pools {
		pools[name] = pool
	}
	return pools
}

// GetPoolStats returns statistics for all workflow pools
func (wpm *WorkerPoolManager) GetPoolStats() map[string]interface{} {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	stats := make(map[string]interface{})

	for workflowName, pool := range wpm.pools {
		workers := pool.GetAllWorkers()

		activeWorkers := 0
		totalActiveTasks := int32(0)
		totalTasks := int64(0)

		for _, worker := range workers {
			if worker.Connected {
				activeWorkers++
				worker.mu.RLock()
				totalActiveTasks += worker.ActiveTasks
				totalTasks += worker.TotalTasks
				worker.mu.RUnlock()
			}
		}

		stats[workflowName] = map[string]interface{}{
			"total_workers":         len(workers),
			"active_workers":        activeWorkers,
			"total_active_tasks":    totalActiveTasks,
			"total_tasks_processed": totalTasks,
			"load_balancing_stats":  pool.GetLoadBalancingStats(),
		}
	}

	return stats
}

// Close closes all worker pools
func (wpm *WorkerPoolManager) Close() {
	wpm.mu.Lock()
	defer wpm.mu.Unlock()

	for name, pool := range wpm.pools {
		log.Printf("Closing worker pool for workflow: %s", name)
		pool.Close()
	}
}

// RegisterWorkflowEndpoints maps a workflow to specific worker endpoints
func (wpm *WorkerPoolManager) RegisterWorkflowEndpoints(workflowName string, endpoints []string) {
	wpm.mu.Lock()
	defer wpm.mu.Unlock()

	log.Printf("Registering endpoints for workflow %s: %v", workflowName, endpoints)
	wpm.workflowMapping[workflowName] = endpoints

	// Note: In the new simplified architecture, we fetch endpoints from Redis on each request
	// so we don't need to maintain persistent pools
}

// GetWorkflowEndpoints returns the endpoints registered for a specific workflow
func (wpm *WorkerPoolManager) GetWorkflowEndpoints(workflowName string) []string {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	if endpoints, exists := wpm.workflowMapping[workflowName]; exists {
		return endpoints
	}
	return []string{}
}

// GetAllWorkflowMappings returns all workflow-to-endpoints mappings
func (wpm *WorkerPoolManager) GetAllWorkflowMappings() map[string][]string {
	wpm.mu.RLock()
	defer wpm.mu.RUnlock()

	mappings := make(map[string][]string)
	for workflow, endpoints := range wpm.workflowMapping {
		mappings[workflow] = append([]string{}, endpoints...) // Create a copy
	}
	return mappings
}

// GetWorkflowCapacity returns capacity information for a workflow
func (wpm *WorkerPoolManager) GetWorkflowCapacity(workflowName string) map[string]interface{} {
	pool, err := wpm.GetPool(workflowName)
	if err != nil {
		return map[string]interface{}{
			"error": err.Error(),
		}
	}

	workers := pool.GetAllWorkers()
	activeWorkers := 0
	totalCapacity := int32(0)
	currentLoad := int32(0)

	for _, worker := range workers {
		if worker.Connected {
			activeWorkers++
			worker.mu.RLock()
			currentLoad += worker.ActiveTasks
			// Assume each worker can handle ~64 concurrent tasks (high throughput configuration)
			totalCapacity += 64
			worker.mu.RUnlock()
		}
	}

	utilizationPercent := float64(0)
	if totalCapacity > 0 {
		utilizationPercent = float64(currentLoad) / float64(totalCapacity) * 100
	}

	return map[string]interface{}{
		"workflow_name":       workflowName,
		"active_workers":      activeWorkers,
		"total_workers":       len(workers),
		"current_load":        currentLoad,
		"total_capacity":      totalCapacity,
		"utilization_percent": utilizationPercent,
		"can_accept_more":     currentLoad < totalCapacity,
	}
}
