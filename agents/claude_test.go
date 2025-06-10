package agents

import (
	"context"
	"strings"
	"testing"

	"orchestrator/pkg/proto"
)

func TestNewClaudeAgent(t *testing.T) {
	agent := NewClaudeAgent("claude")

	if agent.GetID() != "claude" {
		t.Errorf("Expected agent ID 'claude', got %s", agent.GetID())
	}
}

func TestClaudeAgent_ProcessMessage_UnsupportedType(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Test unsupported message type
	msg := proto.NewAgentMsg(proto.MsgTypeRESULT, "test", "claude")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for unsupported message type")
	}

	if response != nil {
		t.Error("Expected nil response for unsupported message type")
	}
}

func TestClaudeAgent_HandleTaskMessage_HealthEndpoint(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create task message for health endpoint
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", "Implement: Health Endpoint Implementation")
	msg.SetPayload("requirements", []interface{}{
		"GET /health endpoint",
		"Return JSON response",
		"Include status and timestamp",
	})
	msg.SetPayload("story_id", "001")

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process task message: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response message")
	}

	// Verify response structure
	if response.Type != proto.MsgTypeRESULT {
		t.Errorf("Expected RESULT message type, got %s", response.Type)
	}

	if response.FromAgent != "claude" {
		t.Errorf("Expected response from 'claude', got %s", response.FromAgent)
	}

	if response.ToAgent != "architect" {
		t.Errorf("Expected response to 'architect', got %s", response.ToAgent)
	}

	// Verify payload contents
	status, exists := response.GetPayload("status")
	if !exists {
		t.Error("Expected status in response payload")
	}
	if status != "completed" {
		t.Errorf("Expected status 'completed', got %s", status)
	}

	implementation, exists := response.GetPayload("implementation")
	if !exists {
		t.Error("Expected implementation in response payload")
	}

	implStr, ok := implementation.(string)
	if !ok || implStr == "" {
		t.Error("Expected non-empty implementation string")
	}

	// Verify health endpoint specific code
	if !strings.Contains(implStr, "HealthResponse") {
		t.Error("Expected health endpoint implementation to contain HealthResponse struct")
	}

	if !strings.Contains(implStr, "/health") {
		t.Error("Expected health endpoint implementation to contain /health route")
	}

	tests, exists := response.GetPayload("tests")
	if !exists {
		t.Error("Expected tests in response payload")
	}

	testsStr, ok := tests.(string)
	if !ok || testsStr == "" {
		t.Error("Expected non-empty tests string")
	}

	documentation, exists := response.GetPayload("documentation")
	if !exists {
		t.Error("Expected documentation in response payload")
	}

	docsStr, ok := documentation.(string)
	if !ok || docsStr == "" {
		t.Error("Expected non-empty documentation string")
	}

	files, exists := response.GetPayload("files_created")
	if !exists {
		t.Error("Expected files_created in response payload")
	}

	filesSlice, ok := files.([]string)
	if !ok {
		t.Error("Expected files_created to be a string slice")
	}

	expectedFiles := []string{"health.go", "health_test.go", "README.md"}
	if len(filesSlice) != len(expectedFiles) {
		t.Errorf("Expected %d files, got %d", len(expectedFiles), len(filesSlice))
	}

	// Verify metadata
	storyID, exists := response.GetMetadata("story_id")
	if !exists {
		t.Error("Expected story_id in response metadata")
	}
	if storyID != "001" {
		t.Errorf("Expected story_id '001', got %s", storyID)
	}

	taskType, exists := response.GetMetadata("task_type")
	if !exists {
		t.Error("Expected task_type in response metadata")
	}
	if taskType != "code_generation" {
		t.Errorf("Expected task_type 'code_generation', got %s", taskType)
	}
}

func TestClaudeAgent_HandleTaskMessage_APIImplementation(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create task message for API implementation
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", "Implement REST API endpoints")
	msg.SetPayload("requirements", []interface{}{
		"RESTful design",
		"JSON responses",
		"Error handling",
	})

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process task message: %v", err)
	}

	implementation, exists := response.GetPayload("implementation")
	if !exists {
		t.Error("Expected implementation in response payload")
	}

	implStr, ok := implementation.(string)
	if !ok || implStr == "" {
		t.Error("Expected non-empty implementation string")
	}

	// Verify API specific code
	if !strings.Contains(implStr, "APIResponse") {
		t.Error("Expected API implementation to contain APIResponse struct")
	}

	if !strings.Contains(implStr, "respondJSON") {
		t.Error("Expected API implementation to contain respondJSON function")
	}
}

func TestClaudeAgent_HandleTaskMessage_DatabaseImplementation(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create task message for database implementation
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", "Implement database connection and queries")
	msg.SetPayload("requirements", []interface{}{
		"PostgreSQL connection",
		"Connection pooling",
		"Health checks",
	})

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process task message: %v", err)
	}

	implementation, exists := response.GetPayload("implementation")
	if !exists {
		t.Error("Expected implementation in response payload")
	}

	implStr, ok := implementation.(string)
	if !ok || implStr == "" {
		t.Error("Expected non-empty implementation string")
	}

	// Verify database specific code
	if !strings.Contains(implStr, "database/sql") {
		t.Error("Expected database implementation to import database/sql")
	}

	if !strings.Contains(implStr, "NewDB") {
		t.Error("Expected database implementation to contain NewDB function")
	}
}

func TestClaudeAgent_HandleTaskMessage_DefaultImplementation(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create task message for unknown task type
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", "Implement some custom feature")
	msg.SetPayload("requirements", []interface{}{
		"Custom requirement 1",
		"Custom requirement 2",
	})

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process task message: %v", err)
	}

	implementation, exists := response.GetPayload("implementation")
	if !exists {
		t.Error("Expected implementation in response payload")
	}

	implStr, ok := implementation.(string)
	if !ok || implStr == "" {
		t.Error("Expected non-empty implementation string")
	}

	// Verify default implementation contains TODO comments with requirements
	if !strings.Contains(implStr, "Custom requirement 1") {
		t.Error("Expected default implementation to contain first requirement")
	}

	if !strings.Contains(implStr, "Custom requirement 2") {
		t.Error("Expected default implementation to contain second requirement")
	}
}

func TestClaudeAgent_HandleTaskMessage_MissingContent(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create task message without content
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for missing content")
	}

	if response != nil {
		t.Error("Expected nil response for missing content")
	}
}

func TestClaudeAgent_HandleTaskMessage_InvalidContentType(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create task message with invalid content type
	msg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude")
	msg.SetPayload("content", 123) // Invalid type

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for invalid content type")
	}

	if response != nil {
		t.Error("Expected nil response for invalid content type")
	}
}

func TestClaudeAgent_HandleQuestionMessage(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create question message
	msg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "orchestrator", "claude")
	msg.SetPayload("question", "Should I use goroutines for this implementation?")

	response, err := agent.ProcessMessage(ctx, msg)
	if err != nil {
		t.Fatalf("Failed to process question message: %v", err)
	}

	if response == nil {
		t.Fatal("Expected response message")
	}

	// Verify response is forwarded to architect
	if response.Type != proto.MsgTypeQUESTION {
		t.Errorf("Expected QUESTION message type, got %s", response.Type)
	}

	if response.FromAgent != "claude" {
		t.Errorf("Expected response from 'claude', got %s", response.FromAgent)
	}

	if response.ToAgent != "architect" {
		t.Errorf("Expected response to 'architect', got %s", response.ToAgent)
	}

	question, exists := response.GetPayload("question")
	if !exists {
		t.Error("Expected question in response payload")
	}

	if question != "Should I use goroutines for this implementation?" {
		t.Errorf("Expected original question in response, got %s", question)
	}

	context, exists := response.GetPayload("context")
	if !exists {
		t.Error("Expected context in response payload")
	}

	if context != "Code implementation question" {
		t.Errorf("Expected context 'Code implementation question', got %s", context)
	}

	originalSender, exists := response.GetMetadata("original_sender")
	if !exists {
		t.Error("Expected original_sender in response metadata")
	}

	if originalSender != "orchestrator" {
		t.Errorf("Expected original_sender 'orchestrator', got %s", originalSender)
	}
}

func TestClaudeAgent_HandleQuestionMessage_MissingQuestion(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create question message without question
	msg := proto.NewAgentMsg(proto.MsgTypeQUESTION, "orchestrator", "claude")

	response, err := agent.ProcessMessage(ctx, msg)
	if err == nil {
		t.Error("Expected error for missing question")
	}

	if response != nil {
		t.Error("Expected nil response for missing question")
	}
}

func TestClaudeAgent_HandleShutdownMessage(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	// Create shutdown message
	msg := proto.NewAgentMsg(proto.MsgTypeSHUTDOWN, "orchestrator", "claude")

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

	agentType, exists := response.GetMetadata("agent_type")
	if !exists {
		t.Error("Expected agent_type in response metadata")
	}

	if agentType != "coding_agent" {
		t.Errorf("Expected agent_type 'coding_agent', got %s", agentType)
	}
}

func TestClaudeAgent_GenerateHealthEndpointCode(t *testing.T) {
	agent := NewClaudeAgent("claude")

	code := agent.generateHealthEndpointCode()

	if code == "" {
		t.Error("Expected non-empty code")
	}

	// Check for key components
	expectedComponents := []string{
		"HealthResponse",
		"healthHandler",
		"http.StatusOK",
		"application/json",
		"time.Time",
	}

	for _, component := range expectedComponents {
		if !strings.Contains(code, component) {
			t.Errorf("Expected code to contain %s", component)
		}
	}
}

func TestClaudeAgent_GenerateHealthEndpointTests(t *testing.T) {
	agent := NewClaudeAgent("claude")

	tests := agent.generateHealthEndpointTests()

	if tests == "" {
		t.Error("Expected non-empty tests")
	}

	// Check for key test components
	expectedComponents := []string{
		"TestHealthHandler",
		"httptest.NewRequest",
		"http.StatusOK",
		"json.NewDecoder",
		"http.StatusMethodNotAllowed",
	}

	for _, component := range expectedComponents {
		if !strings.Contains(tests, component) {
			t.Errorf("Expected tests to contain %s", component)
		}
	}
}

func TestClaudeAgent_FormatRequirements(t *testing.T) {
	agent := NewClaudeAgent("claude")

	// Test with requirements
	requirements := []string{
		"Requirement 1",
		"Requirement 2",
		"Requirement 3",
	}

	formatted := agent.formatRequirements(requirements)

	for _, req := range requirements {
		if !strings.Contains(formatted, req) {
			t.Errorf("Expected formatted requirements to contain %s", req)
		}
	}

	// Test with empty requirements
	emptyFormatted := agent.formatRequirements([]string{})
	if !strings.Contains(emptyFormatted, "No specific requirements") {
		t.Error("Expected message for empty requirements")
	}
}

func TestClaudeAgent_GetCreatedFiles(t *testing.T) {
	agent := NewClaudeAgent("claude")

	testCases := []struct {
		content       string
		expectedFiles []string
	}{
		{
			content:       "Health Endpoint Implementation",
			expectedFiles: []string{"health.go", "health_test.go", "README.md"},
		},
		{
			content:       "REST API endpoints",
			expectedFiles: []string{"api.go", "api_test.go", "README.md"},
		},
		{
			content:       "Database connection",
			expectedFiles: []string{"database.go", "database_test.go", "README.md"},
		},
		{
			content:       "Some other feature",
			expectedFiles: []string{"main.go", "main_test.go", "README.md"},
		},
	}

	for _, tc := range testCases {
		files := agent.getCreatedFiles(tc.content)

		if len(files) != len(tc.expectedFiles) {
			t.Errorf("For content '%s', expected %d files, got %d", tc.content, len(tc.expectedFiles), len(files))
			continue
		}

		for i, expectedFile := range tc.expectedFiles {
			if files[i] != expectedFile {
				t.Errorf("For content '%s', expected file %d to be '%s', got '%s'", tc.content, i, expectedFile, files[i])
			}
		}
	}
}

func TestClaudeAgent_Shutdown(t *testing.T) {
	agent := NewClaudeAgent("claude")
	ctx := context.Background()

	err := agent.Shutdown(ctx)
	if err != nil {
		t.Errorf("Expected no error during shutdown, got: %v", err)
	}
}
