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

	// Build approval request payload with budget review type
	payload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeBudgetReview,
		Content:      e.Content,
		Reason:       e.Reason,
		Metadata:     make(map[string]string),
	}

	// Add origin state as context
	if e.OriginState != "" {
		payload.Context = "origin:" + e.OriginState
	}

	// Add story_id to metadata for dispatcher validation
	if e.StoryID != "" {
		payload.Metadata["story_id"] = e.StoryID
	}

	// Add extra payload data to metadata as strings
	for key, value := range e.ExtraPayload {
		payload.Metadata[key] = fmt.Sprintf("%v", value)
	}

	// Set typed payload
	budgetReviewMsg.SetTypedPayload(proto.NewApprovalRequestPayload(payload))

	// Store approval_id in message metadata for tracking
	budgetReviewMsg.SetMetadata("approval_id", approvalID)
	if e.StoryID != "" {
		budgetReviewMsg.SetMetadata("story_id", e.StoryID)
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
	// The architect sends approval response as typed payload
	typedPayload := responseMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("budget review response missing typed payload")
	}

	approvalResult, err := typedPayload.ExtractApprovalResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to extract approval response: %w", err)
	}

	result := &BudgetReviewResult{
		Status:      approvalResult.Status,
		Feedback:    approvalResult.Feedback,
		ApprovalID:  approvalResult.ID,
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
	content := fmt.Sprintf("LLM returned %d consecutive responses with NO TOOL CALLS from %s state. "+
		"In CODING state, the agent should be using MCP tools (shell, done, ask_question) to make progress. "+
		"This suggests either: (1) the work is complete but the agent hasn't used the 'done' tool, or "+
		"(2) the agent is stuck and needs guidance. "+
		"Please provide guidance: CONTINUE (keep working), COMPLETE (mark as done), or REDIRECT (provide specific instructions).",
		consecutiveCount, originState)

	reason := "BUDGET_REVIEW: Multiple responses without tool calls, requesting guidance"

	effect := NewBudgetReviewEffect(content, reason, originState)
	effect.ExtraPayload["consecutive_empty_responses"] = consecutiveCount
	effect.ExtraPayload["issue_type"] = "no_tool_calls"

	return effect
}
