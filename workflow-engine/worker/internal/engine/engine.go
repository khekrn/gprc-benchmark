// Package engine provides workflow execution capabilities for the worker.
// It handles workflow definition, registration, and execution with support
// for state updates and completion callbacks.
package engine

import (
	"fmt"
	"log"

	"workflow-worker/internal/models"
)

// StateUpdateCallback is a function type for sending state updates
type StateUpdateCallback func(workflowID int64, stateName, stateType, status string, data map[string]interface{}) error

// WorkflowCompleteCallback is a function type for sending workflow completion notifications
type WorkflowCompleteCallback func(workflowID int64, status string, variables map[string]interface{}) error

// WorkflowEngine handles workflow execution
type WorkflowEngine struct {
	workflows                map[string]*models.WorkflowDefinition
	stateUpdateCallback      StateUpdateCallback
	workflowCompleteCallback WorkflowCompleteCallback
}

// NewWorkflowEngine creates a new workflow engine
func NewWorkflowEngine() *WorkflowEngine {
	engine := &WorkflowEngine{
		workflows: make(map[string]*models.WorkflowDefinition),
	}

	// Register the loan approval workflow
	engine.RegisterLoanApprovalWorkflow()

	return engine
}

// SetStateUpdateCallback sets the callback for sending state updates
func (e *WorkflowEngine) SetStateUpdateCallback(callback StateUpdateCallback) {
	e.stateUpdateCallback = callback
}

// SetWorkflowCompleteCallback sets the callback for sending workflow completion notifications
func (e *WorkflowEngine) SetWorkflowCompleteCallback(callback WorkflowCompleteCallback) {
	e.workflowCompleteCallback = callback
}

// RegisterWorkflow registers a workflow definition
func (e *WorkflowEngine) RegisterWorkflow(workflow *models.WorkflowDefinition) {
	e.workflows[workflow.Name] = workflow
	log.Printf("Registered workflow: %s", workflow.Name)
}

// ExecuteWorkflow executes a workflow by name
func (e *WorkflowEngine) ExecuteWorkflow(workflowName, requestID string, workflowID int64, payload map[string]interface{}) error {
	workflow, exists := e.workflows[workflowName]
	if !exists {
		return fmt.Errorf("workflow %s not found", workflowName)
	}

	ctx := &models.WorkflowContext{
		WorkflowID:   workflowID,
		RequestID:    requestID,
		WorkflowName: workflowName,
		Variables:    payload,
		CurrentStep:  workflow.StartStep,
	}

	log.Printf("Starting workflow execution: %s (ID: %d)", workflowName, workflowID)

	return e.executeWorkflowSteps(workflow, ctx)
}

// executeWorkflowSteps executes the workflow steps
func (e *WorkflowEngine) executeWorkflowSteps(workflow *models.WorkflowDefinition, ctx *models.WorkflowContext) error {
	currentStepName := ctx.CurrentStep

	for currentStepName != "" {
		step, exists := workflow.Steps[currentStepName]
		if !exists {
			return fmt.Errorf("step %s not found in workflow %s", currentStepName, workflow.Name)
		}

		log.Printf("Executing step: %s (%s)", step.Name, step.Type)

		// Send pending state update
		if e.stateUpdateCallback != nil {
			err := e.stateUpdateCallback(ctx.WorkflowID, step.Name, step.Type, models.StatusPending, ctx.Variables)
			if err != nil {
				log.Printf("Error sending pending state update: %v", err)
			}
		}

		// Execute the step
		success, err := step.Handler(ctx)
		if err != nil {
			log.Printf("Error executing step %s: %v", step.Name, err)

			// Send failed state update
			if e.stateUpdateCallback != nil {
				e.stateUpdateCallback(ctx.WorkflowID, step.Name, step.Type, models.StatusFailed, ctx.Variables)
			}

			// Complete workflow with failure
			if e.workflowCompleteCallback != nil {
				e.workflowCompleteCallback(ctx.WorkflowID, models.StatusFailed, ctx.Variables)
			}
			return err
		}

		// Send success state update
		if e.stateUpdateCallback != nil {
			err = e.stateUpdateCallback(ctx.WorkflowID, step.Name, step.Type, models.StatusSuccess, ctx.Variables)
			if err != nil {
				log.Printf("Error sending success state update: %v", err)
			}
		}

		// Determine next step
		currentStepName = e.getNextStep(step, success)
		ctx.CurrentStep = currentStepName

		log.Printf("Step %s completed successfully. Next step: %s", step.Name, currentStepName)
	}

	// Workflow completed successfully
	log.Printf("Workflow %s completed successfully", ctx.WorkflowName)
	if e.workflowCompleteCallback != nil {
		return e.workflowCompleteCallback(ctx.WorkflowID, models.StatusSuccess, ctx.Variables)
	}
	return nil
}

// getNextStep determines the next step based on current step and execution result
func (e *WorkflowEngine) getNextStep(step *models.WorkflowStep, success bool) string {
	if step.Type == models.StepTypeCondition {
		if success {
			return step.NextOnTrue
		}
		return step.NextOnFalse
	}
	return step.Next
}

// GetRegisteredWorkflows returns the list of registered workflow names
func (e *WorkflowEngine) GetRegisteredWorkflows() []string {
	workflows := make([]string, 0, len(e.workflows))
	for name := range e.workflows {
		workflows = append(workflows, name)
	}
	return workflows
}

// RegisterLoanApprovalWorkflow registers the loan approval workflow
func (e *WorkflowEngine) RegisterLoanApprovalWorkflow() {
	workflow := &models.WorkflowDefinition{
		Name:      "loan_approval",
		StartStep: "PostLoanApplication",
		Steps: map[string]*models.WorkflowStep{
			"PostLoanApplication": {
				Name:    "PostLoanApplication",
				Type:    models.StepTypeTask,
				Handler: e.postLoanApplication,
				Next:    "PostLoanApplicationCond",
			},
			"PostLoanApplicationCond": {
				Name:        "PostLoanApplicationCond",
				Type:        models.StepTypeCondition,
				Handler:     e.postLoanApplicationCondition,
				NextOnTrue:  "PanVerification",
				NextOnFalse: "SendCallback",
			},
			"PanVerification": {
				Name:    "PanVerification",
				Type:    models.StepTypeTask,
				Handler: e.panVerification,
				Next:    "PanVerificationCond",
			},
			"PanVerificationCond": {
				Name:        "PanVerificationCond",
				Type:        models.StepTypeCondition,
				Handler:     e.panVerificationCondition,
				NextOnTrue:  "AadhaarVerification",
				NextOnFalse: "SendCallback",
			},
			"AadhaarVerification": {
				Name:    "AadhaarVerification",
				Type:    models.StepTypeTask,
				Handler: e.aadhaarVerification,
				Next:    "AadhaarVerificationCond",
			},
			"AadhaarVerificationCond": {
				Name:        "AadhaarVerificationCond",
				Type:        models.StepTypeCondition,
				Handler:     e.aadhaarVerificationCondition,
				NextOnTrue:  "BureauPull",
				NextOnFalse: "SendCallback",
			},
			"BureauPull": {
				Name:    "BureauPull",
				Type:    models.StepTypeTask,
				Handler: e.bureauPull,
				Next:    "BureauPullCond",
			},
			"BureauPullCond": {
				Name:        "BureauPullCond",
				Type:        models.StepTypeCondition,
				Handler:     e.bureauPullCondition,
				NextOnTrue:  "FinalDecision",
				NextOnFalse: "SendCallback",
			},
			"FinalDecision": {
				Name:    "FinalDecision",
				Type:    models.StepTypeTask,
				Handler: e.finalDecision,
				Next:    "FinalDecisionCond",
			},
			"FinalDecisionCond": {
				Name:        "FinalDecisionCond",
				Type:        models.StepTypeCondition,
				Handler:     e.finalDecisionCondition,
				NextOnTrue:  "UpdateStatus",
				NextOnFalse: "SendCallback",
			},
			"UpdateStatus": {
				Name:    "UpdateStatus",
				Type:    models.StepTypeTask,
				Handler: e.updateStatus,
				Next:    "UpdateStatusCond",
			},
			"UpdateStatusCond": {
				Name:        "UpdateStatusCond",
				Type:        models.StepTypeCondition,
				Handler:     e.updateStatusCondition,
				NextOnTrue:  "SendCallback",
				NextOnFalse: "SendCallback",
			},
			"SendCallback": {
				Name:    "SendCallback",
				Type:    models.StepTypeTask,
				Handler: e.sendCallback,
				Next:    "", // End of workflow
			},
		},
	}

	e.RegisterWorkflow(workflow)
}
