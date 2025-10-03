package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"workflow-engine/internal/database"
	"workflow-engine/internal/models"
	pb "workflow-engine/proto"
)

type WorkflowServer struct {
	pb.UnimplementedWorkflowServiceServer
	db            *database.DB
	activeStreams map[string]pb.WorkflowService_WorkflowStreamServer // stream ID -> incoming worker stream
	mu            sync.RWMutex

	// Worker pool for non-blocking message processing
	workerPool *WorkerPool
	shutdownCh chan struct{}
}

type streamContext struct {
	stream     pb.WorkflowService_WorkflowStreamServer
	streamID   string
	responseCh chan *pb.WorkflowStreamResponse
	shutdownCh chan struct{}
}

type streamMessage struct {
	request   *pb.WorkflowStreamRequest
	streamCtx *streamContext
}

type WorkerPool struct {
	workers    int
	queue      chan *streamMessage
	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

func NewWorkerPool(workers int, queueSize int) *WorkerPool {
	return &WorkerPool{
		workers:    workers,
		queue:      make(chan *streamMessage, queueSize),
		shutdownCh: make(chan struct{}),
	}
}

func (wp *WorkerPool) Start(handler func(*streamMessage)) {
	for i := 0; i < wp.workers; i++ {
		wp.wg.Add(1)
		go func() {
			defer wp.wg.Done()
			for {
				select {
				case msg := <-wp.queue:
					handler(msg)
				case <-wp.shutdownCh:
					return
				}
			}
		}()
	}
}

func (wp *WorkerPool) Submit(msg *streamMessage) bool {
	select {
	case wp.queue <- msg:
		return true
	default:
		return false // Queue full
	}
}

func (wp *WorkerPool) Stop() {
	close(wp.shutdownCh)
	wp.wg.Wait()
}

func NewWorkflowServer(db *database.DB) *WorkflowServer {
	ws := &WorkflowServer{
		db:            db,
		activeStreams: make(map[string]pb.WorkflowService_WorkflowStreamServer),
		workerPool:    NewWorkerPool(10, 1000), // 10 workers, 1000 queue size
		shutdownCh:    make(chan struct{}),
	}

	// Start worker pool
	ws.workerPool.Start(ws.processMessage)

	return ws
}

// RegisterEndpoint handles endpoint registration
func (s *WorkflowServer) RegisterEndpoint(ctx context.Context, req *pb.RegisterEndpointRequest) (*pb.RegisterEndpointResponse, error) {
	log.Printf("Registering endpoint: %s -> %s", req.Name, req.Endpoint)

	// Check if endpoint already exists
	existingEndpoint, err := s.db.GetEndpointByName(req.Name)
	if err == nil && existingEndpoint != nil {
		return &pb.RegisterEndpointResponse{
			Success:    false,
			Message:    "Endpoint with this name already exists",
			EndpointId: existingEndpoint.ID,
		}, nil
	}

	endpoint, err := s.db.CreateEndpoint(req.Name, req.Endpoint)
	if err != nil {
		log.Printf("Error creating endpoint: %v", err)
		return &pb.RegisterEndpointResponse{
			Success: false,
			Message: "Failed to create endpoint",
		}, status.Errorf(codes.Internal, "failed to create endpoint: %v", err)
	}

	return &pb.RegisterEndpointResponse{
		Success:    true,
		Message:    "Endpoint registered successfully",
		EndpointId: endpoint.ID,
	}, nil
}

// WorkflowStream handles bidirectional streaming from workers (after StartWorkflow trigger)
func (s *WorkflowServer) WorkflowStream(stream pb.WorkflowService_WorkflowStreamServer) error {
	log.Println("Worker established bidirectional stream for workflow execution")

	// Generate a unique stream ID for this session
	streamID := fmt.Sprintf("stream_%d", time.Now().UnixNano())

	// Create stream context with response channel
	streamCtx := &streamContext{
		stream:     stream,
		streamID:   streamID,
		responseCh: make(chan *pb.WorkflowStreamResponse, 100), // Buffered channel for responses
		shutdownCh: make(chan struct{}),
	}

	// Store the stream
	s.mu.Lock()
	s.activeStreams[streamID] = stream
	s.mu.Unlock()

	// Clean up when stream ends
	defer func() {
		close(streamCtx.shutdownCh)
		close(streamCtx.responseCh)
		s.mu.Lock()
		delete(s.activeStreams, streamID)
		s.mu.Unlock()
		log.Printf("Workflow stream %s closed", streamID)
	}()

	// Start dedicated response sender goroutine for this stream
	go s.responseSender(streamCtx)

	// Non-blocking message processing loop
	for {
		req, err := stream.Recv()
		if err == io.EOF {
			log.Printf("Worker closed the workflow stream %s", streamID)
			return nil
		}
		if err != nil {
			log.Printf("Error receiving from workflow stream %s: %v", streamID, err)
			return err
		}

		// Submit message to worker pool for non-blocking processing
		msg := &streamMessage{
			request:   req,
			streamCtx: streamCtx,
		}

		// Try to submit to worker pool, handle backpressure
		if !s.workerPool.Submit(msg) {
			log.Printf("Worker pool queue full, dropping message for stream %s", streamID)
			// Send error response through response channel
			s.sendAsyncErrorResponse(streamCtx, "Server busy, please retry")
		}
	}
}

// processMessage handles messages in worker goroutines (non-blocking)
func (s *WorkflowServer) processMessage(msg *streamMessage) {
	if err := s.handleStreamMessageAsync(msg.streamCtx, msg.request); err != nil {
		log.Printf("Error handling stream message for %s: %v", msg.streamCtx.streamID, err)
		// Send error response through response channel
		s.sendAsyncErrorResponse(msg.streamCtx, "Internal server error")
	}
}

// responseSender handles all responses for a specific stream in a single goroutine
func (s *WorkflowServer) responseSender(streamCtx *streamContext) {
	for {
		select {
		case response := <-streamCtx.responseCh:
			if response == nil {
				return // Channel closed
			}
			if err := streamCtx.stream.Send(response); err != nil {
				log.Printf("Failed to send response on stream %s: %v", streamCtx.streamID, err)
				return
			}
		case <-streamCtx.shutdownCh:
			return
		}
	}
}

// sendAsyncErrorResponse sends error response through response channel
func (s *WorkflowServer) sendAsyncErrorResponse(streamCtx *streamContext, message string) {
	response := &pb.WorkflowStreamResponse{
		MessageType: &pb.WorkflowStreamResponse_StateResponse{
			StateResponse: &pb.StateUpdateResponse{
				Success: false,
				Message: message,
			},
		},
	}

	select {
	case streamCtx.responseCh <- response:
		// Response queued successfully
	case <-streamCtx.shutdownCh:
		// Stream is shutting down, ignore
	default:
		// Response channel full, log and drop
		log.Printf("Response channel full for stream %s, dropping error response", streamCtx.streamID)
	}
}

// handleStreamMessageAsync processes messages asynchronously and sends responses through channel
func (s *WorkflowServer) handleStreamMessageAsync(streamCtx *streamContext, req *pb.WorkflowStreamRequest) error {
	switch msg := req.MessageType.(type) {
	case *pb.WorkflowStreamRequest_StateUpdate:
		return s.handleStateUpdateAsync(streamCtx, msg.StateUpdate)
	case *pb.WorkflowStreamRequest_WorkflowComplete:
		return s.handleWorkflowCompleteAsync(streamCtx, msg.WorkflowComplete)
	default:
		log.Printf("Unknown message type received")
		s.sendAsyncErrorResponse(streamCtx, "Unknown message type")
		return status.Errorf(codes.InvalidArgument, "unknown message type")
	}
}

// handleStateUpdateAsync processes state updates and sends responses through channel
func (s *WorkflowServer) handleStateUpdateAsync(streamCtx *streamContext, req *pb.StateUpdateRequest) error {
	log.Printf("State update for workflow %d: %s -> %s", req.WorkflowId, req.StateName, req.Status)

	// Create or update state in database
	if req.Status == models.StateStatusPending {
		// Create new state
		_, err := s.db.CreateState(req.WorkflowId, req.StateName, req.StateType, req.Status)
		if err != nil {
			log.Printf("Error creating state: %v", err)
			s.sendAsyncStateResponse(streamCtx, false, "Failed to create state")
			return err
		}
	} else {
		// Update existing state
		err := s.db.UpdateStateStatus(req.WorkflowId, req.StateName, req.Status)
		if err != nil {
			log.Printf("Error updating state: %v", err)
			s.sendAsyncStateResponse(streamCtx, false, "Failed to update state")
			return err
		}
	}

	// Update workflow status if needed
	if req.Status == models.StateStatusRunning {
		err := s.db.UpdateWorkflowStatus(req.WorkflowId, models.WorkflowStatusRunning)
		if err != nil {
			log.Printf("Error updating workflow status: %v", err)
		}
	}

	s.sendAsyncStateResponse(streamCtx, true, "State updated successfully")
	return nil
}

// handleWorkflowCompleteAsync processes workflow completion and sends responses through channel
func (s *WorkflowServer) handleWorkflowCompleteAsync(streamCtx *streamContext, req *pb.WorkflowCompleteRequest) error {
	log.Printf("Workflow %d completed with status: %s", req.WorkflowId, req.Status)

	// Update workflow status
	err := s.db.UpdateWorkflowStatus(req.WorkflowId, req.Status)
	if err != nil {
		log.Printf("Error updating workflow status: %v", err)
		s.sendAsyncCompleteResponse(streamCtx, false, "Failed to update workflow status")
		return err
	}

	// Save final variables if provided
	if req.Variables != "" {
		var variables models.JSONB
		if err := json.Unmarshal([]byte(req.Variables), &variables); err == nil {
			err = s.db.CreateOrUpdateVariables(req.WorkflowId, "final", variables)
			if err != nil {
				log.Printf("Error saving variables: %v", err)
			}
		}
	}

	s.sendAsyncCompleteResponse(streamCtx, true, "Workflow completed successfully")
	return nil
}

// sendAsyncStateResponse sends state response through response channel
func (s *WorkflowServer) sendAsyncStateResponse(streamCtx *streamContext, success bool, message string) {
	response := &pb.WorkflowStreamResponse{
		MessageType: &pb.WorkflowStreamResponse_StateResponse{
			StateResponse: &pb.StateUpdateResponse{
				Success: success,
				Message: message,
			},
		},
	}

	select {
	case streamCtx.responseCh <- response:
		// Response queued successfully
	case <-streamCtx.shutdownCh:
		// Stream is shutting down, ignore
	default:
		// Response channel full, log and drop
		log.Printf("Response channel full for stream %s, dropping state response", streamCtx.streamID)
	}
}

// sendAsyncCompleteResponse sends complete response through response channel
func (s *WorkflowServer) sendAsyncCompleteResponse(streamCtx *streamContext, success bool, message string) {
	response := &pb.WorkflowStreamResponse{
		MessageType: &pb.WorkflowStreamResponse_CompleteResponse{
			CompleteResponse: &pb.WorkflowCompleteResponse{
				Success: success,
				Message: message,
			},
		},
	}

	select {
	case streamCtx.responseCh <- response:
		// Response queued successfully
	case <-streamCtx.shutdownCh:
		// Stream is shutting down, ignore
	default:
		// Response channel full, log and drop
		log.Printf("Response channel full for stream %s, dropping complete response", streamCtx.streamID)
	}
}

// SendWorkflowExecution sends workflow execution request through existing worker stream
func (s *WorkflowServer) SendWorkflowExecution(endpointName string, workflowID int64, workflowName, requestID, payload string) error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	log.Printf("Looking for active streams, total streams: %d", len(s.activeStreams))

	// Find the active stream for this endpoint
	var targetStream pb.WorkflowService_WorkflowStreamServer
	for streamID, stream := range s.activeStreams {
		// For now, use the first available stream
		// TODO: implement proper endpoint-to-stream mapping
		log.Printf("Found active stream: %s", streamID)
		targetStream = stream
		break
	}

	if targetStream == nil {
		log.Printf("No active streams found for endpoint %s", endpointName)
		return status.Errorf(codes.NotFound, "no active stream found for endpoint %s", endpointName)
	}

	log.Printf("Sending workflow execution through existing stream to %s", endpointName)

	// Send workflow execution request through the stream
	// We use the execution response to carry the workflow execution details
	response := &pb.WorkflowStreamResponse{
		MessageType: &pb.WorkflowStreamResponse_ExecutionResponse{
			ExecutionResponse: &pb.WorkflowExecutionResponse{
				Success:    true,
				Message:    fmt.Sprintf("EXECUTE:%s:%s:%s", workflowName, requestID, payload),
				WorkflowId: workflowID,
			},
		},
	}

	err := targetStream.Send(response)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to send workflow execution through stream: %v", err)
	}

	log.Printf("Workflow execution request sent through stream successfully")
	return nil
}

// GetActiveStreams returns the list of active stream IDs
func (s *WorkflowServer) GetActiveStreams() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	streamIDs := make([]string, 0, len(s.activeStreams))
	for streamID := range s.activeStreams {
		streamIDs = append(streamIDs, streamID)
	}
	return streamIDs
}

// Shutdown gracefully shuts down the workflow server
func (s *WorkflowServer) Shutdown() {
	close(s.shutdownCh)
	s.workerPool.Stop()
}
