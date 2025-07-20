package coder

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// createMockLLMClient creates a mock LLM client with predefined responses for testing
func createMockLLMClient() *agent.MockLLMClient {
	// Need more responses to handle iterative planning workflow
	responses := []agent.CompletionResponse{
		{Content: "Mock planning iteration 1: Exploring codebase..."},
		{Content: "Mock planning iteration 2: Analyzing patterns..."},
		{Content: "Mock planning iteration 3: Finalizing plan..."},
		{Content: "Mock coding response: Creating JWT authentication files..."},
		{Content: "Mock fixing response: Addressing test failures..."},
		{Content: "Mock additional response for extended workflows..."},
		{Content: "Mock fallback response..."},
	}
	return agent.NewMockLLMClient(responses, nil)
}

// TestEnhancedPlanningWorkflow tests the complete enhanced planning flow
func TestEnhancedPlanningWorkflow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "enhanced-planning-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 8192, // Increased for enhanced planning
		MaxReplyTokens:   2048,
		CompactionBuffer: 512,
	}

	// Create mock LLM client
	mockLLM := createMockLLMClient()

	// Create driver with mock LLM client
	driver, err := NewCoder("enhanced-test-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Test task that should trigger enhanced planning
	planningTask := "Implement user authentication system with JWT tokens and password hashing"
	if err := driver.ProcessTask(ctx, planningTask); err != nil {
		t.Fatalf("Failed to process planning task: %v", err)
	}

	// Verify we're in PLANNING state initially
	currentState := driver.GetCurrentState()
	if currentState != StatePlanning {
		t.Errorf("Expected initial state to be PLANNING, got %s", currentState)
	}

	// Verify enhanced planning features are working
	stateData := driver.GetStateData()

	// Check that task content is preserved
	if taskContent, exists := stateData["task_content"]; !exists || taskContent != planningTask {
		t.Errorf("Expected task_content to be preserved, got %v", taskContent)
	}

	// Check that tree output should be generated during planning
	// Note: In mock mode this may not be available, but we can check the data structure
	if _, exists := stateData["tree_output"]; !exists {
		t.Log("Tree output not available in mock mode - this is expected")
	}

	// Simulate successful planning completion
	// In a real scenario, this would be triggered by tool usage
	driver.SetStateData("plan", "Mock enhanced plan: JWT auth system with bcrypt hashing")
	driver.SetStateData("plan_confidence", "HIGH")
	driver.SetStateData("exploration_summary", "Found existing auth patterns in codebase")
	driver.SetStateData("planning_completed_at", time.Now().UTC())

	// Transition to PLAN_REVIEW
	if err := driver.TransitionTo(ctx, StatePlanReview, nil); err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}

	// Process plan review state
	_, _, err = driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process PLAN_REVIEW state: %v", err)
	}

	// Should have pending approval request
	hasPending, approvalID, content, reason, approvalType := driver.GetPendingApprovalRequest()
	if !hasPending {
		t.Error("Expected pending approval request after plan submission")
	}
	if approvalType != proto.ApprovalTypePlan {
		t.Errorf("Expected plan approval, got %s", approvalType)
	}
	if content == "" || reason == "" || approvalID == "" {
		t.Error("Approval request should have content, reason, and ID")
	}

	// Approve the enhanced plan
	if err := driver.ProcessApprovalResult(proto.ApprovalStatusApproved.String(), proto.ApprovalTypePlan.String()); err != nil {
		t.Fatalf("Failed to process plan approval: %v", err)
	}

	// Continue processing
	if err := driver.Run(ctx); err != nil {
		t.Fatalf("Failed to continue after plan approval: %v", err)
	}

	// Should move to CODING state
	finalState := driver.GetCurrentState()
	if finalState != StateCoding {
		t.Errorf("Expected state to be CODING after plan approval, got %s", finalState)
	}

	// Verify planning data is preserved
	finalStateData := driver.GetStateData()
	if plan, exists := finalStateData["plan"]; !exists || plan == "" {
		t.Error("Expected plan to be preserved after approval")
	}
	if confidence, exists := finalStateData["plan_confidence"]; !exists || confidence != "HIGH" {
		t.Error("Expected plan confidence to be preserved")
	}
}

// TestContainerLifecycleManagement tests readonly→readwrite container transitions
func TestContainerLifecycleManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "container-lifecycle-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	// Create mock LLM client
	mockLLM := createMockLLMClient()

	driver, err := NewCoder("container-test-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Test task
	task := "Create simple HTTP server"
	if err := driver.ProcessTask(ctx, task); err != nil {
		t.Fatalf("Failed to process task: %v", err)
	}

	// In PLANNING state, container should be configured for readonly access
	currentState := driver.GetCurrentState()
	if currentState != StatePlanning {
		t.Errorf("Expected state to be PLANNING, got %s", currentState)
	}

	// Mock planning completion
	driver.SetStateData("plan", "Mock plan: Simple HTTP server with health endpoint")
	driver.SetStateData("planning_completed_at", time.Now().UTC())

	// Transition to PLAN_REVIEW and approve
	if err := driver.TransitionTo(ctx, StatePlanReview, nil); err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}

	// Process approval
	if err := driver.ProcessApprovalResult(proto.ApprovalStatusApproved.String(), proto.ApprovalTypePlan.String()); err != nil {
		t.Fatalf("Failed to process approval: %v", err)
	}

	// Continue to CODING state
	if err := driver.Run(ctx); err != nil {
		t.Fatalf("Failed to run after approval: %v", err)
	}

	// Should be in CODING state now
	codingState := driver.GetCurrentState()
	if codingState != StateCoding {
		t.Errorf("Expected state to be CODING, got %s", codingState)
	}

	// In CODING state, container should be reconfigured for readwrite access
	// Note: In mock mode we can't test actual container operations, but we can verify
	// the state transitions work correctly
	t.Log("Container lifecycle transitions completed successfully in mock mode")
}

// TestPlanningContextPreservation tests context preservation during QUESTION transitions
func TestPlanningContextPreservation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "context-preservation-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	// Create mock LLM client
	mockLLM := createMockLLMClient()

	driver, err := NewCoder("context-test-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Start with a task
	task := "Implement complex distributed system with microservices"
	if err := driver.ProcessTask(ctx, task); err != nil {
		t.Fatalf("Failed to process task: %v", err)
	}

	// Should be in PLANNING state
	if driver.GetCurrentState() != StatePlanning {
		t.Errorf("Expected state to be PLANNING, got %s", driver.GetCurrentState())
	}

	// Simulate planning context being built up
	driver.SetStateData("exploration_findings", map[string]interface{}{
		"existing_services": []string{"user-service", "auth-service"},
		"patterns_found":    []string{"REST", "gRPC", "event-driven"},
	})

	// Simulate question submission (would normally be via tool)
	driver.SetStateData("question_submitted", map[string]interface{}{
		"question": "Should I use GraphQL or REST for the new API?",
		"context":  "Found existing REST services but team mentioned GraphQL",
		"urgency":  "HIGH",
	})

	// Process the question transition
	_, _, err = driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process question transition: %v", err)
	}

	// Should be in QUESTION state
	if driver.GetCurrentState() != StateQuestion {
		t.Errorf("Expected state to be QUESTION, got %s", driver.GetCurrentState())
	}

	// Verify question data is set correctly
	stateData := driver.GetStateData()
	if origin, exists := stateData["question_origin"]; !exists || origin != string(StatePlanning) {
		t.Errorf("Expected question_origin to be PLANNING, got %v", origin)
	}

	// Verify planning context was saved
	if saved, exists := stateData["planning_context_saved"]; !exists || saved == nil {
		t.Error("Expected planning context to be saved during question transition")
	}

	// Simulate architect answer
	if err := driver.ProcessAnswer("Use REST for consistency with existing services"); err != nil {
		t.Fatalf("Failed to process answer: %v", err)
	}

	// Set the flag to indicate question was answered
	driver.SetStateData("question_answered", true)

	// Process return to planning
	if err := driver.Run(ctx); err != nil {
		t.Fatalf("Failed to continue after answer: %v", err)
	}

	// Should return to PLANNING state
	returnState := driver.GetCurrentState()
	if returnState != StatePlanning {
		t.Errorf("Expected to return to PLANNING state, got %s", returnState)
	}

	// Verify context preservation worked
	finalStateData := driver.GetStateData()
	if findings, exists := finalStateData["exploration_findings"]; !exists || findings == nil {
		t.Error("Expected exploration findings to be preserved after question answered")
	}

	t.Log("Context preservation across QUESTION transition successful")
}

// TestToolBasedQuestionFlow tests the new ask_question tool integration
func TestToolBasedQuestionFlow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "tool-question-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	// Create mock LLM client
	mockLLM := createMockLLMClient()

	driver, err := NewCoder("tool-test-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Test ask_question tool integration in PLANNING state
	task := "Implement OAuth2 authentication"
	if err := driver.ProcessTask(ctx, task); err != nil {
		t.Fatalf("Failed to process task: %v", err)
	}

	// Simulate ask_question tool call result
	questionData := map[string]interface{}{
		"question": "Which OAuth2 flow should I implement?",
		"context":  "Found existing session-based auth, need to know if we're adding or replacing",
		"urgency":  "MEDIUM",
	}

	driver.SetStateData("question_submitted", questionData)

	// Process the planning state with question
	nextState, _, err := driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process planning with question: %v", err)
	}

	// Should transition to QUESTION state
	if nextState != StateQuestion {
		t.Errorf("Expected transition to QUESTION state, got %s", nextState)
	}

	// Verify question data was processed correctly
	stateData := driver.GetStateData()
	if content, exists := stateData["question_content"]; !exists || content != questionData["question"] {
		t.Errorf("Expected question content to be preserved, got %v", content)
	}

	if reason, exists := stateData["question_reason"]; !exists {
		t.Error("Expected question reason to be set")
	} else if reasonStr, ok := reason.(string); !ok || reasonStr == "" {
		t.Error("Expected question reason to be non-empty string")
	}

	// Test question handling in CODING state
	driver.SetStateData("task_content", task)
	driver.SetStateData("plan", "Mock plan for OAuth2")
	if err := driver.TransitionTo(ctx, StateCoding, nil); err != nil {
		t.Fatalf("Failed to transition to CODING: %v", err)
	}

	// Simulate ask_question from CODING state
	codingQuestionData := map[string]interface{}{
		"question": "Should I use PKCE for the authorization code flow?",
		"context":  "Implementing authorization code flow, need security best practices",
		"urgency":  "HIGH",
	}

	driver.SetStateData("question_submitted", codingQuestionData)

	// Process CODING state with question
	codingNextState, _, err := driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process coding with question: %v", err)
	}

	// Should transition to QUESTION state from CODING
	if codingNextState != StateQuestion {
		t.Errorf("Expected transition to QUESTION from CODING, got %s", codingNextState)
	}

	// Verify question origin is tracked correctly
	codingStateData := driver.GetStateData()
	if origin, exists := codingStateData["question_origin"]; !exists || origin != string(StateCoding) {
		t.Errorf("Expected question_origin to be CODING, got %v", origin)
	}

	// Test question handling in FIXING state
	driver.SetStateData("fixing_reason", "test_failure")
	if err := driver.TransitionTo(ctx, StateFixing, nil); err != nil {
		t.Fatalf("Failed to transition to FIXING: %v", err)
	}

	// Simulate ask_question from FIXING state
	fixingQuestionData := map[string]interface{}{
		"question": "How should I handle the token refresh failure in tests?",
		"context":  "Tests failing due to expired tokens, need guidance on mock strategy",
		"urgency":  "LOW",
	}

	driver.SetStateData("question_submitted", fixingQuestionData)

	// Process FIXING state with question
	fixingNextState, _, err := driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process fixing with question: %v", err)
	}

	// Should transition to QUESTION state from FIXING
	if fixingNextState != StateQuestion {
		t.Errorf("Expected transition to QUESTION from FIXING, got %s", fixingNextState)
	}

	// Verify question origin is tracked correctly
	fixingStateData := driver.GetStateData()
	if origin, exists := fixingStateData["question_origin"]; !exists || origin != string(StateFixing) {
		t.Errorf("Expected question_origin to be FIXING, got %v", origin)
	}

	t.Log("Tool-based question flow tested successfully across all states")
}

// TestEnhancedPlanningErrorHandling tests error scenarios in enhanced planning
func TestEnhancedPlanningErrorHandling(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "planning-error-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	// Create mock LLM client
	mockLLM := createMockLLMClient()

	driver, err := NewCoder("error-test-coder", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Test invalid question data format
	task := "Create API endpoint"
	if err := driver.ProcessTask(ctx, task); err != nil {
		t.Fatalf("Failed to process task: %v", err)
	}

	// Set invalid question data
	driver.SetStateData("question_submitted", "invalid-format")

	// Should handle invalid question data gracefully
	_, _, err = driver.ProcessState(ctx)
	if err == nil {
		t.Error("Expected error when processing invalid question data")
	}

	// Test missing required question fields
	invalidQuestionData := map[string]interface{}{
		"context": "Some context but no question",
		"urgency": "HIGH",
	}

	driver.SetStateData("question_submitted", invalidQuestionData)

	// Should handle missing question field
	_, _, err = driver.ProcessState(ctx)
	if err == nil {
		t.Error("Expected error when question field is missing")
	}

	// Test valid recovery after error
	validQuestionData := map[string]interface{}{
		"question": "Valid question after error?",
		"context":  "Recovery test context",
		"urgency":  "LOW",
	}

	driver.SetStateData("question_submitted", validQuestionData)

	// Should process successfully now
	nextState, _, err := driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process valid question after error: %v", err)
	}

	if nextState != StateQuestion {
		t.Errorf("Expected successful transition to QUESTION, got %s", nextState)
	}

	t.Log("Error handling in enhanced planning works correctly")
}
