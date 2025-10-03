package grpc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "workflow-worker/proto"
)

// WorkflowEngine interface for executing workflows
type WorkflowEngine interface {
	ExecuteWorkflow(workflowName, requestID string, workflowID int64, payload map[string]interface{}) error
}

// Client represents a gRPC client for workflow communication
type Client struct {
	conn           *grpc.ClientConn
	client         pb.WorkflowServiceClient
	stream         pb.WorkflowService_WorkflowStreamClient
	mu             sync.RWMutex
	connected      bool
	reconnectDelay time.Duration
	maxReconnects  int
	ctx            context.Context
	cancel         context.CancelFunc
	workflowEngine WorkflowEngine // Proper interface instead of interface{}
}

// NewClient creates a new gRPC client
func NewClient(serverAddr string) (*Client, error) {
	ctx, cancel := context.WithCancel(context.Background())

	client := &Client{
		reconnectDelay: 5 * time.Second,
		maxReconnects:  10,
		ctx:            ctx,
		cancel:         cancel,
	}

	err := client.connect(serverAddr)
	if err != nil {
		cancel()
		return nil, err
	}

	return client, nil
}

// connect establishes connection to the workflow server
func (c *Client) connect(serverAddr string) error {
	log.Printf("Connecting to workflow server at %s", serverAddr)

	conn, err := grpc.NewClient(serverAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return fmt.Errorf("failed to connect to server: %v", err)
	}

	c.conn = conn
	c.client = pb.NewWorkflowServiceClient(conn)

	log.Printf("Connected to workflow server successfully")
	return nil
}

// RegisterEndpoint registers this worker's endpoint with the server
func (c *Client) RegisterEndpoint(name, endpoint string) (*pb.RegisterEndpointResponse, error) {
	req := &pb.RegisterEndpointRequest{
		Name:     name,
		Endpoint: endpoint,
	}

	resp, err := c.client.RegisterEndpoint(c.ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to register endpoint: %v", err)
	}

	log.Printf("Endpoint registered successfully: %s -> %s", name, endpoint)
	return resp, nil
}

// StartStream starts the bidirectional streaming connection
func (c *Client) StartStream() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.connected {
		log.Printf("Stream already active, reusing existing connection")
		return nil // Already connected
	}

	log.Printf("Creating new bidirectional stream to server...")
	stream, err := c.client.WorkflowStream(c.ctx)
	if err != nil {
		return fmt.Errorf("failed to start stream: %v", err)
	}

	c.stream = stream
	c.connected = true

	log.Printf("New workflow stream established successfully")

	// Start listening for incoming messages
	go c.listen()

	return nil
}

// StartStreamWithRegistration is deprecated - use RegisterEndpoint instead
func (c *Client) StartStreamWithRegistration(workerName, endpoint string) error {
	return fmt.Errorf("StartStreamWithRegistration is deprecated in new architecture")
}

// listen listens for incoming messages from the server
func (c *Client) listen() {
	defer func() {
		c.mu.Lock()
		c.connected = false
		c.mu.Unlock()
	}()

	for {
		select {
		case <-c.ctx.Done():
			log.Printf("Stream context cancelled, stopping listener")
			return
		default:
			resp, err := c.stream.Recv()
			if err == io.EOF {
				log.Printf("Server closed the stream")
				return
			}
			if err != nil {
				log.Printf("Error receiving from stream: %v", err)
				return
			}

			c.handleResponse(resp)
		}
	}
}

// handleResponse handles responses from the server
func (c *Client) handleResponse(resp *pb.WorkflowStreamResponse) {
	switch msg := resp.MessageType.(type) {
	case *pb.WorkflowStreamResponse_ExecutionResponse:
		log.Printf("Received execution response: %+v", msg.ExecutionResponse)

		// Check if this is actually a workflow execution request (server sending work to worker)
		if msg.ExecutionResponse.Success && strings.HasPrefix(msg.ExecutionResponse.Message, "EXECUTE:") {
			log.Printf("Received workflow execution request through stream")
			c.handleWorkflowExecution(msg.ExecutionResponse)
		} else {
			// Handle regular execution response
			log.Printf("Workflow execution started: ID %d", msg.ExecutionResponse.WorkflowId)
		}
	case *pb.WorkflowStreamResponse_StateResponse:
		log.Printf("Received state response: %+v", msg.StateResponse)
	case *pb.WorkflowStreamResponse_CompleteResponse:
		log.Printf("Received complete response: %+v", msg.CompleteResponse)
	default:
		log.Printf("Unknown response type received")
	}
}

// SendStateUpdate sends a state update to the server
func (c *Client) SendStateUpdate(workflowID int64, stateName, stateType, status string, data map[string]interface{}) error {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return fmt.Errorf("client not connected")
	}
	stream := c.stream
	c.mu.RUnlock()

	// Convert data to JSON string
	dataBytes, _ := json.Marshal(data)
	dataStr := string(dataBytes)

	req := &pb.WorkflowStreamRequest{
		MessageType: &pb.WorkflowStreamRequest_StateUpdate{
			StateUpdate: &pb.StateUpdateRequest{
				WorkflowId: workflowID,
				StateName:  stateName,
				StateType:  stateType,
				Status:     status,
				Data:       dataStr,
			},
		},
	}

	err := stream.Send(req)
	if err != nil {
		log.Printf("Error sending state update: %v", err)
		return err
	}

	log.Printf("State update sent: workflow=%d, state=%s, status=%s", workflowID, stateName, status)
	return nil
}

// SendWorkflowComplete sends workflow completion notification to the server
func (c *Client) SendWorkflowComplete(workflowID int64, status string, variables map[string]interface{}) error {
	c.mu.RLock()
	if !c.connected {
		c.mu.RUnlock()
		return fmt.Errorf("client not connected")
	}
	stream := c.stream
	c.mu.RUnlock()

	// Convert variables to JSON string
	variablesBytes, _ := json.Marshal(variables)
	variablesStr := string(variablesBytes)

	req := &pb.WorkflowStreamRequest{
		MessageType: &pb.WorkflowStreamRequest_WorkflowComplete{
			WorkflowComplete: &pb.WorkflowCompleteRequest{
				WorkflowId: workflowID,
				Status:     status,
				Variables:  variablesStr,
			},
		},
	}

	err := stream.Send(req)
	if err != nil {
		log.Printf("Error sending workflow complete: %v", err)
		return err
	}

	log.Printf("Workflow complete sent: workflow=%d, status=%s", workflowID, status)
	return nil
}

// Close closes the gRPC connection
func (c *Client) Close() error {
	c.cancel()

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stream != nil {
		c.stream.CloseSend()
	}

	if c.conn != nil {
		return c.conn.Close()
	}

	c.connected = false
	log.Printf("gRPC client closed")
	return nil
}

// IsConnected returns the connection status
func (c *Client) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.connected
}

// SetWorkflowEngine sets the workflow engine for executing workflows
func (c *Client) SetWorkflowEngine(engine WorkflowEngine) {
	c.workflowEngine = engine
}

// handleWorkflowExecution handles workflow execution requests received through the stream
func (c *Client) handleWorkflowExecution(resp *pb.WorkflowExecutionResponse) {
	// Parse the execution details from the message
	// Format: "EXECUTE:workflowName:requestID:payload"
	parts := strings.Split(resp.Message, ":")
	if len(parts) < 4 {
		log.Printf("Invalid workflow execution message format: %s", resp.Message)
		return
	}

	workflowName := parts[1]
	requestID := parts[2]
	payload := strings.Join(parts[3:], ":") // Join in case payload contains colons
	workflowID := resp.WorkflowId           // Get the workflow ID from the response

	log.Printf("Executing workflow through stream: %s (ID: %s, Workflow ID: %d)", workflowName, requestID, workflowID)

	// Execute the workflow if we have a workflow engine
	if c.workflowEngine != nil {
		log.Printf("Starting workflow execution: %s with payload: %s", workflowName, payload)

		// Parse the JSON payload
		var parsedPayload map[string]interface{}
		err := json.Unmarshal([]byte(payload), &parsedPayload)
		if err != nil {
			log.Printf("Error parsing workflow payload: %v", err)
			return
		}

		// Execute the workflow - pass the workflow ID from the response
		go c.executeWorkflowAsync(workflowName, requestID, workflowID, parsedPayload)

		log.Printf("Workflow execution started asynchronously")
	} else {
		log.Printf("No workflow engine available to execute workflow")
	}
}

// executeWorkflowAsync executes the workflow asynchronously
func (c *Client) executeWorkflowAsync(workflowName, requestID string, workflowID int64, payload map[string]interface{}) {
	if c.workflowEngine != nil {
		log.Printf("Starting workflow execution: %s (Request ID: %s, Workflow ID: %d)", workflowName, requestID, workflowID)

		// Actually execute the workflow using the engine
		err := c.workflowEngine.ExecuteWorkflow(workflowName, requestID, workflowID, payload)
		if err != nil {
			log.Printf("Error executing workflow %s: %v", workflowName, err)
		} else {
			log.Printf("Workflow %s executed successfully", workflowName)
		}
	} else {
		log.Printf("No workflow engine available to execute workflow")
	}
}
