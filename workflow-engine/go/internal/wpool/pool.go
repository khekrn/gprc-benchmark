package wpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	redis_client "workflow-engine/internal/redis"
	pb "workflow-engine/proto"
)

// WorkerConnection represents a connection to a worker
type WorkerConnection struct {
	Name          string   // Worker name (should match workflow name for workflow-specific workers)
	Endpoint      string   // Worker gRPC endpoint
	WorkflowTypes []string // List of workflow types this worker can handle
	Conn          *grpc.ClientConn
	Client        pb.WorkerServiceClient
	Stream        pb.WorkerService_WorkflowStreamClient
	Connected     bool
	LastSeen      time.Time
	ActiveTasks   int32 // Track active workloads for load balancing
	TotalTasks    int64 // Track total tasks processed
	MaxCapacity   int32 // Maximum concurrent tasks this worker can handle
	mu            sync.RWMutex
	ctx           context.Context
	cancel        context.CancelFunc
}

// PoolConfig represents configuration for a worker pool
type PoolConfig struct {
	WorkflowFilter  string // If set, only accept workers for this specific workflow
	MinWorkers      int    // Minimum number of workers to maintain
	MaxWorkers      int    // Maximum number of workers allowed
	LoadBalanceType string // "least_connections", "round_robin", "workflow_affinity"
}

// streamMessage represents a message to be processed
type streamMessage struct {
	worker   *WorkerConnection
	response *pb.WorkerToServerMessage
}

// WorkerPool manages connections to workers with workflow-specific capabilities
type WorkerPool struct {
	workers         map[string]*WorkerConnection // worker_name -> connection
	config          PoolConfig
	mu              sync.RWMutex
	redisClient     *redis_client.Client
	ctx             context.Context
	cancel          context.CancelFunc
	roundRobinIndex int

	// Message processing queue for non-blocking operation
	messageQueue chan *streamMessage
	processWG    sync.WaitGroup

	// Sync callback system for synchronous workflow execution
	syncCallbacks map[string]func(interface{}) // requestID -> callback function
	callbackMu    sync.RWMutex
}

// NewWorkerPool creates a new general-purpose worker pool (accepts all workflow types)
func NewWorkerPool(redisClient *redis_client.Client) *WorkerPool {
	return NewWorkerPoolWithConfig(redisClient, PoolConfig{
		WorkflowFilter:  "", // Accept all workflows
		MinWorkers:      1,
		MaxWorkers:      64, // High throughput support
		LoadBalanceType: "least_connections",
	})
}

// NewWorkflowSpecificPool creates a worker pool for a specific workflow type
func NewWorkflowSpecificPool(redisClient *redis_client.Client, workflowName string, minWorkers, maxWorkers int) *WorkerPool {
	return NewWorkerPoolWithConfig(redisClient, PoolConfig{
		WorkflowFilter:  workflowName,
		MinWorkers:      minWorkers,
		MaxWorkers:      maxWorkers,
		LoadBalanceType: "least_connections",
	})
}

// NewWorkerPoolWithConfig creates a worker pool with custom configuration
func NewWorkerPoolWithConfig(redisClient *redis_client.Client, config PoolConfig) *WorkerPool {
	ctx, cancel := context.WithCancel(context.Background())

	pool := &WorkerPool{
		workers:       make(map[string]*WorkerConnection),
		config:        config,
		redisClient:   redisClient,
		ctx:           ctx,
		cancel:        cancel,
		messageQueue:  make(chan *streamMessage, 1000),
		syncCallbacks: make(map[string]func(interface{})),
	}

	log.Printf("Creating worker pool with config: workflow_filter=%s, min_workers=%d, max_workers=%d",
		config.WorkflowFilter, config.MinWorkers, config.MaxWorkers)

	// Start message processing goroutines
	pool.startMessageProcessors()

	// Start background processes
	go pool.monitorWorkerEvents()
	go pool.loadExistingWorkers()
	go pool.performHealthChecks()

	return pool
}

// startMessageProcessors starts goroutines to process messages from workers
func (wp *WorkerPool) startMessageProcessors() {
	for i := 0; i < 64; i++ { // Start 64 message processors for high throughput
		wp.processWG.Add(1)
		go func() {
			defer wp.processWG.Done()
			for {
				select {
				case msg := <-wp.messageQueue:
					wp.processWorkerMessage(msg.worker, msg.response)
				case <-wp.ctx.Done():
					return
				}
			}
		}()
	}
}

// Submit submits a message to the worker pool queue
func (wp *WorkerPool) Submit(msg *streamMessage) bool {
	select {
	case wp.messageQueue <- msg:
		return true
	default:
		return false // Queue full
	}
}

// monitorWorkerEvents listens for worker registration/deregistration events from Redis
func (wp *WorkerPool) monitorWorkerEvents() {
	log.Printf("Starting worker event monitoring for pool (filter: %s)", wp.config.WorkflowFilter)

	// Subscribe to worker events using the redis client
	eventCh := wp.redisClient.SubscribeToWorkerEvents()

	for {
		select {
		case event := <-eventCh:
			// Apply workflow filter if set
			if !wp.shouldAcceptWorker(event.Worker) {
				log.Printf("Ignoring worker %s (doesn't match filter: %s)", event.Worker.Name, wp.config.WorkflowFilter)
				continue
			}

			switch event.Type {
			case "worker_online":
				log.Printf("Worker came online: %s at %s", event.Worker.Name, event.Worker.Endpoint)
				if wp.canAcceptMoreWorkers() {
					wp.connectToWorker(event.Worker)
				} else {
					log.Printf("Pool at capacity, not connecting to worker %s", event.Worker.Name)
				}
			case "worker_offline":
				log.Printf("Worker went offline: %s", event.Worker.Name)
				wp.disconnectWorker(event.Worker.Name)
			}

		case <-wp.ctx.Done():
			return
		}
	}
}

// shouldAcceptWorker determines if a worker should be accepted by this pool
func (wp *WorkerPool) shouldAcceptWorker(workerInfo redis_client.WorkerInfo) bool {
	// If no filter is set, accept all workers
	if wp.config.WorkflowFilter == "" {
		return true
	}

	// For workflow-specific pools, check if worker supports the required workflow type
	for _, workflowType := range workerInfo.WorkflowTypes {
		if workflowType == wp.config.WorkflowFilter {
			return true
		}
	}

	return false
}

// canAcceptMoreWorkers checks if the pool can accept more worker connections
func (wp *WorkerPool) canAcceptMoreWorkers() bool {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	currentWorkers := len(wp.workers)
	return currentWorkers < wp.config.MaxWorkers
}

// loadExistingWorkers connects to workers that are already registered in Redis
func (wp *WorkerPool) loadExistingWorkers() {
	workers, err := wp.redisClient.GetAllWorkers()
	if err != nil {
		log.Printf("Error loading existing workers: %v", err)
		return
	}

	log.Printf("Found %d existing workers in Redis", len(workers))

	for _, worker := range workers {
		if !wp.shouldAcceptWorker(worker) {
			continue
		}

		if !wp.canAcceptMoreWorkers() {
			log.Printf("Pool at capacity, skipping worker %s", worker.Name)
			continue
		}

		log.Printf("Connecting to existing worker: %s at %s", worker.Name, worker.Endpoint)
		if err := wp.connectToWorker(worker); err != nil {
			log.Printf("Failed to connect to existing worker %s: %v", worker.Name, err)
		}
	}
}

// connectToWorker establishes a gRPC connection to a worker
func (wp *WorkerPool) connectToWorker(workerInfo redis_client.WorkerInfo) error {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	// Check if already connected
	if conn, exists := wp.workers[workerInfo.Name]; exists && conn.Connected {
		log.Printf("Already connected to worker %s", workerInfo.Name)
		return nil
	}

	log.Printf("Establishing gRPC connection to worker %s at %s", workerInfo.Name, workerInfo.Endpoint)

	// Create gRPC connection with timeout
	grpcConn, err := grpc.NewClient(workerInfo.Endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to worker: %v", err)
	}

	client := pb.NewWorkerServiceClient(grpcConn)

	// Test connection with health check
	healthCtx, healthCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer healthCancel()

	healthResp, err := client.HealthCheck(healthCtx, &pb.HealthCheckRequest{})
	if err != nil {
		grpcConn.Close()
		return fmt.Errorf("health check failed: %v", err)
	}

	// Parse worker capabilities from health response metadata
	workflowTypes := workerInfo.WorkflowTypes // Use the types from Redis registration
	maxCapacity := int32(10)                  // Default capacity

	if healthResp.Metadata != nil {
		// Check for registered_workflows first (from worker health check)
		if wfTypes, ok := healthResp.Metadata["registered_workflows"]; ok {
			workflowTypes = parseWorkflowTypes(wfTypes)
		}
		// Also check workflow_types for backward compatibility
		if wfTypes, ok := healthResp.Metadata["workflow_types"]; ok {
			workflowTypes = parseWorkflowTypes(wfTypes)
		}
		if cap, ok := healthResp.Metadata["max_capacity"]; ok {
			if parsedCap := parseCapacity(cap); parsedCap > 0 {
				maxCapacity = parsedCap
			}
		}
	}

	// If no workflow types found in health response, fall back to Redis registration
	if len(workflowTypes) == 0 {
		workflowTypes = workerInfo.WorkflowTypes
	}

	// Create streaming connection
	streamCtx, streamCancel := context.WithCancel(wp.ctx)
	stream, err := client.WorkflowStream(streamCtx)
	if err != nil {
		streamCancel()
		grpcConn.Close()
		return fmt.Errorf("failed to create stream: %v", err)
	}

	workerConn := &WorkerConnection{
		Name:          workerInfo.Name,
		Endpoint:      workerInfo.Endpoint,
		WorkflowTypes: workflowTypes,
		Conn:          grpcConn,
		Client:        client,
		Stream:        stream,
		Connected:     true,
		LastSeen:      time.Now(),
		MaxCapacity:   maxCapacity,
		ctx:           streamCtx,
		cancel:        streamCancel,
	}

	wp.workers[workerInfo.Name] = workerConn

	log.Printf("Successfully connected to worker %s (capacity: %d, workflows: %v)",
		workerInfo.Name, maxCapacity, workflowTypes)

	// Start background processes for this worker
	go wp.monitorWorkerConnection(workerConn)
	go wp.handleWorkerStream(workerConn)

	return nil
}

// disconnectWorker disconnects and removes a worker
func (wp *WorkerPool) disconnectWorker(workerName string) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	if conn, exists := wp.workers[workerName]; exists {
		if conn.cancel != nil {
			conn.cancel()
		}
		if conn.Conn != nil {
			conn.Conn.Close()
		}
		delete(wp.workers, workerName)
		log.Printf("Disconnected worker: %s", workerName)
	}
}

// performHealthChecks periodically checks worker health
func (wp *WorkerPool) performHealthChecks() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			wp.checkAllWorkersHealth()
		case <-wp.ctx.Done():
			return
		}
	}
}

// checkAllWorkersHealth performs health checks on all connected workers
func (wp *WorkerPool) checkAllWorkersHealth() {
	wp.mu.RLock()
	workers := make([]*WorkerConnection, 0, len(wp.workers))
	for _, worker := range wp.workers {
		workers = append(workers, worker)
	}
	wp.mu.RUnlock()

	for _, worker := range workers {
		if worker.Connected {
			go wp.checkWorkerHealth(worker)
		}
	}
}

// checkWorkerHealth performs a health check on a specific worker
func (wp *WorkerPool) checkWorkerHealth(conn *WorkerConnection) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := conn.Client.HealthCheck(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		log.Printf("Health check failed for worker %s: %v", conn.Name, err)
		wp.disconnectWorker(conn.Name)
		return
	}

	conn.mu.Lock()
	conn.LastSeen = time.Now()
	conn.mu.Unlock()
}

// monitorWorkerConnection monitors the health of a worker connection
func (wp *WorkerPool) monitorWorkerConnection(conn *WorkerConnection) {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			// Check if worker has been silent for too long
			conn.mu.RLock()
			lastSeen := conn.LastSeen
			conn.mu.RUnlock()

			if time.Since(lastSeen) > 2*time.Minute {
				log.Printf("Worker %s has been silent for too long, disconnecting", conn.Name)
				wp.disconnectWorker(conn.Name)
				return
			}

		case <-conn.ctx.Done():
			return
		}
	}
}

// GetAvailableWorker returns the best available worker using least connections (for backward compatibility)
func (wp *WorkerPool) GetAvailableWorker() (*WorkerConnection, error) {
	return wp.getAvailableWorkerLeastConnections("")
}

// GetAvailableWorkerRoundRobin returns worker using round-robin selection (for backward compatibility)
func (wp *WorkerPool) GetAvailableWorkerRoundRobin() (*WorkerConnection, error) {
	return wp.getAvailableWorkerRoundRobin("")
}

// GetAvailableWorkerForWorkflow returns the best available worker for a specific workflow
func (wp *WorkerPool) GetAvailableWorkerForWorkflow(workflowName string) (*WorkerConnection, error) {
	// First, let's log some debug information
	wp.mu.RLock()
	totalWorkers := len(wp.workers)
	connectedWorkers := 0
	compatibleWorkers := 0

	log.Printf("=== DEBUG: Looking for workers for workflow '%s' ===", workflowName)
	log.Printf("Pool config: filter='%s', max_workers=%d", wp.config.WorkflowFilter, wp.config.MaxWorkers)
	log.Printf("Total workers in pool: %d", totalWorkers)

	for name, worker := range wp.workers {
		if worker.Connected {
			connectedWorkers++
			log.Printf("Connected worker: %s, endpoint: %s, workflow_types: %v",
				name, worker.Endpoint, worker.WorkflowTypes)

			if wp.canWorkerHandleWorkflow(worker, workflowName) {
				compatibleWorkers++
				log.Printf("  -> Can handle workflow '%s'", workflowName)
			} else {
				log.Printf("  -> Cannot handle workflow '%s'", workflowName)
			}
		} else {
			log.Printf("Disconnected worker: %s", name)
		}
	}
	wp.mu.RUnlock()

	log.Printf("Connected workers: %d, Compatible workers: %d", connectedWorkers, compatibleWorkers)

	switch wp.config.LoadBalanceType {
	case "round_robin":
		return wp.getAvailableWorkerRoundRobin(workflowName)
	case "workflow_affinity":
		return wp.getAvailableWorkerWithAffinity(workflowName)
	default: // "least_connections"
		return wp.getAvailableWorkerLeastConnections(workflowName)
	}
}

// getAvailableWorkerLeastConnections returns worker with least active tasks
func (wp *WorkerPool) getAvailableWorkerLeastConnections(workflowName string) (*WorkerConnection, error) {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	var bestWorker *WorkerConnection
	minActiveTasks := int32(^uint32(0) >> 1) // Max int32

	for _, worker := range wp.workers {
		if !worker.Connected {
			continue
		}

		// Check if worker can handle this workflow (if specified)
		if workflowName != "" && !wp.canWorkerHandleWorkflow(worker, workflowName) {
			continue
		}

		worker.mu.RLock()
		activeTasks := worker.ActiveTasks
		maxCapacity := worker.MaxCapacity
		worker.mu.RUnlock()

		// Skip if worker is at capacity
		if activeTasks >= maxCapacity {
			continue
		}

		if activeTasks < minActiveTasks {
			minActiveTasks = activeTasks
			bestWorker = worker
		}
	}

	if bestWorker == nil {
		if workflowName != "" {
			return nil, fmt.Errorf("no available workers for workflow: %s", workflowName)
		}
		return nil, fmt.Errorf("no available workers")
	}

	return bestWorker, nil
}

// getAvailableWorkerRoundRobin returns worker using round-robin selection
func (wp *WorkerPool) getAvailableWorkerRoundRobin(workflowName string) (*WorkerConnection, error) {
	wp.mu.Lock()
	defer wp.mu.Unlock()

	// Get list of eligible workers
	var eligibleWorkers []*WorkerConnection
	for _, worker := range wp.workers {
		if !worker.Connected {
			continue
		}

		if workflowName != "" && !wp.canWorkerHandleWorkflow(worker, workflowName) {
			continue
		}

		worker.mu.RLock()
		atCapacity := worker.ActiveTasks >= worker.MaxCapacity
		worker.mu.RUnlock()

		if !atCapacity {
			eligibleWorkers = append(eligibleWorkers, worker)
		}
	}

	if len(eligibleWorkers) == 0 {
		if workflowName != "" {
			return nil, fmt.Errorf("no available workers for workflow: %s", workflowName)
		}
		return nil, fmt.Errorf("no available workers")
	}

	// Use round-robin to select worker
	selectedWorker := eligibleWorkers[wp.roundRobinIndex%len(eligibleWorkers)]
	wp.roundRobinIndex++

	return selectedWorker, nil
}

// getAvailableWorkerWithAffinity prefers workers that exactly match the workflow name
func (wp *WorkerPool) getAvailableWorkerWithAffinity(workflowName string) (*WorkerConnection, error) {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	var exactMatch *WorkerConnection
	var fallbackMatch *WorkerConnection
	minActiveTasks := int32(^uint32(0) >> 1)

	for _, worker := range wp.workers {
		if !worker.Connected {
			continue
		}

		if workflowName != "" && !wp.canWorkerHandleWorkflow(worker, workflowName) {
			continue
		}

		worker.mu.RLock()
		activeTasks := worker.ActiveTasks
		maxCapacity := worker.MaxCapacity
		worker.mu.RUnlock()

		if activeTasks >= maxCapacity {
			continue
		}

		// Prefer exact name match (workflow-specific worker)
		if workflowName != "" && worker.Name == workflowName {
			if activeTasks < minActiveTasks {
				minActiveTasks = activeTasks
				exactMatch = worker
			}
		} else {
			// Fallback to any compatible worker
			if fallbackMatch == nil || activeTasks < minActiveTasks {
				fallbackMatch = worker
			}
		}
	}

	if exactMatch != nil {
		return exactMatch, nil
	}

	if fallbackMatch != nil {
		return fallbackMatch, nil
	}

	if workflowName != "" {
		return nil, fmt.Errorf("no available workers for workflow: %s", workflowName)
	}
	return nil, fmt.Errorf("no available workers")
}

// canWorkerHandleWorkflow checks if a worker can handle a specific workflow type
func (wp *WorkerPool) canWorkerHandleWorkflow(worker *WorkerConnection, workflowName string) bool {
	// If pool has a workflow filter, it only connects to compatible workers
	if wp.config.WorkflowFilter != "" {
		return wp.config.WorkflowFilter == workflowName
	}

	// If no workflow name specified, any worker is acceptable
	if workflowName == "" {
		return true
	}

	// Check worker's supported workflow types
	for _, supportedType := range worker.WorkflowTypes {
		if supportedType == workflowName {
			return true
		}
	}

	return false
}

// ExecuteWorkflow sends a workflow execution request to an available worker (async)
func (wp *WorkerPool) ExecuteWorkflow(workflowName, requestID string, workflowID int64, payload string) error {
	worker, err := wp.GetAvailableWorkerForWorkflow(workflowName)
	if err != nil {
		return err
	}

	// Increment active tasks for load balancing
	worker.mu.Lock()
	worker.ActiveTasks++
	worker.TotalTasks++
	worker.mu.Unlock()

	req := &pb.ServerToWorkerMessage{
		MessageType: &pb.ServerToWorkerMessage_ExecutionRequest{
			ExecutionRequest: &pb.WorkflowExecutionRequest{
				WorkflowName: workflowName,
				RequestId:    requestID,
				WorkflowId:   workflowID,
				Payload:      payload,
			},
		},
	}

	err = worker.Stream.Send(req)
	if err != nil {
		// Decrement active tasks if send failed
		worker.mu.Lock()
		worker.ActiveTasks--
		worker.mu.Unlock()

		log.Printf("Failed to send workflow execution request to worker %s: %v", worker.Name, err)
		wp.disconnectWorker(worker.Name)
		return fmt.Errorf("failed to send request to worker: %v", err)
	}

	log.Printf("Sent workflow execution request to worker %s: workflow=%s, requestID=%s (active: %d/%d)",
		worker.Name, workflowName, requestID, worker.ActiveTasks, worker.MaxCapacity)
	return nil
}

// ExecuteWorkflowSync executes a workflow synchronously and waits for the result
func (wp *WorkerPool) ExecuteWorkflowSync(workflowName, requestID string, workflowID int64, payload string, timeout time.Duration) (*WorkflowResult, error) {
	worker, err := wp.GetAvailableWorkerForWorkflow(workflowName)
	if err != nil {
		return nil, err
	}

	// Increment active tasks for load balancing
	worker.mu.Lock()
	worker.ActiveTasks++
	worker.TotalTasks++
	worker.mu.Unlock()

	// Create channels for result communication
	resultCh := make(chan *WorkflowResult, 1)
	errorCh := make(chan error, 1)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Track state updates
	states := make([]StateUpdate, 0)
	var statesMu sync.Mutex

	// Create a temporary subscription for this specific workflow execution
	go func() {
		defer func() {
			// Decrement active tasks when done
			worker.mu.Lock()
			if worker.ActiveTasks > 0 {
				worker.ActiveTasks--
			}
			worker.mu.Unlock()
		}()

		// Wait for context cancellation or completion
		<-ctx.Done()
	}()

	// Register a callback for this specific request
	wp.registerSyncCallback(requestID, func(msgType interface{}) {
		switch msg := msgType.(type) {
		case *pb.WorkerToServerMessage_StateUpdate:
			statesMu.Lock()
			var stateData map[string]interface{}
			if msg.StateUpdate.Data != "" {
				json.Unmarshal([]byte(msg.StateUpdate.Data), &stateData)
			}

			states = append(states, StateUpdate{
				StateName: msg.StateUpdate.StateName,
				StateType: msg.StateUpdate.StateType,
				Status:    msg.StateUpdate.Status,
				Data:      stateData,
				Timestamp: time.Now(),
			})
			statesMu.Unlock()

		case *pb.WorkerToServerMessage_WorkflowComplete:
			var resultData map[string]interface{}
			if msg.WorkflowComplete.Variables != "" {
				json.Unmarshal([]byte(msg.WorkflowComplete.Variables), &resultData)
			}

			statesMu.Lock()
			finalStates := make([]StateUpdate, len(states))
			copy(finalStates, states)
			statesMu.Unlock()

			result := &WorkflowResult{
				Success:    msg.WorkflowComplete.Status == "s" || msg.WorkflowComplete.Status == "success",
				Status:     msg.WorkflowComplete.Status,
				Result:     resultData,
				WorkflowID: workflowID,
				RequestID:  requestID,
				States:     finalStates,
			}

			if msg.WorkflowComplete.Status == "f" || msg.WorkflowComplete.Status == "failed" {
				result.Error = "Workflow execution failed"
			}

			select {
			case resultCh <- result:
			default:
			}

		case *pb.WorkerToServerMessage_ExecutionResponse:
			if !msg.ExecutionResponse.Success {
				errorCh <- fmt.Errorf("workflow execution failed: %s", msg.ExecutionResponse.Message)
			}
		}
	})

	// Send the execution request
	req := &pb.ServerToWorkerMessage{
		MessageType: &pb.ServerToWorkerMessage_ExecutionRequest{
			ExecutionRequest: &pb.WorkflowExecutionRequest{
				WorkflowName: workflowName,
				RequestId:    requestID,
				WorkflowId:   workflowID,
				Payload:      payload,
			},
		},
	}

	err = worker.Stream.Send(req)
	if err != nil {
		wp.unregisterSyncCallback(requestID)
		worker.mu.Lock()
		worker.ActiveTasks--
		worker.mu.Unlock()

		log.Printf("Failed to send sync workflow execution request to worker %s: %v", worker.Name, err)
		wp.disconnectWorker(worker.Name)
		return nil, fmt.Errorf("failed to send request to worker: %v", err)
	}

	log.Printf("Sent sync workflow execution request to worker %s: workflow=%s, requestID=%s (active: %d/%d)",
		worker.Name, workflowName, requestID, worker.ActiveTasks, worker.MaxCapacity)

	// Wait for result or timeout
	select {
	case result := <-resultCh:
		wp.unregisterSyncCallback(requestID)
		return result, nil
	case err := <-errorCh:
		wp.unregisterSyncCallback(requestID)
		return nil, err
	case <-ctx.Done():
		wp.unregisterSyncCallback(requestID)
		return nil, fmt.Errorf("workflow execution timeout after %v", timeout)
	}
}

// GetWorkerByName returns a specific worker by name
func (wp *WorkerPool) GetWorkerByName(name string) (*WorkerConnection, error) {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	if worker, exists := wp.workers[name]; exists && worker.Connected {
		return worker, nil
	}

	return nil, fmt.Errorf("worker not found or not connected: %s", name)
}

// GetAllWorkers returns all worker connections
func (wp *WorkerPool) GetAllWorkers() map[string]*WorkerConnection {
	wp.mu.RLock()
	defer wp.mu.RUnlock()

	workers := make(map[string]*WorkerConnection)
	for name, worker := range wp.workers {
		workers[name] = worker
	}

	return workers
}

// GetLoadBalancingStats returns load balancing statistics
func (wp *WorkerPool) GetLoadBalancingStats() map[string]interface{} {
	wp.mu.RLock()
	workers := make([]*WorkerConnection, 0, len(wp.workers))
	for _, worker := range wp.workers {
		workers = append(workers, worker)
	}
	wp.mu.RUnlock()

	activeWorkers := 0
	totalActiveTasks := int32(0)
	totalCapacity := int32(0)
	workerStats := make(map[string]interface{})

	for _, worker := range workers {
		if worker.Connected {
			activeWorkers++
		}

		worker.mu.RLock()
		totalActiveTasks += worker.ActiveTasks
		totalCapacity += worker.MaxCapacity

		workerStats[worker.Name] = map[string]interface{}{
			"connected":      worker.Connected,
			"endpoint":       worker.Endpoint,
			"workflow_types": worker.WorkflowTypes,
			"active_tasks":   worker.ActiveTasks,
			"total_tasks":    worker.TotalTasks,
			"max_capacity":   worker.MaxCapacity,
			"utilization": func() float64 {
				if worker.MaxCapacity > 0 {
					return float64(worker.ActiveTasks) / float64(worker.MaxCapacity) * 100
				}
				return 0
			}(),
			"last_seen": worker.LastSeen.Format(time.RFC3339),
		}
		worker.mu.RUnlock()
	}

	utilizationPercent := float64(0)
	if totalCapacity > 0 {
		utilizationPercent = float64(totalActiveTasks) / float64(totalCapacity) * 100
	}

	return map[string]interface{}{
		"pool_config": map[string]interface{}{
			"workflow_filter":   wp.config.WorkflowFilter,
			"min_workers":       wp.config.MinWorkers,
			"max_workers":       wp.config.MaxWorkers,
			"load_balance_type": wp.config.LoadBalanceType,
		},
		"current_state": map[string]interface{}{
			"total_workers":       len(workers),
			"active_workers":      activeWorkers,
			"total_active_tasks":  totalActiveTasks,
			"total_capacity":      totalCapacity,
			"utilization_percent": utilizationPercent,
			"round_robin_index":   wp.roundRobinIndex,
		},
		"workers": workerStats,
	}
}

// handleWorkerStream handles incoming messages from a worker
func (wp *WorkerPool) handleWorkerStream(conn *WorkerConnection) {
	for {
		select {
		case <-conn.ctx.Done():
			return
		default:
			resp, err := conn.Stream.Recv()
			if err != nil {
				log.Printf("Error receiving from worker %s: %v", conn.Name, err)
				wp.disconnectWorker(conn.Name)
				return
			}

			// Submit message to worker pool for non-blocking processing
			msg := &streamMessage{
				worker:   conn,
				response: resp,
			}

			// Try to submit to worker pool, handle backpressure
			if !wp.Submit(msg) {
				log.Printf("Worker pool queue full, dropping message from worker %s", conn.Name)
			}
		}
	}
}

// registerSyncCallback registers a callback for synchronous workflow execution
func (wp *WorkerPool) registerSyncCallback(requestID string, callback func(interface{})) {
	wp.callbackMu.Lock()
	defer wp.callbackMu.Unlock()
	wp.syncCallbacks[requestID] = callback
}

// unregisterSyncCallback removes a callback for synchronous workflow execution
func (wp *WorkerPool) unregisterSyncCallback(requestID string) {
	wp.callbackMu.Lock()
	defer wp.callbackMu.Unlock()
	delete(wp.syncCallbacks, requestID)
}

// processWorkerMessage processes messages received from workers
func (wp *WorkerPool) processWorkerMessage(conn *WorkerConnection, resp *pb.WorkerToServerMessage) {
	// Update last seen time
	conn.mu.Lock()
	conn.LastSeen = time.Now()
	conn.mu.Unlock()

	// Extract request ID for sync callbacks (if available)
	var requestID string
	switch msg := resp.MessageType.(type) {
	case *pb.WorkerToServerMessage_ExecutionResponse:
		log.Printf("Received execution response from worker %s: success=%v",
			conn.Name, msg.ExecutionResponse.Success)

		// Check for sync callback
		wp.callbackMu.RLock()
		if callback, exists := wp.syncCallbacks[requestID]; exists {
			callback(msg)
		}
		wp.callbackMu.RUnlock()

	case *pb.WorkerToServerMessage_StateUpdate:
		log.Printf("Received state update from worker %s: workflow=%d, state=%s, status=%s",
			conn.Name, msg.StateUpdate.WorkflowId, msg.StateUpdate.StateName, msg.StateUpdate.Status)

		// Try to find sync callback by checking all registered callbacks
		// Note: We'll need to store requestID in state updates for better tracking
		wp.callbackMu.RLock()
		for _, callback := range wp.syncCallbacks {
			callback(msg)
		}
		wp.callbackMu.RUnlock()

	case *pb.WorkerToServerMessage_WorkflowComplete:
		log.Printf("Received workflow completion from worker %s: workflow=%d, status=%s",
			conn.Name, msg.WorkflowComplete.WorkflowId, msg.WorkflowComplete.Status)

		// Decrement active tasks when workflow completes
		conn.mu.Lock()
		if conn.ActiveTasks > 0 {
			conn.ActiveTasks--
		}
		conn.mu.Unlock()

		// Check for sync callback
		wp.callbackMu.RLock()
		for _, callback := range wp.syncCallbacks {
			callback(msg)
		}
		wp.callbackMu.RUnlock()

	default:
		log.Printf("Received unknown message type from worker %s", conn.Name)
	}
}

// Close closes all worker connections and stops the pool
func (wp *WorkerPool) Close() {
	log.Printf("Closing worker pool (filter: %s)", wp.config.WorkflowFilter)

	wp.mu.Lock()
	defer wp.mu.Unlock()

	if wp.cancel != nil {
		wp.cancel()
	}

	// Wait for message processors to finish
	wp.processWG.Wait()

	for name, worker := range wp.workers {
		if worker.cancel != nil {
			worker.cancel()
		}
		if worker.Conn != nil {
			worker.Conn.Close()
		}
		log.Printf("Closed connection to worker: %s", name)
	}

	wp.workers = make(map[string]*WorkerConnection)
}

// Helper functions

// parseWorkflowTypes parses comma-separated workflow types
func parseWorkflowTypes(typesStr string) []string {
	if typesStr == "" {
		return []string{}
	}

	types := strings.Split(typesStr, ",")
	result := make([]string, 0, len(types))
	for _, t := range types {
		if trimmed := strings.TrimSpace(t); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// parseCapacity parses capacity from string
func parseCapacity(capStr string) int32 {
	if capStr == "" {
		return 0
	}

	capacity, err := strconv.ParseInt(capStr, 10, 32)
	if err != nil {
		log.Printf("Failed to parse capacity '%s': %v", capStr, err)
		return 0
	}

	return int32(capacity)
}
