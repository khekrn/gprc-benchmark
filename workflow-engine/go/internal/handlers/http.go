package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"workflow-engine/internal/database"
	"workflow-engine/internal/models"
	"workflow-engine/internal/wpool"
)

type HTTPHandler struct {
	db                *database.DB
	workerPoolManager *wpool.WorkerPoolManager
}

type StartWorkflowRequest struct {
	WorkflowName string                 `json:"workflow_name" binding:"required"`
	Payload      map[string]interface{} `json:"payload"`
}

type RegisterWorkflowEndpointsRequest struct {
	WorkflowName string   `json:"workflow_name" binding:"required"`
	Endpoints    []string `json:"endpoints" binding:"required"`
}

type RegisterWorkflowEndpointsResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

type StartWorkflowResponse struct {
	Success    bool   `json:"success"`
	Message    string `json:"message"`
	WorkflowID int64  `json:"workflow_id,omitempty"`
	RequestID  string `json:"request_id,omitempty"`
}

type StartWorkflowSyncRequest struct {
	WorkflowName string                 `json:"workflow_name" binding:"required"`
	Payload      map[string]interface{} `json:"payload"`
	TimeoutSec   int                    `json:"timeout_sec,omitempty"` // Optional timeout in seconds, default 30
	Detailed     bool                   `json:"detailed,omitempty"`    // Optional flag for detailed response, default false
}

func NewHTTPHandler(db *database.DB, workerPoolManager *wpool.WorkerPoolManager) *HTTPHandler {
	return &HTTPHandler{
		db:                db,
		workerPoolManager: workerPoolManager,
	}
}

// StartWorkflow handles the REST API endpoint to start a workflow
func (h *HTTPHandler) StartWorkflow(c *gin.Context) {
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

	// In the new architecture, send workflow execution request directly to worker pool manager
	// The worker pool manager will automatically discover and connect to available workers for this workflow
	err = h.workerPoolManager.ExecuteWorkflow(req.WorkflowName, requestID, workflow.ID, payloadStr)
	if err != nil {
		h.db.UpdateWorkflowStatus(workflow.ID, models.WorkflowStatusFailed)
		c.JSON(http.StatusInternalServerError, StartWorkflowResponse{
			Success: false,
			Message: "Failed to send workflow execution request: " + err.Error(),
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

// StartWorkflowSync starts a workflow execution and waits for the result synchronously
func (h *HTTPHandler) StartWorkflowSync(c *gin.Context) {
	var req StartWorkflowSyncRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": err.Error()})
		return
	}

	// Check if endpoint exists for this workflow
	_, err := h.db.GetEndpointByName(req.WorkflowName)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"success": false,
			"error":   "Workflow not found",
		})
		return
	}

	// Set default timeout if not provided
	timeout := 30 * time.Second
	if req.TimeoutSec > 0 {
		timeout = time.Duration(req.TimeoutSec) * time.Second
	}

	// Generate unique request ID
	requestID := uuid.New().String()

	// Convert payload to JSON string
	payloadBytes, err := json.Marshal(req.Payload)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"success": false, "error": "Invalid payload format"})
		return
	}

	// Create workflow record in database
	workflow, err := h.db.CreateWorkflow(req.WorkflowName, requestID, "sync_execution")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"success": false, "error": "Failed to create workflow record"})
		return
	}

	// Execute workflow synchronously through worker pool manager
	result, err := h.workerPoolManager.ExecuteWorkflowSync(req.WorkflowName, requestID, workflow.ID, string(payloadBytes), timeout)
	if err != nil {
		// Update workflow status to failed
		h.db.UpdateWorkflowStatus(workflow.ID, models.WorkflowStatusFailed)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":     false,
			"error":       err.Error(),
			"workflow_id": workflow.ID,
			"request_id":  requestID,
		})
		return
	}

	// Update workflow status in database based on result
	finalStatus := models.WorkflowStatusSuccess
	if !result.Success {
		finalStatus = models.WorkflowStatusFailed
	}
	h.db.UpdateWorkflowStatus(workflow.ID, finalStatus)

	// Return simplified or detailed response based on request
	if req.Detailed {
		// Return the complete workflow result for debugging/auditing
		c.JSON(http.StatusOK, gin.H{
			"success":     true,
			"message":     "Workflow executed successfully",
			"workflow_id": workflow.ID,
			"request_id":  requestID,
			"result":      result,
		})
	} else {
		// Return simplified response with just the essential information
		response := gin.H{
			"success":     true,
			"workflow_id": workflow.ID,
			"request_id":  requestID,
			"status":      result.Status,
		}

		// Add result data if workflow was successful
		if result.Success && result.Result != nil {
			response["result"] = result.Result
		}

		// Add error information if workflow failed
		if !result.Success && result.Error != "" {
			response["error"] = result.Error
		}

		c.JSON(http.StatusOK, response)
	}
}

// GetWorkflowStatus returns the status of a workflow
func (h *HTTPHandler) GetWorkflow(c *gin.Context) {
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
func (h *HTTPHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":    "healthy",
		"timestamp": time.Now().Unix(),
		"version":   "1.0.0",
	})
}

// RegisterWorkflowEndpoints registers endpoints for a specific workflow
func (h *HTTPHandler) RegisterWorkflowEndpoints(c *gin.Context) {
	var req RegisterWorkflowEndpointsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, RegisterWorkflowEndpointsResponse{
			Success: false,
			Message: "Invalid request format",
		})
		return
	}

	// First, create or update endpoint entries in the database
	for _, endpoint := range req.Endpoints {
		// Check if endpoint already exists
		existingEndpoint, err := h.db.GetEndpointByName(req.WorkflowName)
		if err != nil {
			// Create new endpoint entry
			_, err = h.db.CreateEndpoint(req.WorkflowName, endpoint)
			if err != nil {
				c.JSON(http.StatusInternalServerError, RegisterWorkflowEndpointsResponse{
					Success: false,
					Message: fmt.Sprintf("Failed to create endpoint in database: %v", err),
				})
				return
			}
		} else {
			// Endpoint already exists, log it
			fmt.Printf("Endpoint already exists for workflow %s: %s\n", req.WorkflowName, existingEndpoint.Endpoint)
		}
	}

	// Register workflow endpoints in the pool manager
	h.workerPoolManager.RegisterWorkflowEndpoints(req.WorkflowName, req.Endpoints)

	c.JSON(http.StatusOK, RegisterWorkflowEndpointsResponse{
		Success: true,
		Message: fmt.Sprintf("Successfully registered %d endpoints for workflow %s", len(req.Endpoints), req.WorkflowName),
	})
}

// GetWorkflowMappings returns all workflow-to-endpoints mappings
func (h *HTTPHandler) GetWorkflowMappings(c *gin.Context) {
	mappings := h.workerPoolManager.GetAllWorkflowMappings()

	c.JSON(http.StatusOK, gin.H{
		"success":  true,
		"mappings": mappings,
	})
}

// GetActiveConnections returns the number of active gRPC connections
func (h *HTTPHandler) GetConnections(c *gin.Context) {
	// Get statistics from all workflow pools
	poolStats := h.workerPoolManager.GetPoolStats()

	c.JSON(http.StatusOK, gin.H{
		"success":    true,
		"pool_stats": poolStats,
	})
}

// GetMetrics returns comprehensive system metrics for production monitoring
func (h *HTTPHandler) GetMetrics(c *gin.Context) {
	// Get database pool statistics
	dbStats := h.db.GetPoolStats()

	// Get worker pool statistics
	poolStats := h.workerPoolManager.GetPoolStats()

	// Get workflow mappings
	mappings := h.workerPoolManager.GetAllWorkflowMappings()

	metrics := gin.H{
		"timestamp": time.Now().Unix(),
		"service": gin.H{
			"name":    "workflow-engine",
			"version": "2.0.0",
			"uptime":  time.Since(time.Now()).String(), // This would be calculated from service start time
		},
		"database": gin.H{
			"status": "connected",
			"pool":   dbStats,
		},
		"workers": gin.H{
			"total_pools":       len(poolStats),
			"pool_stats":        poolStats,
			"workflow_mappings": mappings,
		},
		"performance": gin.H{
			"high_throughput_enabled": true,
			"connection_pooling":      true,
			"max_db_connections":      30,
			"min_db_connections":      10,
		},
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"metrics": metrics,
	})
}
