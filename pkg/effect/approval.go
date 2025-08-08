package effect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// AwaitApprovalEffect represents an async approval request effect.
type AwaitApprovalEffect struct {
	ApprovalType   proto.ApprovalType
	RequestPayload map[string]any
	TargetAgent    string
	Timeout        time.Duration
}

// Execute sends an approval request and blocks waiting for the response.
func (e *AwaitApprovalEffect) Execute(ctx context.Context, runtime Runtime) (any, error) {
	agentID := runtime.GetAgentID()

	// Create REQUEST message with approval payload
	requestMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, agentID, e.TargetAgent)
	requestMsg.SetPayload(proto.KeyKind, string(proto.RequestKindApproval))

	// Set approval type as flat field (architect expects this)
	requestMsg.SetPayload("approval_type", string(e.ApprovalType))

	// Add all request payload fields as flat fields
	for key, value := range e.RequestPayload {
		requestMsg.SetPayload(key, value)
	}

	runtime.Info("📤 Sending %s approval request to %s", e.ApprovalType, e.TargetAgent)

	// Send the request
	if err := runtime.SendMessage(requestMsg); err != nil {
		return nil, fmt.Errorf("failed to send approval request: %w", err)
	}

	// Create timeout context
	timeoutCtx := ctx
	if e.Timeout > 0 {
		var cancel context.CancelFunc
		timeoutCtx, cancel = context.WithTimeout(ctx, e.Timeout)
		defer cancel()
	}

	runtime.Debug("⏳ Blocking waiting for RESULT message from %s", e.TargetAgent)

	// Block waiting for RESPONSE message using the runtime's receive method
	resultMsg, err := runtime.ReceiveMessage(timeoutCtx, proto.MsgTypeRESPONSE)
	if err != nil {
		return nil, fmt.Errorf("failed to receive approval result: %w", err)
	}

	// Parse the result message into ApprovalResult
	// The architect sends approval data as an "approval_result" object
	approvalResultRaw, exists := resultMsg.GetPayload("approval_result")
	if !exists {
		return nil, fmt.Errorf("missing approval_result in result message")
	}

	// Convert to ApprovalResult struct
	approvalResult, ok := approvalResultRaw.(*proto.ApprovalResult)
	if !ok {
		return nil, fmt.Errorf("invalid approval_result format in result message")
	}

	result := &ApprovalResult{
		Status:   approvalResult.Status,
		Feedback: approvalResult.Feedback,
		Data:     resultMsg.Payload,
	}

	runtime.Info("✅ Received %s approval result: %s", e.ApprovalType, approvalResult.Status)
	return result, nil
}

// Type returns the effect type identifier.
func (e *AwaitApprovalEffect) Type() string {
	return "await_approval"
}

// ApprovalResult represents the result of an approval request.
type ApprovalResult struct {
	Status   proto.ApprovalStatus `json:"status"`
	Data     map[string]any       `json:"data,omitempty"`
	Feedback string               `json:"feedback,omitempty"`
}

// NewPlanApprovalEffect creates an effect for plan approval requests.
func NewPlanApprovalEffect(planContent, taskContent string) *AwaitApprovalEffect {
	return &AwaitApprovalEffect{
		ApprovalType: proto.ApprovalTypePlan,
		TargetAgent:  "architect",
		Timeout:      5 * time.Minute, // Configurable timeout
		RequestPayload: map[string]any{
			"plan":    planContent,
			"content": taskContent,
		},
	}
}

// NewCompletionApprovalEffect creates an effect for completion approval requests.
func NewCompletionApprovalEffect(summary, filesCreated string) *AwaitApprovalEffect {
	return &AwaitApprovalEffect{
		ApprovalType: proto.ApprovalTypeCompletion,
		TargetAgent:  "architect",
		Timeout:      5 * time.Minute, // Configurable timeout
		RequestPayload: map[string]any{
			"summary":       summary,
			"files_created": filesCreated,
		},
	}
}

// NewBudgetApprovalEffect creates an effect for budget approval requests.
func NewBudgetApprovalEffect(requestType, details string, cost float64) *AwaitApprovalEffect {
	return &AwaitApprovalEffect{
		ApprovalType: proto.ApprovalTypeBudgetReview,
		TargetAgent:  "architect",
		Timeout:      2 * time.Minute, // Shorter timeout for budget requests
		RequestPayload: map[string]any{
			"request_type": requestType,
			"details":      details,
			"cost":         cost,
		},
	}
}
