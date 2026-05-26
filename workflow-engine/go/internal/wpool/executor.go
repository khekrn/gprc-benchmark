package wpool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"workflow-engine/internal/database"
	"workflow-engine/internal/models"
	redis_client "workflow-engine/internal/redis"
	pb "workflow-engine/proto"
)

// WorkflowExecutor handles execution of a single workflow request
type WorkflowExecutor struct {
	workflowName string
	db           *database.DB
	redisClient  *redis_client.Client
	connections  map[string]*WorkerConnection // endpoint -> connection
	mu           sync.RWMutex
	ctx          context.Context
	cancel       context.CancelFunc
}

// NewWorkflowExecutor creates a new workflow executor
func NewWorkflowExecutor(db *database.DB, redisClient *redis_client.Client, workflowName string) *WorkflowExecutor {
	ctx, cancel := context.WithCancel(context.Background())

	return &WorkflowExecutor{
		workflowName: workflowName,
		db:           db,
		redisClient:  redisClient,
		connections:  make(map[string]*WorkerConnection),
		ctx:          ctx,
		cancel:       cancel,
	}
} // Execute executes a workflow with available workers
func (we *WorkflowExecutor) Execute(requestID string, workflowID int64, payload string, workers []redis_client.WorkerInfo) error {
	log.Printf("Executing workflow %s (ID: %d) with %d workers", we.workflowName, workflowID, len(workers))

	// Step 1: Establish streaming connections with all available workers
	err := we.connectToWorkers(workers)
	if err != nil {
		return fmt.Errorf("failed to connect to workers: %v", err)
	}

	if len(we.connections) == 0 {
		return fmt.Errorf("no workers available for streaming connection")
	}

	// Step 2: Select a worker using load balancing (simple round-robin for now)
	selectedWorker := we.selectWorker()
	if selectedWorker == nil {
		return fmt.Errorf("no available worker selected")
	}

	log.Printf("Selected worker %s for workflow execution", selectedWorker.Name)

	// Step 3: Send workflow execution request to selected worker
	executionRequest := &pb.WorkflowExecutionRequest{
		WorkflowName: we.workflowName,
		RequestId:    requestID,
		Payload:      payload,
		WorkflowId:   workflowID, // Include the actual workflow ID from database
	}

	serverMessage := &pb.ServerToWorkerMessage{
		MessageType: &pb.ServerToWorkerMessage_ExecutionRequest{
			ExecutionRequest: executionRequest,
		},
	}

	// Send the request through the streaming connection
	err = selectedWorker.Stream.Send(serverMessage)
	if err != nil {
		return fmt.Errorf("failed to send execution request to worker: %v", err)
	}

	log.Printf("Successfully sent workflow execution request to worker %s", selectedWorker.Name)

	// Create initial variable entry for the workflow
	initialVariables := models.JSONB{
		"workflow_started_at": time.Now().Unix(),
		"request_id":          requestID,
		"workflow_name":       we.workflowName,
		"status":              "initiated",
	}

	// Parse the payload and add it to initial variables
	var payloadData map[string]interface{}
	if payload != "" {
		if err := json.Unmarshal([]byte(payload), &payloadData); err == nil {
			initialVariables["payload"] = payloadData
		}
	}

	err = we.db.CreateOrUpdateVariables(workflowID, "workflow_start", initialVariables)
	if err != nil {
		log.Printf("Failed to create initial variables: %v", err)
	} else {
		log.Printf("Created initial variables for workflow %d", workflowID)
	}

	// Step 4: Wait for workflow completion (simplified - in real implementation, you'd handle this asynchronously)
	go we.handleWorkerResponses(selectedWorker, workflowID)

	return nil
}

// connectToWorkers establishes streaming connections with workers
func (we *WorkflowExecutor) connectToWorkers(workers []redis_client.WorkerInfo) error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errors []error

	for _, worker := range workers {
		wg.Add(1)
		go func(w redis_client.WorkerInfo) {
			defer wg.Done()

			if err := we.connectToWorker(w); err != nil {
				mu.Lock()
				errors = append(errors, fmt.Errorf("failed to connect to worker %s: %v", w.Name, err))
				mu.Unlock()
			}
		}(worker)
	}

	wg.Wait()

	if len(errors) > 0 && len(we.connections) == 0 {
		return fmt.Errorf("failed to connect to any workers: %v", errors)
	}

	return nil
}

// connectToWorker establishes a streaming connection with a single worker
func (we *WorkflowExecutor) connectToWorker(worker redis_client.WorkerInfo) error {
	log.Printf("Connecting to worker %s at %s", worker.Name, worker.Endpoint)

	// Create gRPC connection
	conn, err := grpc.Dial(worker.Endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to worker %s: %v", worker.Name, err)
	}

	// Create worker service client
	client := pb.NewWorkerServiceClient(conn)

	// Test connection with health check
	ctx, cancel := context.WithTimeout(we.ctx, 5*time.Second)
	defer cancel()

	_, err = client.HealthCheck(ctx, &pb.HealthCheckRequest{})
	if err != nil {
		conn.Close()
		return fmt.Errorf("health check failed for worker %s: %v", worker.Name, err)
	}

	// Establish bidirectional streaming connection
	stream, err := client.WorkflowStream(we.ctx)
	if err != nil {
		conn.Close()
		return fmt.Errorf("failed to create stream with worker %s: %v", worker.Name, err)
	}

	// Create worker connection
	workerConn := &WorkerConnection{
		Name:        worker.Name,
		Endpoint:    worker.Endpoint,
		Conn:        conn,
		Client:      client,
		Stream:      stream,
		Connected:   true,
		LastSeen:    time.Now(),
		ActiveTasks: 0,
		TotalTasks:  0,
		ctx:         we.ctx,
	}

	// Store connection
	we.mu.Lock()
	we.connections[worker.Endpoint] = workerConn
	we.mu.Unlock()

	log.Printf("Successfully connected to worker %s", worker.Name)
	return nil
}

// selectWorker selects an available worker for load balancing (simple round-robin)
func (we *WorkflowExecutor) selectWorker() *WorkerConnection {
	we.mu.RLock()
	defer we.mu.RUnlock()

	// Simple selection - pick the first available worker
	// In a real implementation, you'd implement proper load balancing
	for _, conn := range we.connections {
		if conn.Connected {
			return conn
		}
	}

	return nil
}

// handleWorkerResponses handles responses from the worker
func (we *WorkflowExecutor) handleWorkerResponses(worker *WorkerConnection, workflowID int64) {
	defer func() {
		log.Printf("Finished handling responses for workflow %d", workflowID)
	}()

	for {
		select {
		case <-we.ctx.Done():
			return
		default:
			// Receive message from worker
			response, err := worker.Stream.Recv()
			if err != nil {
				log.Printf("Error receiving from worker %s: %v", worker.Name, err)
				return
			}

			// Handle different response types
			switch msg := response.MessageType.(type) {
			case *pb.WorkerToServerMessage_ExecutionResponse:
				log.Printf("Received execution response from worker %s: success=%v, message=%s",
					worker.Name, msg.ExecutionResponse.Success, msg.ExecutionResponse.Message)

			case *pb.WorkerToServerMessage_StateUpdate:
				log.Printf("Received state update from worker %s: workflow_id=%d, state=%s, status=%s",
					worker.Name, msg.StateUpdate.WorkflowId, msg.StateUpdate.StateName, msg.StateUpdate.Status)

				// Save state update to database
				if msg.StateUpdate.Status == "p" { // pending - create new state
					_, err := we.db.CreateState(msg.StateUpdate.WorkflowId, msg.StateUpdate.StateName,
						msg.StateUpdate.StateType, msg.StateUpdate.Status)
					if err != nil {
						log.Printf("Failed to create state in database: %v", err)
					}
				} else { // success/failure - update existing state
					err := we.db.UpdateStateStatus(msg.StateUpdate.WorkflowId, msg.StateUpdate.StateName,
						msg.StateUpdate.Status)
					if err != nil {
						log.Printf("Failed to update state in database: %v", err)
					}
				}

				// Save variables if provided
				if msg.StateUpdate.Data != "" {
					var variablesData models.JSONB
					if err := json.Unmarshal([]byte(msg.StateUpdate.Data), &variablesData); err != nil {
						log.Printf("Failed to parse variables data: %v", err)
					} else {
						err := we.db.CreateOrUpdateVariables(msg.StateUpdate.WorkflowId, msg.StateUpdate.StateName, variablesData)
						if err != nil {
							log.Printf("Failed to save variables in database: %v", err)
						} else {
							log.Printf("Saved variables for workflow %d, task %s", msg.StateUpdate.WorkflowId, msg.StateUpdate.StateName)
						}
					}
				}

			case *pb.WorkerToServerMessage_WorkflowComplete:
				log.Printf("Workflow %d completed on worker %s: status=%s",
					msg.WorkflowComplete.WorkflowId, worker.Name, msg.WorkflowComplete.Status)

				// Save final variables if provided
				if msg.WorkflowComplete.Variables != "" {
					var variablesData models.JSONB
					if err := json.Unmarshal([]byte(msg.WorkflowComplete.Variables), &variablesData); err != nil {
						log.Printf("Failed to parse final variables data: %v", err)
					} else {
						err := we.db.CreateOrUpdateVariables(msg.WorkflowComplete.WorkflowId, "workflow_complete", variablesData)
						if err != nil {
							log.Printf("Failed to save final variables in database: %v", err)
						} else {
							log.Printf("Saved final variables for completed workflow %d", msg.WorkflowComplete.WorkflowId)
						}
					}
				}

				// Update workflow status to completed
				err := we.db.UpdateWorkflowStatus(msg.WorkflowComplete.WorkflowId, models.WorkflowStatusSuccess)
				if err != nil {
					log.Printf("Failed to update workflow status: %v", err)
				} else {
					log.Printf("Updated workflow %d status to success", msg.WorkflowComplete.WorkflowId)
				}

				// Workflow completed - close the executor
				go func() {
					// Close after a short delay to ensure all cleanup is done
					time.Sleep(100 * time.Millisecond)
					we.Close()
				}()
				return

			default:
				log.Printf("Received unknown message type from worker %s", worker.Name)
			}
		}
	}
}

// Close closes all worker connections
func (we *WorkflowExecutor) Close() {
	log.Printf("Closing workflow executor for %s", we.workflowName)

	we.cancel()

	we.mu.Lock()
	defer we.mu.Unlock()

	for endpoint, conn := range we.connections {
		if conn.Connected {
			if conn.Stream != nil {
				conn.Stream.CloseSend()
			}
			if conn.Conn != nil {
				conn.Conn.Close()
			}
		}
		delete(we.connections, endpoint)
	}

	log.Printf("All connections closed for workflow executor %s", we.workflowName)
}
