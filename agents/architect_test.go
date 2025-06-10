package agents

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/proto"
)

func TestNewArchitectAgent(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")

	if agent.GetID() != "architect" {
		t.Errorf("Expected agent ID 'architect', got %s", agent.GetID())
	}

	if agent.storiesDir != "stories" {
		t.Errorf("Expected stories directory 'stories', got %s", agent.storiesDir)
	}
}

func TestArchitectAgent_ProcessMessage_UnsupportedType(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	// Test unsupported message type
	msg := proto.NewAgentMsg(proto.MsgTypeRESULT, "test", "architect")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for unsupported message type")
	}

	if response != nil {
		t.Error("Expected nil response for unsupported message type")
	}
}

func TestArchitectAgent_HandleTaskMessage(t *testing.T) {
	// Create temporary directory with test story
	tmpDir := t.TempDir()
	storyFile := filepath.Join(tmpDir, "001.md")
	storyContent := `# Health Endpoint Implementation

This story implements a basic health check endpoint.

## Requirements
- GET /health endpoint
- Return 200 OK status
- JSON response format
- Include system status information

## Acceptance Criteria
- Endpoint responds to GET requests
- Returns valid JSON
- Includes timestamp and status
`

	err := os.WriteFile(storyFile, []byte(storyContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create test story file: %v", err)
	}

	agent := NewArchitectAgent("architect", tmpDir)
	// Set a mock dispatcher for testing
	mockDispatcher := &MockDispatcher{}
	agent.SetDispatcher(mockDispatcher)
	ctx := context.Background()

	// Create task message with story ID
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect")
	msg.SetPayload("story_id", "001")

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process task message: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response message")
	}

	// Verify response
	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected RESULT message type, got %s", response.Type)
	}

	if response.FromAgent != "architect" {
		t.Errorf("Expected response from 'architect', got %s", response.FromAgent)
	}

	status, exists := response.GetPayload("status")
	if !exists {
		t.Error("Expected status in response payload")
	}

	if status != "task_created" {
		t.Errorf("Expected status 'task_created', got %s", status)
	}

	// Verify task message ID is included
	_, exists = response.GetPayload("task_message_id")
	if !exists {
		t.Error("Expected task_message_id in response payload")
	}

	// Verify message was dispatched to coding agent
	dispatchedMessages := mockDispatcher.GetMessages()
	if len(dispatchedMessages) != 1 {
		t.Errorf("Expected 1 dispatched message, got %d", len(dispatchedMessages))
	}

	if len(dispatchedMessages) > 0 {
		dispatchedMsg := dispatchedMessages[0]
		if dispatchedMsg.Type != proto.MsgTypeTASK {
			t.Errorf("Expected dispatched message type TASK, got %s", dispatchedMsg.Type)
		}
		if dispatchedMsg.ToAgent != "claude" {
			t.Errorf("Expected dispatched message to 'claude', got %s", dispatchedMsg.ToAgent)
		}
		if dispatchedMsg.FromAgent != "architect" {
			t.Errorf("Expected dispatched message from 'architect', got %s", dispatchedMsg.FromAgent)
		}
	}
}

func TestArchitectAgent_HandleTaskMessage_MissingStoryID(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	// Create task message without story ID
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for missing story_id")
	}

	if response != nil {
		t.Error("Expected nil response for missing story_id")
	}
}

func TestArchitectAgent_HandleTaskMessage_InvalidStoryID(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	// Create task message with invalid story ID type
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect")
	msg.SetPayload("story_id", 123) // Invalid type

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for invalid story_id type")
	}

	if response != nil {
		t.Error("Expected nil response for invalid story_id type")
	}
}

func TestArchitectAgent_HandleTaskMessage_MissingStoryFile(t *testing.T) {
	tmpDir := t.TempDir()
	agent := NewArchitectAgent("architect", tmpDir)
	ctx := context.Background()

	// Create task message with non-existent story
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "orchestrator", "architect")
	msg.SetPayload("story_id", "nonexistent")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for missing story file")
	}

	if response != nil {
		t.Error("Expected nil response for missing story file")
	}
}

func TestArchitectAgent_HandleQuestionMessage(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	// Create question message
	msg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "claude", "architect")
	msg.SetPayload("question", "What database pattern should I use?")

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process question message: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response message")
	}

	// Verify response
	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected RESULT message type, got %s", response.Type)
	}

	answer, exists := response.GetPayload("answer")
	if !exists {
		t.Error("Expected answer in response payload")
	}

	answerStr, ok := answer.(string)
	if !ok || answerStr == "" {
		t.Error("Expected non-empty answer string")
	}

	// Verify question is echoed back
	question, exists := response.GetPayload("question")
	if !exists {
		t.Error("Expected question in response payload")
	}

	if question != "What database pattern should I use?" {
		t.Errorf("Expected original question in response, got %s", question)
	}
}

func TestArchitectAgent_HandleQuestionMessage_MissingQuestion(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	// Create question message without question
	msg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "claude", "architect")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for missing question")
	}

	if response != nil {
		t.Error("Expected nil response for missing question")
	}
}

func TestArchitectAgent_HandleShutdownMessage(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	// Create shutdown message
	msg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", "architect")

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process shutdown message: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response message")
	}

	// Verify response
	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected RESULT message type, got %s", response.Type)
	}

	status, exists := response.GetPayload("status")
	if !exists {
		t.Error("Expected status in response payload")
	}

	if status != "shutdown_acknowledged" {
		t.Errorf("Expected status 'shutdown_acknowledged', got %s", status)
	}
}

func TestArchitectAgent_GenerateTaskFromStory(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")

	story := `# Health Endpoint
	
Basic health check implementation.

- GET /health endpoint
- Return JSON response
- Include system status
`

	task := agent.generateTaskFromStory(story)

	if task == "" {
		t.Error("Expected non-empty task content")
	}

	if !contains(task, "Health Endpoint") {
		t.Error("Expected task to contain story title")
	}

	if !contains(task, "GET /health") {
		t.Error("Expected task to contain requirements")
	}
}

func TestArchitectAgent_ExtractRequirements(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")

	story := `# Test Story

Some description.

- First requirement
- Second requirement
* Third requirement

1. Numbered requirement
2. Another numbered requirement

Some other content.
`

	requirements := agent.extractRequirements(story)

	expectedCount := 5
	if len(requirements) != expectedCount {
		t.Errorf("Expected %d requirements, got %d", expectedCount, len(requirements))
	}

	expected := []string{
		"First requirement",
		"Second requirement",
		"Third requirement",
		"Numbered requirement",
		"Another numbered requirement",
	}

	for i, req := range expected {
		if i >= len(requirements) || requirements[i] != req {
			t.Errorf("Expected requirement %d to be '%s', got '%s'", i, req, requirements[i])
		}
	}
}

func TestArchitectAgent_GenerateAnswer(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")

	testCases := []struct {
		question string
		keywords []string
	}{
		{
			question: "What architecture pattern should I use?",
			keywords: []string{"clean architecture", "separation of concerns"},
		},
		{
			question: "How should I handle database operations?",
			keywords: []string{"storage", "ACID"},
		},
		{
			question: "What about API design?",
			keywords: []string{"RESTful", "HTTP status codes"},
		},
		{
			question: "How do I test this?",
			keywords: []string{"unit tests", "integration tests"},
		},
		{
			question: "Will this perform well?",
			keywords: []string{"performance", "caching"},
		},
		{
			question: "Some random question",
			keywords: []string{"trade-offs", "best practices"},
		},
	}

	for _, tc := range testCases {
		answer := agent.generateAnswer(tc.question)

		if answer == "" {
			t.Errorf("Expected non-empty answer for question: %s", tc.question)
			continue
		}

		foundKeyword := false
		for _, keyword := range tc.keywords {
			if contains(answer, keyword) {
				foundKeyword = true
				break
			}
		}

		if !foundKeyword {
			t.Errorf("Expected answer to contain one of %v for question '%s', got: %s",
				tc.keywords, tc.question, answer)
		}
	}
}

func TestArchitectAgent_Shutdown(t *testing.T) {
	agent := NewArchitectAgent("architect", "stories")
	ctx := context.Background()

	err := agent.Shutdown(ctx)
	if err != nil {
		t.Errorf("Expected no error during shutdown, got: %v", err)
	}
}

// Helper function to check if a string contains a substring (case-insensitive)
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(substr) == 0 ||
			(len(s) > len(substr) && containsAt(s, substr, 0)))
}

func containsAt(s, substr string, start int) bool {
	if start+len(substr) > len(s) {
		return false
	}

	for i := 0; i < len(substr); i++ {
		if toLower(s[start+i]) != toLower(substr[i]) {
			if start+1 <= len(s)-len(substr) {
				return containsAt(s, substr, start+1)
			}
			return false
		}
	}
	return true
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// MockDispatcher for testing
type MockDispatcher struct {
	messages []*proto.AgentMsg
	errors   []error
}

func (m *MockDispatcher) DispatchMessage(msg *proto.AgentMsg) error {
	m.messages = append(m.messages, msg.Clone())
	if len(m.errors) > 0 {
		err := m.errors[0]
		m.errors = m.errors[1:]
		return err
	}
	return nil
}

func (m *MockDispatcher) GetMessages() []*proto.AgentMsg {
	return m.messages
}

func (m *MockDispatcher) AddError(err error) {
	m.errors = append(m.errors, err)
}
