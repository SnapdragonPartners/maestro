package architect

import (
	"context"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

func TestNewReviewEvaluator(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()

	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	if evaluator == nil {
		t.Fatal("NewReviewEvaluator returned nil")
	}

	if evaluator.queue != queue {
		t.Error("Queue not set correctly")
	}

	if evaluator.renderer != renderer {
		t.Error("Renderer not set correctly")
	}

	if evaluator.workspaceDir != "/tmp/workspace" {
		t.Error("Workspace directory not set correctly")
	}

	if evaluator.pendingReviews == nil {
		t.Error("PendingReviews map not initialized")
	}

	if len(evaluator.pendingReviews) != 0 {
		t.Error("PendingReviews should be empty initially")
	}
}

func TestReviewHandleResult(t *testing.T) {
	// Create test setup
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		EstimatedPoints: 2,
		FilePath:        "/tmp/test/001.md",
	}

	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1)                                                                     // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh) // nil LLM = mock mode

	// Create RESULT message (code submission)
	resultMsg := proto.NewAgentMsg(
		proto.MsgTypeRESULT,
		"test-agent",
		"architect",
	)
	resultMsg.Payload["story_id"] = "001"
	resultMsg.Payload["code_path"] = "/workspace/src/main.go"
	resultMsg.Payload["code_content"] = "package main\n\nfunc main() {\n\tprintln(\"Hello, World!\")\n}"
	resultMsg.Payload["implementation_notes"] = "Simple hello world implementation"

	ctx := context.Background()
	err := evaluator.HandleResult(ctx, resultMsg)
	if err != nil {
		t.Fatalf("Failed to handle result: %v", err)
	}

	// Verify review was created
	if len(evaluator.pendingReviews) != 1 {
		t.Errorf("Expected 1 pending review, got %d", len(evaluator.pendingReviews))
	}

	pendingReview, exists := evaluator.pendingReviews[resultMsg.ID]
	if !exists {
		t.Fatal("Review not found in pending reviews")
	}

	if pendingReview.StoryID != "001" {
		t.Errorf("Expected story ID '001', got '%s'", pendingReview.StoryID)
	}

	if pendingReview.AgentID != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got '%s'", pendingReview.AgentID)
	}

	if pendingReview.CodePath != "/workspace/src/main.go" {
		t.Errorf("Code path not stored correctly")
	}

	if !strings.Contains(pendingReview.CodeContent, "Hello, World!") {
		t.Error("Code content not stored correctly")
	}

	// In mock mode, review should be completed (either approved or needs_fixes depending on checks)
	if pendingReview.Status != "approved" && pendingReview.Status != "needs_fixes" {
		t.Errorf("Expected status 'approved' or 'needs_fixes', got '%s'", pendingReview.Status)
	}

	// Verify story status changed appropriately
	story, _ := queue.GetStory("001")
	if pendingReview.Status == "approved" {
		if story.Status != StatusCompleted {
			t.Errorf("Expected story status completed for approved review, got %s", story.Status)
		}
	} else if pendingReview.Status == "needs_fixes" {
		if story.Status != StatusWaitingReview {
			t.Errorf("Expected story status waiting_review for needs_fixes review, got %s", story.Status)
		}
	}
}

func TestReviewHandleResultInvalid(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	// Test missing story_id
	resultMsg := proto.NewAgentMsg(
		proto.MsgTypeRESULT,
		"test-agent",
		"architect",
	)
	resultMsg.Payload["code_content"] = "some code"

	ctx := context.Background()
	err := evaluator.HandleResult(ctx, resultMsg)
	if err == nil {
		t.Error("Expected error for missing story_id")
	}
}

func TestRunAutomatedChecks(t *testing.T) {
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		EstimatedPoints: 2,
	}

	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	pendingReview := &PendingReview{
		ID:           "review1",
		StoryID:      "001",
		AgentID:      "test-agent",
		CodeContent:  "package main\n\nfunc main() {}",
		ChecksRun:    []string{},
		CheckResults: make(map[string]bool),
	}

	ctx := context.Background()

	// Run automated checks (will likely fail due to missing tools, but should not error)
	passed, err := evaluator.runAutomatedChecks(ctx, pendingReview)
	if err != nil {
		t.Fatalf("Automated checks should not error: %v", err)
	}

	// Verify checks were attempted
	if len(pendingReview.ChecksRun) == 0 {
		t.Error("No checks were run")
	}

	expectedChecks := []string{"format", "lint", "test"}
	for _, check := range expectedChecks {
		found := false
		for _, runCheck := range pendingReview.ChecksRun {
			if runCheck == check {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Check %s was not run", check)
		}
	}

	// In test environment, checks will likely pass due to missing tools
	if !passed {
		t.Log("Checks failed as expected in test environment (no dev tools)")
	} else {
		t.Log("Checks passed (dev tools available)")
	}
}

func TestCommandExists(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	// Test with a command that should exist
	if !evaluator.commandExists("ls") {
		t.Error("ls command should exist on most systems")
	}

	// Test with a command that should not exist
	if evaluator.commandExists("definitely-not-a-real-command-12345") {
		t.Error("Fake command should not exist")
	}
}

func TestGenerateFixFeedback(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	pendingReview := &PendingReview{
		ID:      "review1",
		StoryID: "001",
		CheckResults: map[string]bool{
			"format": false,
			"lint":   true,
			"test":   false,
		},
	}

	feedback := evaluator.generateFixFeedback(pendingReview)

	if !strings.Contains(feedback, "formatting issues") {
		t.Error("Feedback should mention formatting issues")
	}

	if strings.Contains(feedback, "Linting issues") {
		t.Error("Feedback should not mention linting issues (lint passed)")
	}

	if !strings.Contains(feedback, "Tests are failing") {
		t.Error("Feedback should mention test failures")
	}

	if !strings.Contains(feedback, "resubmit") {
		t.Error("Feedback should ask for resubmission")
	}
}

func TestFormatReviewContext(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	story := &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		EstimatedPoints: 2,
		DependsOn:       []string{"002"},
	}

	pendingReview := &PendingReview{
		ID:          "review1",
		StoryID:     "001",
		AgentID:     "test-agent",
		CodePath:    "/workspace/main.go",
		SubmittedAt: time.Now(),
		ChecksRun:   []string{"format", "test"},
		CheckResults: map[string]bool{
			"format": true,
			"test":   false,
		},
		Context: map[string]any{
			"implementation_notes": "Added user authentication",
			"files_changed":        3,
		},
	}

	context := evaluator.formatReviewContext(pendingReview, story)

	if !strings.Contains(context, "Story ID: 001") {
		t.Error("Context should include story ID")
	}

	if !strings.Contains(context, "Test Story") {
		t.Error("Context should include story title")
	}

	if !strings.Contains(context, "test-agent") {
		t.Error("Context should include agent ID")
	}

	if !strings.Contains(context, "/workspace/main.go") {
		t.Error("Context should include code path")
	}

	if !strings.Contains(context, "✅ PASSED") {
		t.Error("Context should show passed checks")
	}

	if !strings.Contains(context, "❌ FAILED") {
		t.Error("Context should show failed checks")
	}

	if !strings.Contains(context, "implementation_notes") {
		t.Error("Context should include submission context")
	}
}

func TestGetPendingReviews(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	// Add some pending reviews
	evaluator.pendingReviews["r1"] = &PendingReview{
		ID:      "r1",
		StoryID: "001",
		Status:  "pending",
	}
	evaluator.pendingReviews["r2"] = &PendingReview{
		ID:      "r2",
		StoryID: "002",
		Status:  "approved",
	}

	reviews := evaluator.GetPendingReviews()

	if len(reviews) != 2 {
		t.Errorf("Expected 2 reviews, got %d", len(reviews))
	}
}

func TestGetReviewStatus(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	// Add reviews with different statuses
	evaluator.pendingReviews["r1"] = &PendingReview{
		ID:      "r1",
		StoryID: "001",
		Status:  "pending",
	}
	evaluator.pendingReviews["r2"] = &PendingReview{
		ID:      "r2",
		StoryID: "002",
		Status:  "approved",
	}
	evaluator.pendingReviews["r3"] = &PendingReview{
		ID:      "r3",
		StoryID: "003",
		Status:  "needs_fixes",
	}

	status := evaluator.GetReviewStatus()

	if status.TotalReviews != 3 {
		t.Errorf("Expected 3 total reviews, got %d", status.TotalReviews)
	}

	if status.PendingReviews != 1 {
		t.Errorf("Expected 1 pending review, got %d", status.PendingReviews)
	}

	if status.ApprovedReviews != 1 {
		t.Errorf("Expected 1 approved review, got %d", status.ApprovedReviews)
	}

	if status.NeedsFixesReviews != 1 {
		t.Errorf("Expected 1 needs_fixes review, got %d", status.NeedsFixesReviews)
	}
}

func TestClearCompletedReviews(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	// Add reviews with different statuses
	evaluator.pendingReviews["r1"] = &PendingReview{
		ID:      "r1",
		StoryID: "001",
		Status:  "pending",
	}
	evaluator.pendingReviews["r2"] = &PendingReview{
		ID:      "r2",
		StoryID: "002",
		Status:  "approved",
	}
	evaluator.pendingReviews["r3"] = &PendingReview{
		ID:      "r3",
		StoryID: "003",
		Status:  "rejected",
	}

	cleared := evaluator.ClearCompletedReviews()

	if cleared != 2 {
		t.Errorf("Expected 2 reviews cleared, got %d", cleared)
	}

	if len(evaluator.pendingReviews) != 1 {
		t.Errorf("Expected 1 review remaining, got %d", len(evaluator.pendingReviews))
	}

	// Verify only pending review remains
	if _, exists := evaluator.pendingReviews["r1"]; !exists {
		t.Error("Pending review should not be cleared")
	}
}

func TestProcessLLMReviewResponse(t *testing.T) {
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusWaitingReview,
		EstimatedPoints: 2,
	}

	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	mergeCh := make(chan string, 1) // Test merge channel
	evaluator := NewReviewEvaluator(nil, renderer, queue, "/tmp/workspace", escalationHandler, mergeCh)

	pendingReview := &PendingReview{
		ID:      "review1",
		StoryID: "001",
		AgentID: "test-agent",
		Status:  "pending",
	}

	ctx := context.Background()

	// Test approval response
	approvalResponse := "The code looks good and is approved. LGTM!"
	err := evaluator.processLLMReviewResponse(ctx, pendingReview, approvalResponse)
	if err != nil {
		t.Fatalf("Failed to process approval response: %v", err)
	}

	if pendingReview.Status != "approved" {
		t.Errorf("Expected status 'approved', got '%s'", pendingReview.Status)
	}

	// Verify story was marked as completed
	story, _ := queue.GetStory("001")
	if story.Status != StatusCompleted {
		t.Errorf("Expected story status completed, got %s", story.Status)
	}

	// Test rejection response with a new review
	pendingReview2 := &PendingReview{
		ID:      "review2",
		StoryID: "002",
		AgentID: "test-agent",
		Status:  "pending",
	}

	// Add story 002
	queue.stories["002"] = &QueuedStory{
		ID:              "002",
		Title:           "Test Story 2",
		Status:          StatusInProgress,
		EstimatedPoints: 1,
	}

	rejectionResponse := "Issues found in the code. Needs changes before approval."
	err = evaluator.processLLMReviewResponse(ctx, pendingReview2, rejectionResponse)
	if err != nil {
		t.Fatalf("Failed to process rejection response: %v", err)
	}

	if pendingReview2.Status != "needs_fixes" {
		t.Errorf("Expected status 'needs_fixes', got '%s'", pendingReview2.Status)
	}
}
