package handlers

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"workflow-engine/internal/database"
	"workflow-engine/internal/grpc"
	"workflow-engine/internal/models"
)

type HTTPServer struct {
	db         *database.DB
	grpcServer *grpc.WorkflowServer
}

type StartWorkflowRequest struct {
	WorkflowName string                 `json:"workflow_name" binding:"required"`
	Payload      map[string]interface{} `json:"payload"`
}

type StartWorkflowResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	WorkflowID int64  `json:"workflow_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

func NewHTTPServer(db *database.DB, grpcServer *grpc.WorkflowServer) *HTTPServer {
	return &HTTPServer{
		db:         db,
		grpcServer: grpcServer,
	}
}

// StartWorkflow handles the REST API endpoint to start a workflow
func (h *HTTPServer) StartWorkflow(c *gin.Context) {
	var req StartWorkflowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, StartWorkflowResponse{
			Success: false,
			Message: "Invalid request format",
		})
		return
	}

	// Check if endpoint exists for this workflow
	_, err := h.db.GetEndpointByName(req.WorkflowName)
	if err != nil {
		c.JSON(http.StatusNotFound, StartWorkflowResponse{
			Success: false,
			Message: "Workflow not found",
		})
		return
	}

	// Generate request ID
	requestID := uuid.New().String()

	// Create workflow entry in database
	workflow, err := h.db.CreateWorkflow(req.WorkflowName, requestID, "loan_approval")
	if err != nil {
		c.JSON(http.StatusInternalServerError, StartWorkflowResponse{
			Success: false,
			Message: "Failed to create workflow",
		})
		return
	}

	// Convert payload to JSON string
	payloadBytes, _ := json.Marshal(req.Payload)
	payloadStr := string(payloadBytes)

	// In the new architecture, we don't check for active streams
	// Instead, we directly connect to the worker when the workflow starts

	// Send workflow execution request to the specific endpoint
	// The gRPC server will establish the connection on-demand
	err = h.grpcServer.SendWorkflowExecution(req.WorkflowName, workflow.ID, req.WorkflowName, requestID, payloadStr)
	if err != nil {
		h.db.UpdateWorkflowStatus(workflow.ID, models.WorkflowStatusFailed)
		c.JSON(http.StatusInternalServerError, StartWorkflowResponse{
			Success: false,
			Message: "Failed to send workflow execution request",
		})
		return
	}

	c.JSON(http.StatusOK, StartWorkflowResponse{
		Success:    true,
		Message:    "Workflow started successfully",
		WorkflowID: workflow.ID,
		RequestID:  requestID,
	})
}

// GetWorkflowStatus returns the status of a workflow
func (h *HTTPServer) GetWorkflowStatus(c *gin.Context) {
	workflowIDStr := c.Param("id")
	workflowID, err := strconv.ParseInt(workflowIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "Invalid workflow ID",
		})
		return
	}

	workflow, err := h.db.GetWorkflowByID(workflowID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"message": "Workflow not found",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"workflow": workflow,
	})
}

// HealthCheck returns the health status of the server
func (h *HTTPServer) HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"version":   "1.0.0",
	})
}

// GetActiveConnections returns the number of active gRPC connections
func (h *HTTPServer) GetActiveConnections(c *gin.Context) {
	streams := h.grpcServer.GetActiveStreams()
	c.JSON(http.StatusOK, gin.H{
		"active_connections": len(streams),
		"stream_ids":         streams,
	})
}
