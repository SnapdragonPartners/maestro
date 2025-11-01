package effect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// ApprovalEffect represents an approval request effect that blocks until architect responds.
type ApprovalEffect struct {
	Content      string             // The content to be reviewed (code diff, plan, etc.)
	Reason       string             // Human-readable reason for the approval request
	ApprovalType proto.ApprovalType // Type of approval (CODE, PLAN, BUDGET_REVIEW, COMPLETION) - renamed to avoid method conflict
	StoryID      string             // Story ID for this approval request (required by architect)
	TargetAgent  string             // Target agent (typically "architect")
	Timeout      time.Duration      // Timeout for waiting for response
}

// Execute sends an approval request and blocks waiting for the architect's response.
func (e *ApprovalEffect) Execute(ctx context.Context, runtime Runtime) (any, error) {
	agentID := runtime.GetAgentID()
	approvalID := proto.GenerateApprovalID()

	// Create REQUEST message with typed approval payload
	approvalMsg := proto.NewAgentMsg(proto.MsgTypeREQUEST, agentID, e.TargetAgent)

	// Build approval request payload
	payload := &proto.ApprovalRequestPayload{
		ApprovalType: e.ApprovalType,
		Content:      e.Content,
		Reason:       e.Reason,
	}

	// Add story_id as context if provided
	if e.StoryID != "" {
		payload.Context = "story_id:" + e.StoryID
	}

	// Set typed payload
	approvalMsg.SetTypedPayload(proto.NewApprovalRequestPayload(payload))

	// Store approval_id in metadata for tracking
	approvalMsg.SetMetadata("approval_id", approvalID)
	if e.StoryID != "" {
		approvalMsg.SetMetadata("story_id", e.StoryID)
	}

	runtime.Info("📤 Sending %s approval request %s to %s", e.ApprovalType.String(), approvalID, e.TargetAgent)

	// Send the approval request
	if err := runtime.SendMessage(approvalMsg); err != nil {
		return nil, fmt.Errorf("failed to send approval request: %w", err)
	}

	// Create timeout context
	timeoutCtx, cancel := context.WithTimeout(ctx, e.Timeout)
	defer cancel()

	// Block waiting for RESPONSE message
	runtime.Info("⏳ Waiting for approval response (timeout: %v)", e.Timeout)

	responseMsg, err := runtime.ReceiveMessage(timeoutCtx, proto.MsgTypeRESPONSE)
	if err != nil {
		return nil, fmt.Errorf("failed to receive approval response: %w", err)
	}

	// Extract approval result from response payload
	typedPayload := responseMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("approval response missing typed payload")
	}

	approvalResult, err := typedPayload.ExtractApprovalResponse()
	if err != nil {
		return nil, fmt.Errorf("failed to extract approval response: %w", err)
	}

	result := &ApprovalResult{
		Status:     approvalResult.Status,
		Feedback:   approvalResult.Feedback,
		ApprovalID: approvalResult.ID,
	}

	runtime.Info("📥 Received approval response: %s", result.Status)
	return result, nil
}

// Type returns the effect type identifier.
func (e *ApprovalEffect) Type() string {
	return "approval"
}

// ApprovalResult represents the result of an approval effect.
type ApprovalResult struct {
	Status     proto.ApprovalStatus `json:"status"`      // "APPROVED", "REJECTED", "NEEDS_CHANGES"
	Feedback   string               `json:"feedback"`    // Architect's feedback/reasoning
	ApprovalID string               `json:"approval_id"` // Original approval request ID
}

// NewApprovalEffect creates an approval effect with default timeout.
func NewApprovalEffect(content, reason string, approvalType proto.ApprovalType) *ApprovalEffect {
	return &ApprovalEffect{
		Content:      content,
		Reason:       reason,
		ApprovalType: approvalType,
		StoryID:      "", // Empty by default - should be set by caller
		TargetAgent:  "architect",
		Timeout:      5 * time.Minute, // Default 5 minute timeout
	}
}

// NewApprovalEffectWithTimeout creates an approval effect with custom timeout.
func NewApprovalEffectWithTimeout(content, reason string, approvalType proto.ApprovalType, timeout time.Duration) *ApprovalEffect {
	return &ApprovalEffect{
		Content:      content,
		Reason:       reason,
		ApprovalType: approvalType,
		StoryID:      "", // Empty by default - should be set by caller
		TargetAgent:  "architect",
		Timeout:      timeout,
	}
}

// NewPlanApprovalEffectWithStoryID creates a plan approval effect with story context.
func NewPlanApprovalEffectWithStoryID(planContent, taskContent, storyID string) *ApprovalEffect {
	content := fmt.Sprintf("Plan for Story %s:\n\nTask:\n%s\n\nProposed Plan:\n%s", storyID, taskContent, planContent)
	reason := fmt.Sprintf("Plan requires architect approval before implementation (Story %s)", storyID)
	effect := NewApprovalEffect(content, reason, proto.ApprovalTypePlan)
	effect.StoryID = storyID // Set the story ID for the message payload
	return effect
}

// NewCompletionApprovalEffectWithStoryID creates a completion approval effect with story context.
func NewCompletionApprovalEffectWithStoryID(summary, filesCreated, storyID string) *ApprovalEffect {
	content := fmt.Sprintf("Story %s Completion Summary:\n\nFiles Created: %s\n\nSummary:\n%s", storyID, filesCreated, summary)
	reason := fmt.Sprintf("Story completion requires architect approval (Story %s)", storyID)
	effect := NewApprovalEffect(content, reason, proto.ApprovalTypeCompletion)
	effect.StoryID = storyID // Set the story ID for the message payload
	return effect
}
