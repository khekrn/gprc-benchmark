package models

// WorkflowContext holds the state and variables for a workflow execution
type WorkflowContext struct {
	WorkflowID   int64                  `json:"workflow_id"`
	RequestID    string                 `json:"request_id"`
	WorkflowName string                 `json:"workflow_name"`
	Variables    map[string]interface{} `json:"variables"`
	CurrentStep  string                 `json:"current_step"`
}

// WorkflowStep represents a single step in the workflow
type WorkflowStep struct {
	Name        string              `json:"name"`
	Type        string              `json:"type"` // "task" or "condition"
	Handler     WorkflowStepHandler `json:"-"`
	NextOnTrue  string              `json:"next_on_true,omitempty"`
	NextOnFalse string              `json:"next_on_false,omitempty"`
	Next        string              `json:"next,omitempty"`
}

// WorkflowStepHandler is the function signature for workflow step handlers
type WorkflowStepHandler func(ctx *WorkflowContext) (bool, error)

// WorkflowDefinition represents a complete workflow
type WorkflowDefinition struct {
	Name         string                   `json:"name"`
	Steps        map[string]*WorkflowStep `json:"steps"`
	StartStep    string                   `json:"start_step"`
	SuccessSteps []string                 `json:"success_steps"`
	FailureSteps []string                 `json:"failure_steps"`
}

// LoanApplicationData represents the loan application payload
type LoanApplicationData struct {
	ApplicationID string `json:"application_id"`
	Amount        int64  `json:"amount"`
	Applicant     struct {
		Name    string `json:"name"`
		PAN     string `json:"pan"`
		Aadhaar string `json:"aadhaar"`
		Email   string `json:"email"`
		Phone   string `json:"phone"`
	} `json:"applicant"`
	Purpose string `json:"purpose"`
}

// WorkflowStatus constants
const (
	StatusPending = "p"
	StatusSuccess = "s"
	StatusFailed  = "f"
	StatusRunning = "r"
)

// StepType constants
const (
	StepTypeTask      = "task"
	StepTypeCondition = "condition"
)
