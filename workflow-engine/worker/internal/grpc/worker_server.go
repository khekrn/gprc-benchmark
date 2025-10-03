package grpc

import (
	"context"
	"log"

	"workflow-worker/internal/config"
	"workflow-worker/internal/engine"
	pb "workflow-worker/proto"
)

// WorkerServer implements the WorkerService for receiving workflow execution requests
type WorkerServer struct {
	pb.UnimplementedWorkerServiceServer
	engine *engine.WorkflowEngine
	client *Client // Reference to the main client for establishing streams back to server
	config *config.Config
}

// NewWorkerServer creates a new worker server
func NewWorkerServer(engine *engine.WorkflowEngine, client *Client, cfg *config.Config) *WorkerServer {
	return &WorkerServer{
		engine: engine,
		client: client,
		config: cfg,
	}
}

// StartWorkflow handles unary workflow start requests from the main server
func (ws *WorkerServer) StartWorkflow(ctx context.Context, req *pb.WorkflowExecutionRequest) (*pb.WorkflowExecutionResponse, error) {
	log.Printf("Received StartWorkflow request: %s (ID: %s)", req.WorkflowName, req.RequestId)

	// Start the workflow execution in a goroutine and establish stream back to server
	go ws.executeWorkflowWithStream(req)

	// Return immediate response
	return &pb.WorkflowExecutionResponse{
		Success:    true,
		Message:    "Workflow execution started",
		WorkflowId: 1, // This should be extracted from the request or generated
	}, nil
}

// executeWorkflowWithStream executes the workflow and ensures stream connection to server
func (ws *WorkerServer) executeWorkflowWithStream(req *pb.WorkflowExecutionRequest) {
	log.Printf("Establishing stream back to server for workflow: %s", req.WorkflowName)

	// Check if stream is already active, if not establish it
	// StartStream is idempotent - it won't create a new stream if one already exists
	err := ws.client.StartStream()
	if err != nil {
		log.Printf("Failed to establish stream back to server: %v", err)
		return
	}

	log.Printf("Stream connection ready for workflow: %s", req.WorkflowName)

	// Execute the workflow using the engine with the existing client
	testPayload := map[string]interface{}{
		"application_data": map[string]interface{}{
			"application_id": "TEST_APP_001",
			"amount":         50000,
			"applicant": map[string]interface{}{
				"name":    "John Doe",
				"pan":     "ABCDE1234F",
				"aadhaar": "123456789012",
				"email":   "john.doe@example.com",
				"phone":   "+919876543210",
			},
			"purpose": "Personal loan",
		},
		"amount": 50000,
	}

	err = ws.engine.ExecuteWorkflow("loan_approval", req.RequestId, 1, testPayload)
	if err != nil {
		log.Printf("Error executing workflow: %v", err)
	} else {
		log.Printf("Workflow %s completed successfully", req.WorkflowName)
	}

	// Note: We intentionally do NOT close the stream here
	// The stream will be reused for subsequent workflow executions
	log.Printf("Workflow %s completed, stream connection remains active for reuse", req.WorkflowName)
}

// ExecuteWorkflow is kept for interface compatibility but not used in new architecture
func (ws *WorkerServer) ExecuteWorkflow(stream pb.WorkerService_ExecuteWorkflowServer) error {
	log.Println("ExecuteWorkflow stream method called but not used in new architecture")
	return nil
}
