package engine

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"workflow-worker/internal/models"
)

// postLoanApplication handles the loan application submission
func (e *WorkflowEngine) postLoanApplication(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Processing loan application for workflow %d", ctx.WorkflowID)

	// Simulate processing time
	time.Sleep(1 * time.Second)

	// Parse application data
	var appData models.LoanApplicationData
	if data, exists := ctx.Variables["application_data"]; exists {
		if bytes, err := json.Marshal(data); err == nil {
			json.Unmarshal(bytes, &appData)
		}
	}

	// Validate basic application data
	if appData.ApplicationID == "" {
		ctx.Variables["error"] = "Missing application ID"
		return false, fmt.Errorf("missing application ID")
	}

	// Set application processed timestamp
	ctx.Variables["application_processed_at"] = time.Now().Unix()
	ctx.Variables["application_status"] = "submitted"

	log.Printf("Loan application processed successfully for %s", appData.ApplicationID)
	return true, nil
}

// postLoanApplicationCondition checks if loan application was successful
func (e *WorkflowEngine) postLoanApplicationCondition(ctx *models.WorkflowContext) (bool, error) {
	// Check if application was processed successfully
	if status, exists := ctx.Variables["application_status"]; exists {
		return status == "submitted", nil
	}
	return false, nil
}

// panVerification handles PAN verification
func (e *WorkflowEngine) panVerification(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Verifying PAN for workflow %d", ctx.WorkflowID)

	// Simulate PAN verification API call
	time.Sleep(500 * time.Millisecond)

	// Get PAN from variables
	var appData models.LoanApplicationData
	if data, exists := ctx.Variables["application_data"]; exists {
		if bytes, err := json.Marshal(data); err == nil {
			json.Unmarshal(bytes, &appData)
		}
	}

	// Simulate PAN verification (90% success rate)
	success := rand.Float32() < 0.9

	if success {
		ctx.Variables["pan_verified"] = true
		ctx.Variables["pan_verification_score"] = rand.Intn(100) + 1
		log.Printf("PAN verification successful for %s", appData.Applicant.PAN)
	} else {
		ctx.Variables["pan_verified"] = false
		ctx.Variables["pan_error"] = "Invalid PAN number"
		log.Printf("PAN verification failed for %s", appData.Applicant.PAN)
	}

	ctx.Variables["pan_verified_at"] = time.Now().Unix()
	return success, nil
}

// panVerificationCondition checks PAN verification result
func (e *WorkflowEngine) panVerificationCondition(ctx *models.WorkflowContext) (bool, error) {
	if verified, exists := ctx.Variables["pan_verified"]; exists {
		if verified, ok := verified.(bool); ok {
			return verified, nil
		}
	}
	return false, nil
}

// aadhaarVerification handles Aadhaar verification
func (e *WorkflowEngine) aadhaarVerification(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Verifying Aadhaar for workflow %d", ctx.WorkflowID)

	// Simulate Aadhaar verification API call
	time.Sleep(700 * time.Millisecond)

	// Get Aadhaar from variables
	var appData models.LoanApplicationData
	if data, exists := ctx.Variables["application_data"]; exists {
		if bytes, err := json.Marshal(data); err == nil {
			json.Unmarshal(bytes, &appData)
		}
	}

	// Simulate Aadhaar verification (85% success rate)
	success := rand.Float32() < 0.85

	if success {
		ctx.Variables["aadhaar_verified"] = true
		ctx.Variables["aadhaar_verification_score"] = rand.Intn(100) + 1
		log.Printf("Aadhaar verification successful for %s", appData.Applicant.Aadhaar)
	} else {
		ctx.Variables["aadhaar_verified"] = false
		ctx.Variables["aadhaar_error"] = "Invalid Aadhaar number"
		log.Printf("Aadhaar verification failed for %s", appData.Applicant.Aadhaar)
	}

	ctx.Variables["aadhaar_verified_at"] = time.Now().Unix()
	return success, nil
}

// aadhaarVerificationCondition checks Aadhaar verification result
func (e *WorkflowEngine) aadhaarVerificationCondition(ctx *models.WorkflowContext) (bool, error) {
	if verified, exists := ctx.Variables["aadhaar_verified"]; exists {
		if verified, ok := verified.(bool); ok {
			return verified, nil
		}
	}
	return false, nil
}

// bureauPull handles credit bureau data pull
func (e *WorkflowEngine) bureauPull(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Pulling credit bureau data for workflow %d", ctx.WorkflowID)

	// Simulate bureau API call
	time.Sleep(2 * time.Second)

	// Simulate bureau score (300-850 range)
	creditScore := rand.Intn(551) + 300

	ctx.Variables["credit_score"] = creditScore
	ctx.Variables["bureau_pulled_at"] = time.Now().Unix()

	// Generate some mock bureau data
	bureauData := map[string]interface{}{
		"score":           creditScore,
		"payment_history": rand.Float32(),
		"credit_age":      rand.Intn(20) + 1,
		"credit_mix":      rand.Float32(),
		"new_credit":      rand.Intn(5),
	}
	ctx.Variables["bureau_data"] = bureauData

	log.Printf("Credit bureau data pulled successfully. Score: %d", creditScore)
	return true, nil
}

// bureauPullCondition checks if bureau pull was successful
func (e *WorkflowEngine) bureauPullCondition(ctx *models.WorkflowContext) (bool, error) {
	// Check if credit score exists and is reasonable
	if score, exists := ctx.Variables["credit_score"]; exists {
		if creditScore, ok := score.(int); ok {
			return creditScore >= 600, nil // Minimum credit score requirement
		}
	}
	return false, nil
}

// finalDecision makes the final loan approval decision
func (e *WorkflowEngine) finalDecision(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Making final decision for workflow %d", ctx.WorkflowID)

	// Simulate decision making time
	time.Sleep(1 * time.Second)

	// Get all verification results
	panVerified := false
	aadhaarVerified := false
	creditScore := 0

	if pan, exists := ctx.Variables["pan_verified"]; exists {
		if verified, ok := pan.(bool); ok {
			panVerified = verified
		}
	}

	if aadhaar, exists := ctx.Variables["aadhaar_verified"]; exists {
		if verified, ok := aadhaar.(bool); ok {
			aadhaarVerified = verified
		}
	}

	if score, exists := ctx.Variables["credit_score"]; exists {
		if cs, ok := score.(int); ok {
			creditScore = cs
		}
	}

	// Final decision logic
	approved := panVerified && aadhaarVerified && creditScore >= 650

	if approved {
		ctx.Variables["loan_approved"] = true
		ctx.Variables["loan_amount"] = ctx.Variables["amount"]        // Approved amount
		ctx.Variables["interest_rate"] = 8.5 + (rand.Float64() * 3.5) // 8.5% to 12%
		log.Printf("Loan approved for workflow %d", ctx.WorkflowID)
	} else {
		ctx.Variables["loan_approved"] = false
		ctx.Variables["rejection_reason"] = "Credit criteria not met"
		log.Printf("Loan rejected for workflow %d", ctx.WorkflowID)
	}

	ctx.Variables["decision_made_at"] = time.Now().Unix()
	return approved, nil
}

// finalDecisionCondition checks the final decision result
func (e *WorkflowEngine) finalDecisionCondition(ctx *models.WorkflowContext) (bool, error) {
	if approved, exists := ctx.Variables["loan_approved"]; exists {
		if isApproved, ok := approved.(bool); ok {
			return isApproved, nil
		}
	}
	return false, nil
}

// updateStatus updates the loan status
func (e *WorkflowEngine) updateStatus(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Updating loan status for workflow %d", ctx.WorkflowID)

	// Simulate status update API call
	time.Sleep(200 * time.Millisecond)

	if approved, exists := ctx.Variables["loan_approved"]; exists {
		if isApproved, ok := approved.(bool); ok {
			if isApproved {
				ctx.Variables["status"] = "APPROVED"
			} else {
				ctx.Variables["status"] = "REJECTED"
			}
		}
	}

	ctx.Variables["status_updated_at"] = time.Now().Unix()
	log.Printf("Status updated successfully for workflow %d", ctx.WorkflowID)
	return true, nil
}

// updateStatusCondition checks if status update was successful
func (e *WorkflowEngine) updateStatusCondition(ctx *models.WorkflowContext) (bool, error) {
	// Always proceed to callback after status update
	return true, nil
}

// sendCallback sends the final callback
func (e *WorkflowEngine) sendCallback(ctx *models.WorkflowContext) (bool, error) {
	log.Printf("Sending callback for workflow %d", ctx.WorkflowID)

	// Simulate callback API call
	time.Sleep(200 * time.Millisecond)

	// Prepare callback payload
	callbackData := map[string]interface{}{
		"workflow_id":  ctx.WorkflowID,
		"request_id":   ctx.RequestID,
		"status":       ctx.Variables["status"],
		"processed_at": time.Now().Unix(),
	}

	if approved, exists := ctx.Variables["loan_approved"]; exists && approved.(bool) {
		callbackData["loan_amount"] = ctx.Variables["loan_amount"]
		callbackData["interest_rate"] = ctx.Variables["interest_rate"]
	} else {
		callbackData["rejection_reason"] = ctx.Variables["rejection_reason"]
	}

	ctx.Variables["callback_data"] = callbackData
	ctx.Variables["callback_sent_at"] = time.Now().Unix()

	log.Printf("Callback sent successfully for workflow %d", ctx.WorkflowID)
	return true, nil
}
