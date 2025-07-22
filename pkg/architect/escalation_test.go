package architect

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewEscalationHandler(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	queue := NewQueue(tmpDir + "/stories")
	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	if handler == nil {
		t.Fatal("NewEscalationHandler returned nil")
	}

	if handler.logsDir != tmpDir+"/logs" {
		t.Errorf("Expected logsDir %s, got %s", tmpDir+"/logs", handler.logsDir)
	}

	if len(handler.escalations) != 0 {
		t.Errorf("Expected empty escalations map, got %d entries", len(handler.escalations))
	}
}

func TestEscalateBusinessQuestion(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	// Create test queue with a story.
	queue := NewQueue(tmpDir + "/stories")
	queue.stories["001"] = &QueuedStory{
		ID:     "001",
		Title:  "Test Story",
		Status: StatusInProgress,
	}

	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	// Create a test pending question.
	pendingQ := &PendingQuestion{
		ID:       "test-question-001",
		StoryID:  "001",
		AgentID:  "test-agent",
		Question: "What are the business requirements for authentication?",
		Context:  map[string]any{},
		AskedAt:  time.Now().UTC(),
		Status:   "pending",
	}

	ctx := context.Background()
	err := handler.EscalateBusinessQuestion(ctx, pendingQ)
	if err != nil {
		t.Fatalf("Failed to escalate business question: %v", err)
	}

	// Check that escalation was created.
	if len(handler.escalations) != 1 {
		t.Errorf("Expected 1 escalation, got %d", len(handler.escalations))
	}

	// Check that story status was updated.
	story, exists := queue.GetStory("001")
	if !exists {
		t.Fatal("Story 001 not found")
	}

	if story.Status != StatusAwaitHumanFeedback {
		t.Errorf("Expected story status %s, got %s", StatusAwaitHumanFeedback, story.Status)
	}

	// Check escalation details.
	var escalation *EscalationEntry
	for _, e := range handler.escalations {
		escalation = e
		break
	}

	if escalation.Type != "business_question" {
		t.Errorf("Expected escalation type 'business_question', got %s", escalation.Type)
	}

	if escalation.Status != "pending" {
		t.Errorf("Expected escalation status 'pending', got %s", escalation.Status)
	}

	if escalation.Priority != "medium" {
		t.Errorf("Expected escalation priority 'medium', got %s", escalation.Priority)
	}
}

func TestEscalateReviewFailure(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	// Create test queue with a story.
	queue := NewQueue(tmpDir + "/stories")
	queue.stories["002"] = &QueuedStory{
		ID:     "002",
		Title:  "Test Story 2",
		Status: StatusWaitingReview,
	}

	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	ctx := context.Background()
	err := handler.EscalateReviewFailure(ctx, "002", "test-agent", 3, "Code failed quality checks")
	if err != nil {
		t.Fatalf("Failed to escalate review failure: %v", err)
	}

	// Check that escalation was created.
	if len(handler.escalations) != 1 {
		t.Errorf("Expected 1 escalation, got %d", len(handler.escalations))
	}

	// Check that story status was updated.
	story, exists := queue.GetStory("002")
	if !exists {
		t.Fatal("Story 002 not found")
	}

	if story.Status != StatusAwaitHumanFeedback {
		t.Errorf("Expected story status %s, got %s", StatusAwaitHumanFeedback, story.Status)
	}

	// Check escalation details.
	var escalation *EscalationEntry
	for _, e := range handler.escalations {
		escalation = e
		break
	}

	if escalation.Type != "review_failure" {
		t.Errorf("Expected escalation type 'review_failure', got %s", escalation.Type)
	}

	if escalation.Priority != "high" {
		t.Errorf("Expected escalation priority 'high', got %s", escalation.Priority)
	}

	if !strings.Contains(escalation.Question, "Code review failed 3 times") {
		t.Errorf("Expected escalation question to mention 3 failures, got: %s", escalation.Question)
	}
}

func TestDeterminePriority(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	queue := NewQueue(tmpDir + "/stories")
	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	testCases := []struct {
		question         string
		expectedPriority string
	}{
		{"This is a critical security issue", "critical"},
		{"Important customer requirement", "high"},
		{"What are the business rules?", "medium"},
		{"Simple question about implementation", "low"},
		{"URGENT: Production outage!", "critical"},
		{"Revenue impact analysis needed", "high"},
		{"Basic clarification needed", "low"},
	}

	for _, tc := range testCases {
		priority := handler.determinePriority(tc.question, map[string]any{})
		if priority != tc.expectedPriority {
			t.Errorf("Question: %s - Expected priority %s, got %s",
				tc.question, tc.expectedPriority, priority)
		}
	}
}

func TestGetEscalations(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	queue := NewQueue(tmpDir + "/stories")
	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	// Add test escalations.
	handler.escalations["esc1"] = &EscalationEntry{
		ID:          "esc1",
		Status:      "pending",
		Type:        "business_question",
		EscalatedAt: time.Now().UTC(),
	}

	handler.escalations["esc2"] = &EscalationEntry{
		ID:          "esc2",
		Status:      "acknowledged",
		Type:        "review_failure",
		EscalatedAt: time.Now().UTC().Add(-time.Hour),
	}

	handler.escalations["esc3"] = &EscalationEntry{
		ID:          "esc3",
		Status:      "pending",
		Type:        "system_error",
		EscalatedAt: time.Now().UTC().Add(-2 * time.Hour),
	}

	// Test getting all escalations.
	allEscalations := handler.GetEscalations("")
	if len(allEscalations) != 3 {
		t.Errorf("Expected 3 escalations, got %d", len(allEscalations))
	}

	// Test filtering by status.
	pendingEscalations := handler.GetEscalations("pending")
	if len(pendingEscalations) != 2 {
		t.Errorf("Expected 2 pending escalations, got %d", len(pendingEscalations))
	}

	acknowledgedEscalations := handler.GetEscalations("acknowledged")
	if len(acknowledgedEscalations) != 1 {
		t.Errorf("Expected 1 acknowledged escalation, got %d", len(acknowledgedEscalations))
	}

	// Check sorting (newest first)
	if allEscalations[0].ID != "esc1" {
		t.Errorf("Expected newest escalation first, got %s", allEscalations[0].ID)
	}
}

func TestGetEscalationSummary(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	queue := NewQueue(tmpDir + "/stories")
	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	// Add test escalations.
	handler.escalations["esc1"] = &EscalationEntry{
		ID:       "esc1",
		Status:   "pending",
		Type:     "business_question",
		Priority: "high",
	}

	handler.escalations["esc2"] = &EscalationEntry{
		ID:       "esc2",
		Status:   "resolved",
		Type:     "review_failure",
		Priority: "critical",
	}

	summary := handler.GetEscalationSummary()

	if summary.TotalEscalations != 2 {
		t.Errorf("Expected 2 total escalations, got %d", summary.TotalEscalations)
	}

	if summary.PendingEscalations != 1 {
		t.Errorf("Expected 1 pending escalation, got %d", summary.PendingEscalations)
	}

	if summary.ResolvedEscalations != 1 {
		t.Errorf("Expected 1 resolved escalation, got %d", summary.ResolvedEscalations)
	}

	if summary.EscalationsByType["business_question"] != 1 {
		t.Errorf("Expected 1 business_question escalation, got %d", summary.EscalationsByType["business_question"])
	}

	if summary.EscalationsByPriority["high"] != 1 {
		t.Errorf("Expected 1 high priority escalation, got %d", summary.EscalationsByPriority["high"])
	}
}

func TestLogEscalation(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	queue := NewQueue(tmpDir + "/stories")
	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	escalation := &EscalationEntry{
		ID:          "test-esc-001",
		StoryID:     "001",
		AgentID:     "test-agent",
		Type:        "business_question",
		Question:    "Test question",
		EscalatedAt: time.Now().UTC(),
		Status:      "pending",
		Priority:    "medium",
	}

	// Test logging escalation.
	err := handler.logEscalation(escalation)
	if err != nil {
		t.Fatalf("Failed to log escalation: %v", err)
	}

	// Check that log file was created.
	logFile := filepath.Join(tmpDir, "logs", "escalations.jsonl")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Fatalf("Escalation log file was not created: %s", logFile)
	}

	// Read and verify log content.
	content, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("Failed to read escalation log file: %v", err)
	}

	logContent := string(content)
	if !strings.Contains(logContent, "test-esc-001") {
		t.Errorf("Log content does not contain escalation ID: %s", logContent)
	}

	if !strings.Contains(logContent, "business_question") {
		t.Errorf("Log content does not contain escalation type: %s", logContent)
	}
}

func TestResolveEscalation(t *testing.T) {
	tmpDir := "/tmp/escalation_test"
	defer os.RemoveAll(tmpDir)

	queue := NewQueue(tmpDir + "/stories")
	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	// Add test escalation.
	handler.escalations["esc1"] = &EscalationEntry{
		ID:     "esc1",
		Status: "pending",
	}

	// Resolve escalation.
	err := handler.ResolveEscalation("esc1", "Issue resolved by manual intervention", "human-operator")
	if err != nil {
		t.Fatalf("Failed to resolve escalation: %v", err)
	}

	// Check escalation status.
	escalation := handler.escalations["esc1"]
	if escalation.Status != "resolved" {
		t.Errorf("Expected escalation status 'resolved', got %s", escalation.Status)
	}

	if escalation.Resolution != "Issue resolved by manual intervention" {
		t.Errorf("Expected resolution message, got: %s", escalation.Resolution)
	}

	if escalation.HumanOperator != "human-operator" {
		t.Errorf("Expected human operator 'human-operator', got: %s", escalation.HumanOperator)
	}

	if escalation.ResolvedAt == nil {
		t.Error("Expected ResolvedAt to be set")
	}
}

func TestEscalationIntegration(t *testing.T) {
	tmpDir := "/tmp/escalation_integration_test"
	defer os.RemoveAll(tmpDir)

	// Create stories directory and test story.
	storiesDir := filepath.Join(tmpDir, "stories")
	err := os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create stories directory: %v", err)
	}

	storyContent := `---
id: 001
title: "Integration Test Story"
depends_on: []
est_points: 2
---
Test story for escalation integration.`

	storyPath := filepath.Join(storiesDir, "001.md")
	err = os.WriteFile(storyPath, []byte(storyContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test story: %v", err)
	}

	// Set up escalation handler.
	queue := NewQueue(storiesDir)
	err = queue.LoadFromDirectory()
	if err != nil {
		t.Fatalf("Failed to load stories: %v", err)
	}

	handler := NewEscalationHandler(tmpDir+"/logs", queue)

	// Test business question escalation.
	pendingQ := &PendingQuestion{
		ID:       "test-q-001",
		StoryID:  "001",
		AgentID:  "test-agent",
		Question: "What are the critical business requirements?",
		Context:  map[string]any{},
		AskedAt:  time.Now().UTC(),
		Status:   "pending",
	}

	ctx := context.Background()
	err = handler.EscalateBusinessQuestion(ctx, pendingQ)
	if err != nil {
		t.Fatalf("Failed to escalate business question: %v", err)
	}

	// Verify story status changed.
	story, exists := queue.GetStory("001")
	if !exists {
		t.Fatal("Test story not found")
	}

	if story.Status != StatusAwaitHumanFeedback {
		t.Errorf("Expected story status %s, got %s", StatusAwaitHumanFeedback, story.Status)
	}

	// Verify escalation was logged.
	logFile := filepath.Join(tmpDir, "logs", "escalations.jsonl")
	if _, err := os.Stat(logFile); os.IsNotExist(err) {
		t.Fatalf("Escalation log file was not created")
	}

	// Get escalation summary.
	summary := handler.GetEscalationSummary()
	if summary.PendingEscalations != 1 {
		t.Errorf("Expected 1 pending escalation, got %d", summary.PendingEscalations)
	}

	t.Log("✅ Escalation integration test completed successfully")
}
