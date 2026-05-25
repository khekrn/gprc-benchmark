// Package grpc implements the gRPC server for the workflow worker.
// It handles bidirectional streaming communication with workflow servers
// and processes workflow execution requests.
package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"workflow-worker/internal/engine"
	pb "workflow-worker/proto"
)

// WorkerServer implements the WorkerService
type WorkerServer struct {
	pb.UnimplementedWorkerServiceServer
	engine        *engine.WorkflowEngine
	activeStreams map[string]pb.WorkerService_WorkflowStreamServer
	mu            sync.RWMutex
}

// NewWorkerServer creates a new worker server
func NewWorkerServer(workflowEngine *engine.WorkflowEngine) *WorkerServer {
	return &WorkerServer{
		engine:        workflowEngine,
		activeStreams: make(map[string]pb.WorkerService_WorkflowStreamServer),
	}
}

// HealthCheck implements the health check endpoint
func (s *WorkerServer) HealthCheck(ctx context.Context, req *pb.HealthCheckRequest) (*pb.HealthCheckResponse, error) {
	workflows := s.engine.GetRegisteredWorkflows()

	metadata := make(map[string]string)
	metadata["registered_workflows"] = strings.Join(workflows, ",")
	metadata["active_streams"] = fmt.Sprintf("%d", len(s.activeStreams))

	return &pb.HealthCheckResponse{
		Healthy:  true,
		Message:  "Worker is healthy and ready to process workflows",
		Metadata: metadata,
	}, nil
}

// WorkflowStream handles bidirectional streaming for workflow execution
func (s *WorkerServer) WorkflowStream(stream pb.WorkerService_WorkflowStreamServer) error {
	streamID := fmt.Sprintf("stream_%d", len(s.activeStreams))

	s.mu.Lock()
	s.activeStreams[streamID] = stream
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.activeStreams, streamID)
		s.mu.Unlock()
		log.Printf("Stream %s closed", streamID)
	}()

	log.Printf("New stream established: %s", streamID)

	// Handle incoming messages from the server
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			log.Printf("Stream %s closed by server", streamID)
			break
		}
		if err != nil {
			log.Printf("Error receiving from stream %s: %v", streamID, err)
			return status.Errorf(codes.Internal, "stream error: %v", err)
		}

		// Process the request
		err = s.handleRequest(req, stream, streamID)
		if err != nil {
			log.Printf("Error handling request in stream %s: %v", streamID, err)
			return err
		}
	}

	return nil
}

// handleRequest processes incoming requests from the server
func (s *WorkerServer) handleRequest(req *pb.ServerToWorkerMessage, stream pb.WorkerService_WorkflowStreamServer, streamID string) error {
	switch msg := req.MessageType.(type) {
	case *pb.ServerToWorkerMessage_ExecutionRequest:
		return s.handleExecutionRequest(msg.ExecutionRequest, stream, streamID)
	default:
		return status.Errorf(codes.InvalidArgument, "unknown message type")
	}
}

// handleExecutionRequest handles workflow execution requests
func (s *WorkerServer) handleExecutionRequest(req *pb.WorkflowExecutionRequest, stream pb.WorkerService_WorkflowStreamServer, streamID string) error {
	log.Printf("Received workflow execution request: workflow=%s, requestID=%s", req.WorkflowName, req.RequestId)

	// Parse the payload
	var payload map[string]interface{}
	if req.Payload != "" {
		err := json.Unmarshal([]byte(req.Payload), &payload)
		if err != nil {
			log.Printf("Failed to parse payload: %v", err)
			return s.sendExecutionResponse(stream, false, fmt.Sprintf("Failed to parse payload: %v", err), 0)
		}
	}

	// Send execution response first (acknowledgment)
	err := s.sendExecutionResponse(stream, true, "Workflow execution started", 0)
	if err != nil {
		return err
	}

	// Execute the workflow asynchronously
	go func() {
		// Use the workflow ID from the server request
		workflowID := req.WorkflowId

		// Set callbacks for this specific execution
		s.engine.SetStateUpdateCallback(func(wfID int64, stateName, stateType, statusValue string, data map[string]interface{}) error {
			// Convert data to JSON string
			dataBytes, _ := json.Marshal(data)
			return s.sendStateUpdate(stream, wfID, stateName, stateType, statusValue, string(dataBytes))
		})

		s.engine.SetWorkflowCompleteCallback(func(wfID int64, statusValue string, variables map[string]interface{}) error {
			// Convert variables to JSON string
			variablesBytes, _ := json.Marshal(variables)
			return s.sendWorkflowComplete(stream, wfID, statusValue, string(variablesBytes))
		})

		err := s.engine.ExecuteWorkflow(req.WorkflowName, req.RequestId, workflowID, payload)
		if err != nil {
			log.Printf("Workflow execution failed: %v", err)
			// Send failure state update
			s.sendStateUpdate(stream, workflowID, "workflow_execution", "task", "failed", fmt.Sprintf(`{"error": "%s"}`, err.Error()))
		}
	}()

	return nil
}

// handleStateUpdate is not needed in the new architecture (worker sends to server only)
func (s *WorkerServer) handleStateUpdate(req *pb.StateUpdateRequest, stream pb.WorkerService_WorkflowStreamServer, streamID string) error {
	log.Printf("Received state update request from server - this should not happen in the new architecture")
	return nil
}

// handleWorkflowComplete is not needed in the new architecture (worker sends to server only)
func (s *WorkerServer) handleWorkflowComplete(req *pb.WorkflowCompleteRequest, stream pb.WorkerService_WorkflowStreamServer, streamID string) error {
	log.Printf("Received workflow complete request from server - this should not happen in the new architecture")
	return nil
}

// sendExecutionResponse sends a workflow execution response
func (s *WorkerServer) sendExecutionResponse(stream pb.WorkerService_WorkflowStreamServer, success bool, message string, workflowID int64) error {
	resp := &pb.WorkerToServerMessage{
		MessageType: &pb.WorkerToServerMessage_ExecutionResponse{
			ExecutionResponse: &pb.WorkflowExecutionResponse{
				Success:    success,
				Message:    message,
				WorkflowId: workflowID,
			},
		},
	}

	return stream.Send(resp)
}

// sendStateUpdate sends a state update to the server
func (s *WorkerServer) sendStateUpdate(stream pb.WorkerService_WorkflowStreamServer, workflowID int64, stateName, stateType, statusValue, data string) error {
	resp := &pb.WorkerToServerMessage{
		MessageType: &pb.WorkerToServerMessage_StateUpdate{
			StateUpdate: &pb.StateUpdateRequest{
				WorkflowId: workflowID,
				StateName:  stateName,
				StateType:  stateType,
				Status:     statusValue,
				Data:       data,
			},
		},
	}

	log.Printf("Sending state update to server: workflowID=%d, state=%s, status=%s", workflowID, stateName, statusValue)
	err := stream.Send(resp)
	if err != nil {
		// Check if it's a connection closing error (normal when workflow completes)
		if status.Code(err) == codes.Unavailable || status.Code(err) == codes.Canceled {
			log.Printf("Stream closed during state update for workflow %d (normal during completion)", workflowID)
			return nil // Don't treat as error
		}
		log.Printf("Error sending state update: %v", err)
		return err
	}
	return nil
}

// sendWorkflowComplete sends a workflow completion message back to the server
func (s *WorkerServer) sendWorkflowComplete(stream pb.WorkerService_WorkflowStreamServer, workflowID int64, statusValue, variables string) error {
	resp := &pb.WorkerToServerMessage{
		MessageType: &pb.WorkerToServerMessage_WorkflowComplete{
			WorkflowComplete: &pb.WorkflowCompleteRequest{
				WorkflowId: workflowID,
				Status:     statusValue,
				Variables:  variables,
			},
		},
	}

	log.Printf("Sending workflow completion to server: workflowID=%d, status=%s", workflowID, statusValue)
	err := stream.Send(resp)
	if err != nil {
		// Check if it's a connection closing error (normal when workflow completes)
		if status.Code(err) == codes.Unavailable || status.Code(err) == codes.Canceled {
			log.Printf("Stream closed during workflow completion for workflow %d (normal)", workflowID)
			return nil // Don't treat as error
		}
		log.Printf("Error sending workflow completion: %v", err)
		return err
	}
	return nil
}
