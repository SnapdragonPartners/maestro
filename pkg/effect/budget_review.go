package effect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// BudgetReviewEffect represents a budget review request when iteration budgets are exceeded
// or when empty responses require architect guidance.
type BudgetReviewEffect struct {
	ExtraPayload map[string]any // Additional payload data (loops, max_loops, etc.)
	Content      string         // The budget review request content
	Reason       string         // Human-readable reason for the budget review
	OriginState  string         // The origin state that exceeded budget (PLANNING, CODING)
	StoryID      string         // Story ID for the review request
	TargetAgent  string         // Target agent (typically "architect")
	Timeout      time.Duration  // Timeout for waiting for response
}

// Execute sends a budget review request and blocks waiting for the architect's response.
func (e *BudgetReviewEffect) Execute(ctx context.Context, runtime Runtime) (any, error) {
	agentID := runtime.GetAgentID()
	approvalID := proto.GenerateApprovalID()

	// Create REQUEST message with budget review payload
	budgetReviewMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, agentID, e.TargetAgent)
	budgetReviewMsg.SetPayload(proto.KeyKind, string(proto.RequestKindApproval))
	budgetReviewMsg.SetPayload("approval_type", proto.ApprovalTypeBudgetReview.String())
	budgetReviewMsg.SetPayload("content", e.Content)
	budgetReviewMsg.SetPayload("reason", e.Reason)
	budgetReviewMsg.SetPayload("approval_id", approvalID)
	budgetReviewMsg.SetPayload("story_id", e.StoryID) // Include story_id for dispatcher validation
	budgetReviewMsg.SetPayload("origin", e.OriginState)

	// Add any extra payload data
	for key, value := range e.ExtraPayload {
		budgetReviewMsg.SetPayload(key, value)
	}

	runtime.Info("📤 Sending budget review request %s to %s (origin: %s)", approvalID, e.TargetAgent, e.OriginState)

	// Send the budget review request
	if err := runtime.SendMessage(budgetReviewMsg); err != nil {
		return nil, fmt.Errorf("failed to send budget review request: %w", err)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// Block waiting for RESPONSE message
	runtime.Info("⏳ Waiting for budget review response (timeout: %v)", e.Timeout)

	responseMsg, err := runtime.ReceiveMessage(timeoutCtx, proto.MsgTypeRESPONSE)
	if err != nil {
		return nil, fmt.Errorf("failed to receive budget review response: %w", err)
	}

	// Extract budget review result from response payload
	statusRaw, statusExists := responseMsg.GetPayload("status")
	feedbackRaw, _ := responseMsg.GetPayload("feedback")
	approvalIDRaw, _ := responseMsg.GetPayload("approval_id")

	if !statusExists {
		return nil, fmt.Errorf("budget review response missing status field")
	}

	status, ok := statusRaw.(string)
	if !ok {
		return nil, fmt.Errorf("budget review status is not a string: %T", statusRaw)
	}

	// Parse status string to ApprovalStatus enum
	approvalStatus, err := proto.ParseApprovalStatus(status)
	if err != nil {
		return nil, fmt.Errorf("invalid budget review status: %w", err)
	}

	feedbackStr, _ := feedbackRaw.(string)
	approvalIDStr, _ := approvalIDRaw.(string)

	result := &BudgetReviewResult{
		Status:      approvalStatus,
		Feedback:    feedbackStr,
		ApprovalID:  approvalIDStr,
		OriginState: e.OriginState,
	}

	runtime.Info("📥 Received budget review response: %s", result.Status)
	return result, nil
}

// Type returns the effect type identifier.
func (e *BudgetReviewEffect) Type() string {
	return "budget_review"
}

// BudgetReviewResult represents the result of a budget review effect.
type BudgetReviewResult struct {
	Status      proto.ApprovalStatus `json:"status"`       // "APPROVED", "REJECTED", "NEEDS_CHANGES"
	Feedback    string               `json:"feedback"`     // Architect's feedback/reasoning
	ApprovalID  string               `json:"approval_id"`  // Original approval request ID
	OriginState string               `json:"origin_state"` // Original state that triggered budget review
}

// NewBudgetReviewEffect creates a budget review effect with default timeout.
func NewBudgetReviewEffect(content, reason, originState string) *BudgetReviewEffect {
	return &BudgetReviewEffect{
		Content:      content,
		Reason:       reason,
		OriginState:  originState,
		StoryID:      "", // Empty by default - should be set by caller
		TargetAgent:  "architect",
		Timeout:      5 * time.Minute, // Default 5 minute timeout
		ExtraPayload: make(map[string]any),
	}
}

// NewLoopBudgetExceededEffect creates a budget review effect for loop budget exceeded scenarios.
func NewLoopBudgetExceededEffect(originState string, iterationCount, maxIterations int) *BudgetReviewEffect {
	content := fmt.Sprintf("Loop budget exceeded in %s state: %d/%d iterations completed. "+
		"Please provide guidance: CONTINUE (same approach), PIVOT (change approach), or ABANDON (stop task).",
		originState, iterationCount, maxIterations)

	reason := "BUDGET_REVIEW: Loop budget exceeded, requesting guidance"

	effect := NewBudgetReviewEffect(content, reason, originState)
	effect.ExtraPayload["loops"] = iterationCount
	effect.ExtraPayload["max_loops"] = maxIterations

	return effect
}

// NewEmptyResponseBudgetReviewEffect creates a budget review effect for empty response escalation.
func NewEmptyResponseBudgetReviewEffect(originState string, consecutiveCount int) *BudgetReviewEffect {
	content := fmt.Sprintf("LLM returned %d consecutive empty responses from %s state. "+
		"The work may be complete but the agent is unable to proceed. How should I proceed?",
		consecutiveCount, originState)

	reason := "BUDGET_REVIEW: Multiple empty LLM responses, requesting guidance"

	effect := NewBudgetReviewEffect(content, reason, originState)
	effect.ExtraPayload["consecutive_empty_responses"] = consecutiveCount

	return effect
}
