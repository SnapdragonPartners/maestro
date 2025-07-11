package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/logx"
)

// EscalationHandler manages business question escalations and ESCALATED state
type EscalationHandler struct {
	logsDir         string
	escalationsFile string
	escalations     map[string]*EscalationEntry // escalationID -> EscalationEntry
	queue           *Queue
}

// EscalationEntry represents an escalated business question requiring human intervention
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

// EscalationSummary provides an overview of escalation status
type EscalationSummary struct {
	TotalEscalations      int                `json:"total_escalations"`
	PendingEscalations    int                `json:"pending_escalations"`
	ResolvedEscalations   int                `json:"resolved_escalations"`
	EscalationsByType     map[string]int     `json:"escalations_by_type"`
	EscalationsByPriority map[string]int     `json:"escalations_by_priority"`
	Escalations           []*EscalationEntry `json:"escalations"`
}

// NewEscalationHandler creates a new escalation handler
func NewEscalationHandler(logsDir string, queue *Queue) *EscalationHandler {
	escalationsFile := filepath.Join(logsDir, "escalations.jsonl")

	handler := &EscalationHandler{
		logsDir:         logsDir,
		escalationsFile: escalationsFile,
		escalations:     make(map[string]*EscalationEntry),
		queue:           queue,
	}

	// Load existing escalations
	handler.loadEscalations()

	return handler
}

// EscalateBusinessQuestion escalates a business question to human intervention
func (eh *EscalationHandler) EscalateBusinessQuestion(ctx context.Context, pendingQ *PendingQuestion) error {
	// Create escalation entry
	escalation := &EscalationEntry{
		ID:          fmt.Sprintf("esc_%s_%d", pendingQ.ID, time.Now().Unix()),
		StoryID:     pendingQ.StoryID,
		AgentID:     pendingQ.AgentID,
		Type:        "business_question",
		Question:    pendingQ.Question,
		Context:     pendingQ.Context,
		EscalatedAt: time.Now().UTC(),
		Status:      "pending",
		Priority:    eh.determinePriority(pendingQ.Question, pendingQ.Context),
	}

	// Store escalation
	eh.escalations[escalation.ID] = escalation

	// Log to escalations.jsonl
	if err := eh.logEscalation(escalation); err != nil {
		return fmt.Errorf("failed to log escalation: %w", err)
	}

	// Update story status to await human feedback
	if err := eh.queue.MarkAwaitHumanFeedback(escalation.StoryID); err != nil {
		return fmt.Errorf("failed to mark story %s as awaiting human feedback: %w", escalation.StoryID, err)
	}

	logx.Infof("escalated business question %s for story %s (priority: %s)",
		escalation.ID, escalation.StoryID, escalation.Priority)
	logx.Infof("   Question: %s", truncateString(escalation.Question, 100))

	return nil
}

// EscalateReviewFailure escalates repeated code review failures to human intervention
func (eh *EscalationHandler) EscalateReviewFailure(ctx context.Context, storyID, agentID string, failureCount int, lastReview string) error {
	// Create escalation entry for review failure
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

	// Store escalation
	eh.escalations[escalation.ID] = escalation

	// Log to escalations.jsonl
	if err := eh.logEscalation(escalation); err != nil {
		return fmt.Errorf("failed to log escalation: %w", err)
	}

	// Update story status to await human feedback
	if err := eh.queue.MarkAwaitHumanFeedback(escalation.StoryID); err != nil {
		return fmt.Errorf("failed to mark story %s as awaiting human feedback: %w", escalation.StoryID, err)
	}

	logx.Infof("escalated review failure %s for story %s after %d failures",
		escalation.ID, escalation.StoryID, failureCount)

	return nil
}

// EscalateSystemError escalates system errors that require human intervention
func (eh *EscalationHandler) EscalateSystemError(ctx context.Context, storyID, agentID, errorMsg string, errorContext map[string]any) error {
	// Create escalation entry for system error
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

	// Store escalation
	eh.escalations[escalation.ID] = escalation

	// Log to escalations.jsonl
	if err := eh.logEscalation(escalation); err != nil {
		return fmt.Errorf("failed to log escalation: %w", err)
	}

	// Update story status to await human feedback
	if err := eh.queue.MarkAwaitHumanFeedback(escalation.StoryID); err != nil {
		return fmt.Errorf("failed to mark story %s as awaiting human feedback: %w", escalation.StoryID, err)
	}

	logx.Errorf("escalated system error %s for story %s (priority: critical)",
		escalation.ID, escalation.StoryID)

	return nil
}

// logEscalation appends an escalation entry to logs/escalations.jsonl
func (eh *EscalationHandler) logEscalation(escalation *EscalationEntry) error {
	// Ensure logs directory exists
	if err := os.MkdirAll(eh.logsDir, 0755); err != nil {
		return fmt.Errorf("failed to create logs directory: %w", err)
	}

	// Convert escalation to JSON
	jsonData, err := json.Marshal(escalation)
	if err != nil {
		return fmt.Errorf("failed to marshal escalation to JSON: %w", err)
	}

	// Append to escalations.jsonl
	file, err := os.OpenFile(eh.escalationsFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open escalations log file: %w", err)
	}
	defer file.Close()

	// Write JSON line with newline
	if _, err := file.WriteString(string(jsonData) + "\n"); err != nil {
		return fmt.Errorf("failed to write escalation to log: %w", err)
	}

	return nil
}

// loadEscalations loads existing escalations from logs/escalations.jsonl
func (eh *EscalationHandler) loadEscalations() error {
	// Check if escalations file exists
	if _, err := os.Stat(eh.escalationsFile); os.IsNotExist(err) {
		// No existing escalations file, start with empty set
		return nil
	}

	// Read the file
	data, err := os.ReadFile(eh.escalationsFile)
	if err != nil {
		return fmt.Errorf("failed to read escalations file: %w", err)
	}

	// Parse JSONL format (one JSON object per line)
	content := string(data)
	if content == "" {
		return nil
	}

	// Split by newlines and parse each line
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		if line := strings.TrimSpace(line); line != "" {
			var escalation EscalationEntry
			if err := json.Unmarshal([]byte(line), &escalation); err != nil {
				logx.Warnf("failed to parse escalation line %d: %v", i+1, err)
				continue
			}
			eh.escalations[escalation.ID] = &escalation
		}
	}

	return nil
}

// determinePriority determines the priority of a business question
func (eh *EscalationHandler) determinePriority(question string, context map[string]any) string {
	questionLower := strings.ToLower(question)

	// Critical priority keywords
	criticalKeywords := []string{
		"critical", "urgent", "emergency", "blocker", "security",
		"data loss", "outage", "production", "customer impact",
	}

	for _, keyword := range criticalKeywords {
		if strings.Contains(questionLower, keyword) {
			return "critical"
		}
	}

	// High priority keywords
	highKeywords := []string{
		"important", "asap", "deadline", "revenue", "compliance",
		"legal", "regulation", "audit", "risk",
	}

	for _, keyword := range highKeywords {
		if strings.Contains(questionLower, keyword) {
			return "high"
		}
	}

	// Medium priority keywords
	mediumKeywords := []string{
		"business", "requirement", "stakeholder", "customer",
		"policy", "strategy", "roadmap", "feature",
	}

	for _, keyword := range mediumKeywords {
		if strings.Contains(questionLower, keyword) {
			return "medium"
		}
	}

	// Default to low priority
	return "low"
}

// GetEscalations returns all escalations, optionally filtered by status
func (eh *EscalationHandler) GetEscalations(status string) []*EscalationEntry {
	var escalations []*EscalationEntry

	for _, escalation := range eh.escalations {
		if status == "" || escalation.Status == status {
			escalations = append(escalations, escalation)
		}
	}

	// Sort by escalated time (newest first)
	for i := 0; i < len(escalations)-1; i++ {
		for j := i + 1; j < len(escalations); j++ {
			if escalations[i].EscalatedAt.Before(escalations[j].EscalatedAt) {
				escalations[i], escalations[j] = escalations[j], escalations[i]
			}
		}
	}

	return escalations
}

// GetEscalationSummary returns a summary of all escalations
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
		// Count by status
		switch escalation.Status {
		case "pending":
			summary.PendingEscalations++
		case "resolved":
			summary.ResolvedEscalations++
		}

		// Count by type
		summary.EscalationsByType[escalation.Type]++

		// Count by priority
		summary.EscalationsByPriority[escalation.Priority]++
	}

	return summary
}

// ResolveEscalation marks an escalation as resolved
func (eh *EscalationHandler) ResolveEscalation(escalationID, resolution, humanOperator string) error {
	escalation, exists := eh.escalations[escalationID]
	if !exists {
		return fmt.Errorf("escalation %s not found", escalationID)
	}

	// Update escalation status
	now := time.Now().UTC()
	escalation.Status = "resolved"
	escalation.Resolution = resolution
	escalation.HumanOperator = humanOperator
	escalation.ResolvedAt = &now

	// Log the resolution
	if err := eh.logEscalation(escalation); err != nil {
		return fmt.Errorf("failed to log escalation resolution: %w", err)
	}

	logx.Infof("resolved escalation %s by %s", escalationID, humanOperator)

	return nil
}

// AcknowledgeEscalation marks an escalation as acknowledged (seen by human)
func (eh *EscalationHandler) AcknowledgeEscalation(escalationID, humanOperator string) error {
	escalation, exists := eh.escalations[escalationID]
	if !exists {
		return fmt.Errorf("escalation %s not found", escalationID)
	}

	// Update escalation status
	escalation.Status = "acknowledged"
	escalation.HumanOperator = humanOperator

	// Log the acknowledgment
	if err := eh.logEscalation(escalation); err != nil {
		return fmt.Errorf("failed to log escalation acknowledgment: %w", err)
	}

	logx.Infof("acknowledged escalation %s by %s", escalationID, humanOperator)

	return nil
}

// LogTimeout logs when an escalation times out (used by the timeout guard)
func (eh *EscalationHandler) LogTimeout(escalatedAt time.Time, duration time.Duration) error {
	// Create a special timeout escalation entry for logging purposes
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

	// Log the timeout event
	if err := eh.logEscalation(timeoutEscalation); err != nil {
		return fmt.Errorf("failed to log escalation timeout: %w", err)
	}

	logx.Warnf("logged escalation timeout: %v duration", duration.Truncate(time.Minute))

	return nil
}
