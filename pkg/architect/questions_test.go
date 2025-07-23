package architect

import (
	"context"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

func TestNewQuestionHandler(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)

	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	if handler == nil {
		t.Fatal("NewQuestionHandler returned nil")
	}

	if handler.queue != queue {
		t.Error("Queue not set correctly")
	}

	if handler.renderer != renderer {
		t.Error("Renderer not set correctly")
	}

	if handler.pendingQuestions == nil {
		t.Error("PendingQuestions map not initialized")
	}

	if len(handler.pendingQuestions) != 0 {
		t.Error("PendingQuestions should be empty initially")
	}
}

func TestHandleQuestion(t *testing.T) {
	// Create test setup.
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
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler) // nil LLM = mock mode

	// Create QUESTION message.
	questionMsg := proto.NewAgentMsg(
		proto.MsgTypeQUESTION,
		"test-agent",
		"architect",
	)
	questionMsg.Payload["story_id"] = "001"
	questionMsg.Payload[proto.KeyQuestion] = "How should I implement the user authentication?"
	questionMsg.Payload["context"] = "Working on login functionality"

	ctx := context.Background()
	err := handler.HandleQuestion(ctx, questionMsg)
	if err != nil {
		t.Fatalf("Failed to handle question: %v", err)
	}

	// Verify question was stored.
	if len(handler.pendingQuestions) != 1 {
		t.Errorf("Expected 1 pending question, got %d", len(handler.pendingQuestions))
	}

	pendingQ, exists := handler.pendingQuestions[questionMsg.ID]
	if !exists {
		t.Fatal("Question not found in pending questions")
	}

	if pendingQ.StoryID != "001" {
		t.Errorf("Expected story ID '001', got '%s'", pendingQ.StoryID)
	}

	if pendingQ.AgentID != "test-agent" {
		t.Errorf("Expected agent ID 'test-agent', got '%s'", pendingQ.AgentID)
	}

	if pendingQ.Question != "How should I implement the user authentication?" {
		t.Errorf("Question not stored correctly")
	}

	if pendingQ.Status != "answered" {
		t.Errorf("Expected status 'answered', got '%s'", pendingQ.Status)
	}

	if pendingQ.Answer == "" {
		t.Error("Answer should not be empty")
	}
}

func TestHandleQuestionInvalid(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Test missing story_id.
	questionMsg := proto.NewAgentMsg(
		proto.MsgTypeQUESTION,
		"test-agent",
		"architect",
	)
	questionMsg.Payload[proto.KeyQuestion] = "How should I implement this?"

	ctx := context.Background()
	err := handler.HandleQuestion(ctx, questionMsg)
	if err == nil {
		t.Error("Expected error for missing story_id")
	}

	// Test missing question.
	questionMsg2 := proto.NewAgentMsg(
		proto.MsgTypeQUESTION,
		"test-agent",
		"architect",
	)
	questionMsg2.Payload["story_id"] = "001"

	err = handler.HandleQuestion(ctx, questionMsg2)
	if err == nil {
		t.Error("Expected error for missing question")
	}
}

func TestIsBusinessQuestion(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Test technical questions (should not be business)
	technicalQuestions := []string{
		"How do I implement this function?",
		"What's the best way to handle errors?",
		"Which algorithm should I use?",
		"How to optimize this code?",
	}

	for _, question := range technicalQuestions {
		if handler.isBusinessQuestion(question, map[string]any{}) {
			t.Errorf("Technical question incorrectly identified as business: %s", question)
		}
	}

	// Test business questions (should be business)
	businessQuestions := []string{
		"What are the business requirements for this feature?",
		"How does this affect our revenue model?",
		"What's the compliance policy for data storage?",
		"Should we prioritize customer feedback over stakeholder requests?",
	}

	for _, question := range businessQuestions {
		if !handler.isBusinessQuestion(question, map[string]any{}) {
			t.Errorf("Business question not identified correctly: %s", question)
		}
	}

	// Test explicit business flag.
	context := map[string]any{
		"is_business_question": true,
	}

	if !handler.isBusinessQuestion("Any question", context) {
		t.Error("Explicit business flag not recognized")
	}
}

func TestBusinessQuestionEscalation(t *testing.T) {
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		EstimatedPoints: 2,
	}

	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Create business question message.
	questionMsg := proto.NewAgentMsg(
		proto.MsgTypeQUESTION,
		"test-agent",
		"architect",
	)
	questionMsg.Payload["story_id"] = "001"
	questionMsg.Payload[proto.KeyQuestion] = "What are the business requirements for this feature?"

	ctx := context.Background()
	err := handler.HandleQuestion(ctx, questionMsg)
	if err != nil {
		t.Fatalf("Failed to handle business question: %v", err)
	}

	// Verify question was escalated.
	pendingQ, exists := handler.pendingQuestions[questionMsg.ID]
	if !exists {
		t.Fatal("Question not found in pending questions")
	}

	if pendingQ.Status != "escalated" {
		t.Errorf("Expected status 'escalated', got '%s'", pendingQ.Status)
	}
}

func TestGetPendingQuestions(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Add some pending questions.
	handler.pendingQuestions["q1"] = &PendingQuestion{
		ID:      "q1",
		StoryID: "001",
		Status:  "pending",
	}
	handler.pendingQuestions["q2"] = &PendingQuestion{
		ID:      "q2",
		StoryID: "002",
		Status:  "answered",
	}

	questions := handler.GetPendingQuestions()

	if len(questions) != 2 {
		t.Errorf("Expected 2 questions, got %d", len(questions))
	}
}

func TestGetQuestionStatus(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Add questions with different statuses.
	handler.pendingQuestions["q1"] = &PendingQuestion{
		ID:      "q1",
		StoryID: "001",
		Status:  "pending",
	}
	handler.pendingQuestions["q2"] = &PendingQuestion{
		ID:      "q2",
		StoryID: "002",
		Status:  "answered",
	}
	handler.pendingQuestions["q3"] = &PendingQuestion{
		ID:      "q3",
		StoryID: "003",
		Status:  "escalated",
	}

	status := handler.GetQuestionStatus()

	if status.TotalQuestions != 3 {
		t.Errorf("Expected 3 total questions, got %d", status.TotalQuestions)
	}

	if status.PendingQuestions != 1 {
		t.Errorf("Expected 1 pending question, got %d", status.PendingQuestions)
	}

	if status.AnsweredQuestions != 1 {
		t.Errorf("Expected 1 answered question, got %d", status.AnsweredQuestions)
	}

	if status.EscalatedQuestions != 1 {
		t.Errorf("Expected 1 escalated question, got %d", status.EscalatedQuestions)
	}
}

func TestClearAnsweredQuestions(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	// Add questions with different statuses.
	handler.pendingQuestions["q1"] = &PendingQuestion{
		ID:      "q1",
		StoryID: "001",
		Status:  "pending",
	}
	handler.pendingQuestions["q2"] = &PendingQuestion{
		ID:      "q2",
		StoryID: "002",
		Status:  "answered",
	}
	handler.pendingQuestions["q3"] = &PendingQuestion{
		ID:      "q3",
		StoryID: "003",
		Status:  "answered",
	}

	cleared := handler.ClearAnsweredQuestions()

	if cleared != 2 {
		t.Errorf("Expected 2 questions cleared, got %d", cleared)
	}

	if len(handler.pendingQuestions) != 1 {
		t.Errorf("Expected 1 question remaining, got %d", len(handler.pendingQuestions))
	}

	// Verify only pending question remains.
	if _, exists := handler.pendingQuestions["q1"]; !exists {
		t.Error("Pending question should not be cleared")
	}
}

func TestFormatQuestionContext(t *testing.T) {
	queue := NewQueue("/tmp/test")
	renderer, _ := templates.NewRenderer()
	escalationHandler := NewEscalationHandler("/tmp/test/logs", queue)
	handler := NewQuestionHandler(nil, renderer, queue, escalationHandler)

	story := &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusInProgress,
		EstimatedPoints: 2,
		DependsOn:       []string{"002"},
		FilePath:        "/tmp/test/001.md",
	}

	pendingQ := &PendingQuestion{
		ID:       "q1",
		StoryID:  "001",
		AgentID:  "test-agent",
		Question: "How do I implement this?",
		AskedAt:  time.Now(),
		Context: map[string]any{
			"code_snippet": "func test() {}",
			"file_path":    "/src/main.go",
		},
	}

	context := handler.formatQuestionContext(pendingQ, story)

	if !strings.Contains(context, "Story ID: 001") {
		t.Error("Context should include story ID")
	}

	if !strings.Contains(context, "Test Story") {
		t.Error("Context should include story title")
	}

	if !strings.Contains(context, "test-agent") {
		t.Error("Context should include agent ID")
	}

	if !strings.Contains(context, "How do I implement this?") {
		t.Error("Context should include question")
	}

	if !strings.Contains(context, "code_snippet") {
		t.Error("Context should include additional context")
	}
}

func TestTruncateString(t *testing.T) {
	//nolint:govet // Test struct, optimization not critical
	tests := []struct {
		input     string
		maxLength int
		expected  string
	}{
		{"short", 10, "short"},
		{"this is a long string", 10, "this is a ..."},
		{"exact", 5, "exact"},
		{"", 5, ""},
	}

	for _, test := range tests {
		result := truncateString(test.input, test.maxLength)
		if result != test.expected {
			t.Errorf("truncateString(%q, %d) = %q, expected %q",
				test.input, test.maxLength, result, test.expected)
		}
	}
}
