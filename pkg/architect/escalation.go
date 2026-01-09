package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/logx"
)

// EscalationHandler manages business question escalations and ESCALATED state.
// Escalations are stored in memory for the current session - they don't need
// file persistence since escalations are session-scoped and the chat system
// provides durable escalation messaging via post_type: 'escalate'.
type EscalationHandler struct {
	escalations map[string]*EscalationEntry // escalationID -> EscalationEntry
	queue       *Queue
}

// EscalationEntry represents an escalated business question requiring human intervention.
type EscalationEntry struct {
	ID            string         `json:"id"`
	StoryID       string         `json:"story_id"`
	AgentID       string         `json:"agent_id"`
	Type          string         `json:"type"` // "business_question", "review_failure", "system_error"
	Question      string         `json:"question,omitempty"`
	Context       map[string]any `json:"context"`
	EscalatedAt   time.Time      `json:"escalated_at"`
	Status        string         `json:"status"`   // "pending", "acknowledged", "resolved"
	Priority      string         `json:"priority"` // "low", "medium", "high", "critical"
	ResolvedAt    *time.Time     `json:"resolved_at,omitempty"`
	Resolution    string         `json:"resolution,omitempty"`
	HumanOperator string         `json:"human_operator,omitempty"`
}

// EscalationSummary provides an overview of escalation status.
//
//nolint:govet // JSON serialization struct, logical order preferred
type EscalationSummary struct {
	TotalEscalations      int                `json:"total_escalations"`
	PendingEscalations    int                `json:"pending_escalations"`
	ResolvedEscalations   int                `json:"resolved_escalations"`
	EscalationsByType     map[string]int     `json:"escalations_by_type"`
	EscalationsByPriority map[string]int     `json:"escalations_by_priority"`
	Escalations           []*EscalationEntry `json:"escalations"`
}

// NewEscalationHandler creates a new escalation handler.
func NewEscalationHandler(queue *Queue) *EscalationHandler {
	return &EscalationHandler{
		escalations: make(map[string]*EscalationEntry),
		queue:       queue,
	}
}

// EscalateReviewFailure escalates repeated code review failures to human intervention.
func (eh *EscalationHandler) EscalateReviewFailure(_ context.Context, storyID, agentID string, failureCount int, lastReview string) error {
	// Create escalation entry for review failure.
	escalation := &EscalationEntry{
		ID:       fmt.Sprintf("esc_review_%s_%d", storyID, time.Now().Unix()),
		StoryID:  storyID,
		AgentID:  agentID,
		Type:     "review_failure",
		Question: fmt.Sprintf("Code review failed %d times for story %s", failureCount, storyID),
		Context: map[string]any{
			"failure_count": failureCount,
			"last_review":   lastReview,
			"reason":        "3_strikes_rule",
		},
		EscalatedAt: time.Now().UTC(),
		Status:      "pending",
		Priority:    "high", // Review failures are high priority
	}

	// Store escalation in memory.
	eh.escalations[escalation.ID] = escalation

	// Update story status to await human feedback.
	if err := eh.queue.UpdateStoryStatus(escalation.StoryID, StatusPending); err != nil {
		return fmt.Errorf("failed to mark story %s as awaiting human feedback: %w", escalation.StoryID, err)
	}

	logx.Infof("escalated review failure %s for story %s after %d failures",
		escalation.ID, escalation.StoryID, failureCount)

	return nil
}

// EscalateSystemError escalates system errors that require human intervention.
func (eh *EscalationHandler) EscalateSystemError(_ context.Context, storyID, agentID, errorMsg string, errorContext map[string]any) error {
	// Create escalation entry for system error.
	escalation := &EscalationEntry{
		ID:          fmt.Sprintf("esc_error_%s_%d", storyID, time.Now().Unix()),
		StoryID:     storyID,
		AgentID:     agentID,
		Type:        "system_error",
		Question:    fmt.Sprintf("System error in story %s: %s", storyID, errorMsg),
		Context:     errorContext,
		EscalatedAt: time.Now().UTC(),
		Status:      "pending",
		Priority:    "critical", // System errors are critical
	}

	// Store escalation in memory.
	eh.escalations[escalation.ID] = escalation

	// Update story status to await human feedback.
	if err := eh.queue.UpdateStoryStatus(escalation.StoryID, StatusPending); err != nil {
		return fmt.Errorf("failed to mark story %s as awaiting human feedback: %w", escalation.StoryID, err)
	}

	defaultLogger := logx.NewLogger("architect")
	defaultLogger.Error("escalated system error %s for story %s (priority: critical)",
		escalation.ID, escalation.StoryID)

	return nil
}

// GetEscalations returns all escalations, optionally filtered by status.
func (eh *EscalationHandler) GetEscalations(status string) []*EscalationEntry {
	var escalations []*EscalationEntry

	for _, escalation := range eh.escalations {
		if status == "" || escalation.Status == status {
			escalations = append(escalations, escalation)
		}
	}

	// Sort by escalated time (newest first).
	for i := 0; i < len(escalations)-1; i++ {
		for j := i + 1; j < len(escalations); j++ {
			if escalations[i].EscalatedAt.Before(escalations[j].EscalatedAt) {
				escalations[i], escalations[j] = escalations[j], escalations[i]
			}
		}
	}

	return escalations
}

// GetEscalationSummary returns a summary of all escalations.
func (eh *EscalationHandler) GetEscalationSummary() *EscalationSummary {
	summary := &EscalationSummary{
		TotalEscalations:      len(eh.escalations),
		PendingEscalations:    0,
		ResolvedEscalations:   0,
		EscalationsByType:     make(map[string]int),
		EscalationsByPriority: make(map[string]int),
		Escalations:           eh.GetEscalations(""), // All escalations
	}

	for _, escalation := range eh.escalations {
		// Count by status.
		switch escalation.Status {
		case string(StatusPending):
			summary.PendingEscalations++
		case "resolved":
			summary.ResolvedEscalations++
		}

		// Count by type.
		summary.EscalationsByType[escalation.Type]++

		// Count by priority.
		summary.EscalationsByPriority[escalation.Priority]++
	}

	return summary
}

// ResolveEscalation marks an escalation as resolved.
func (eh *EscalationHandler) ResolveEscalation(escalationID, resolution, humanOperator string) error {
	escalation, exists := eh.escalations[escalationID]
	if !exists {
		return fmt.Errorf("escalation %s not found", escalationID)
	}

	// Update escalation status.
	now := time.Now().UTC()
	escalation.Status = "resolved"
	escalation.Resolution = resolution
	escalation.HumanOperator = humanOperator
	escalation.ResolvedAt = &now

	logx.Infof("resolved escalation %s by %s", escalationID, humanOperator)

	return nil
}

// AcknowledgeEscalation marks an escalation as acknowledged (seen by human).
func (eh *EscalationHandler) AcknowledgeEscalation(escalationID, humanOperator string) error {
	escalation, exists := eh.escalations[escalationID]
	if !exists {
		return fmt.Errorf("escalation %s not found", escalationID)
	}

	// Update escalation status.
	escalation.Status = "acknowledged"
	escalation.HumanOperator = humanOperator

	logx.Infof("acknowledged escalation %s by %s", escalationID, humanOperator)

	return nil
}

// LogTimeout logs when an escalation times out (used by the timeout guard).
func (eh *EscalationHandler) LogTimeout(escalatedAt time.Time, duration time.Duration) error {
	// Create a special timeout escalation entry for tracking purposes.
	resolvedTime := time.Now().UTC()
	timeoutEscalation := &EscalationEntry{
		ID:       fmt.Sprintf("timeout_%d", time.Now().Unix()),
		Type:     "escalation_timeout",
		Question: fmt.Sprintf("Escalation timeout: No human response after %v", duration.Truncate(time.Minute)),
		Context: map[string]any{
			"escalated_at":  escalatedAt,
			"timeout_after": duration.String(),
			"timeout_limit": EscalationTimeout.String(),
			"event_type":    "timeout",
		},
		EscalatedAt:   escalatedAt,
		Status:        "timeout",
		Priority:      "critical",
		HumanOperator: "system",
		Resolution:    "Automatic timeout after no human intervention",
		ResolvedAt:    &resolvedTime,
	}

	// Store in memory for session tracking.
	eh.escalations[timeoutEscalation.ID] = timeoutEscalation

	logx.Warnf("logged escalation timeout: %v duration", duration.Truncate(time.Minute))

	return nil
}
