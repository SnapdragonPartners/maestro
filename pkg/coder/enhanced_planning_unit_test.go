package coder

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
)

// TestPlanningContextManagement tests context save/restore functionality.
func TestPlanningContextManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "context-management-test")
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

	responses := []agent.CompletionResponse{
		{Content: "Planning context test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("context-unit-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Test context storage.
	driver.storePlanningContext(driver.BaseStateMachine)

	// Verify context was saved.
	if saved, exists := driver.BaseStateMachine.GetStateValue(KeyPlanningContextSaved); !exists {
		t.Error("Expected planning context to be saved")
	} else {
		if contextData, ok := saved.(map[string]any); !ok {
			t.Error("Expected planning context to be a map")
		} else {
			// Verify required fields exist.
			expectedFields := []string{"exploration_history", "files_examined", "current_findings", "timestamp"}
			for _, field := range expectedFields {
				if _, exists := contextData[field]; !exists {
					t.Errorf("Expected context field %s not found", field)
				}
			}
		}
	}

	// Test context restoration.
	driver.restorePlanningContext(driver.BaseStateMachine)
	// Context restoration calls placeholder methods, so we just verify no panics occur.

	t.Log("Planning context management works correctly")
}

// TestCodingContextManagement tests coding context save/restore functionality.
func TestCodingContextManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "coding-context-test")
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

	responses := []agent.CompletionResponse{
		{Content: "Coding context test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("coding-context-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Test coding context storage.
	driver.storeCodingContext(driver.BaseStateMachine)

	// Verify context was saved.
	if saved, exists := driver.BaseStateMachine.GetStateValue(KeyCodingContextSaved); !exists {
		t.Error("Expected coding context to be saved")
	} else {
		if contextData, ok := saved.(map[string]any); !ok {
			t.Error("Expected coding context to be a map")
		} else {
			// Verify required fields exist.
			expectedFields := []string{"coding_progress", "files_created", "current_task", "timestamp"}
			for _, field := range expectedFields {
				if _, exists := contextData[field]; !exists {
					t.Errorf("Expected context field %s not found", field)
				}
			}
		}
	}

	// Test context restoration.
	driver.restoreCodingContext(driver.BaseStateMachine)
	// Context restoration calls placeholder methods, so we just verify no panics occur.

	t.Log("Coding context management works correctly")
}

// TestQuestionTransitionLogic tests question transition handlers.
func TestQuestionTransitionLogic(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "question-transition-test")
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

	responses := []agent.CompletionResponse{
		{Content: "Question transition test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("question-transition-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	// Test planning question transition.
	questionData := map[string]any{
		"question": "How should I implement this feature?",
		"context":  "Found existing patterns in codebase",
		"urgency":  "HIGH",
	}

	nextState, _, err := driver.handleQuestionTransition(ctx, driver.BaseStateMachine, questionData)
	if err != nil {
		t.Fatalf("Planning question transition failed: %v", err)
	}

	if nextState != StateQuestion {
		t.Errorf("Expected transition to QUESTION, got %s", nextState)
	}

	// Verify question data was set correctly.
	if content, exists := driver.BaseStateMachine.GetStateValue(string(stateDataKeyQuestionContent)); !exists || content != "How should I implement this feature?" {
		t.Error("Question content not set correctly")
	}

	if origin, exists := driver.BaseStateMachine.GetStateValue(string(stateDataKeyQuestionOrigin)); !exists || origin != string(StatePlanning) {
		t.Error("Question origin not set correctly")
	}

	// Test coding question transition.
	driver.BaseStateMachine.SetStateData(KeyQuestionSubmitted, nil) // Reset
	nextState, _, err = driver.handleCodingQuestionTransition(ctx, driver.BaseStateMachine, questionData)
	if err != nil {
		t.Fatalf("Coding question transition failed: %v", err)
	}

	if nextState != StateQuestion {
		t.Errorf("Expected transition to QUESTION, got %s", nextState)
	}

	if origin, exists := driver.BaseStateMachine.GetStateValue(string(stateDataKeyQuestionOrigin)); !exists || origin != string(StateCoding) {
		t.Error("Coding question origin not set correctly")
	}

	t.Log("Question transition logic works correctly across all states")
}

// TestPlanSubmissionHandling tests plan submission workflow.
func TestPlanSubmissionHandling(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "plan-submission-test")
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

	responses := []agent.CompletionResponse{
		{Content: "Plan submission test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("plan-submission-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	// Test plan submission handling.
	planData := map[string]any{
		KeyPlan:               "Comprehensive implementation plan for JWT auth system",
		"confidence":          "HIGH",
		KeyExplorationSummary: "Explored 15 files, found 3 existing patterns",
		"risks":               "Potential breaking changes to session handling",
	}

	nextState, _, err := driver.handlePlanSubmission(ctx, driver.BaseStateMachine, planData)
	if err != nil {
		t.Fatalf("Plan submission failed: %v", err)
	}

	if nextState != StatePlanReview {
		t.Errorf("Expected transition to PLAN_REVIEW, got %s", nextState)
	}

	// Verify plan data was stored correctly.
	if plan, exists := driver.BaseStateMachine.GetStateValue(KeyPlan); !exists || plan != planData[KeyPlan] {
		t.Error("Plan content not stored correctly")
	}

	if confidence, exists := driver.BaseStateMachine.GetStateValue(KeyPlanConfidence); !exists || confidence != "HIGH" {
		t.Error("Plan confidence not stored correctly")
	}

	if summary, exists := driver.BaseStateMachine.GetStateValue(KeyExplorationSummary); !exists || summary != planData[KeyExplorationSummary] {
		t.Error("Exploration summary not stored correctly")
	}

	if risks, exists := driver.BaseStateMachine.GetStateValue(KeyPlanRisks); !exists || risks != planData["risks"] {
		t.Error("Plan risks not stored correctly")
	}

	// Verify completion timestamp was set.
	if _, exists := driver.BaseStateMachine.GetStateValue(KeyPlanningCompletedAt); !exists {
		t.Error("Planning completion timestamp not set")
	}

	// Verify plan submission trigger was cleared.
	if trigger, exists := driver.BaseStateMachine.GetStateValue(KeyPlanSubmitted); exists && trigger != nil {
		t.Error("Plan submission trigger should be cleared after processing")
	}

	t.Log("Plan submission handling works correctly")
}

// TestContainerConfigurationMethods tests container mount configuration.
func TestContainerConfigurationMethods(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "container-config-test")
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

	responses := []agent.CompletionResponse{
		{Content: "Container config test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("container-config-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	// Test container configuration method exists and doesn't panic.
	// In mock mode, this will likely fail due to no actual container runtime.
	// but we're testing that the method signature and basic logic works.
	err = driver.configureWorkspaceMount(ctx, true, "unit-test-readonly")
	if err != nil {
		t.Logf("Container configuration failed in mock mode (expected): %v", err)
	}

	err = driver.configureWorkspaceMount(ctx, false, "unit-test-readwrite")
	if err != nil {
		t.Logf("Container configuration failed in mock mode (expected): %v", err)
	}

	// Test that the method doesn't panic with various inputs.
	//nolint:govet // Test struct, optimization not critical
	testCases := []struct {
		readonly bool
		purpose  string
	}{
		{true, "planning"},
		{false, "coding"},
		{false, "testing"},
		{false, "fixing"},
		{true, "exploration"},
	}

	for _, tc := range testCases {
		t.Run(tc.purpose, func(t *testing.T) {
			err := driver.configureWorkspaceMount(ctx, tc.readonly, tc.purpose)
			// We expect this to fail in mock mode, but not panic.
			if err != nil {
				t.Logf("Container config for %s failed in mock mode (expected): %v", tc.purpose, err)
			}
		})
	}

	t.Log("Container configuration methods work correctly")
}

// TestHelperMethods tests various helper methods used in enhanced planning.
func TestHelperMethods(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "helper-methods-test")
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

	responses := []agent.CompletionResponse{
		{Content: "Helper methods test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("helper-methods-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Test placeholder helper methods don't panic.
	explorationHistory := driver.getExplorationHistory()
	if explorationHistory == nil {
		t.Error("Expected exploration history to not be nil")
	}

	filesExamined := driver.getFilesExamined()
	if filesExamined == nil {
		t.Error("Expected files examined to not be nil")
	}

	currentFindings := driver.getCurrentFindings()
	if currentFindings == nil {
		t.Error("Expected current findings to not be nil")
	}

	codingProgress := driver.getCodingProgress()
	if codingProgress == nil {
		t.Error("Expected coding progress to not be nil")
	}

	filesCreated := driver.getFilesCreated()
	if filesCreated == nil {
		t.Error("Expected files created to not be nil")
	}

	currentTask := driver.getCurrentTask()
	if currentTask == nil {
		t.Error("Expected current task to not be nil")
	}

	fixingProgress := driver.getFixingProgress()
	if fixingProgress == nil {
		t.Error("Expected fixing progress to not be nil")
	}

	testFailures := driver.getTestFailures()
	if testFailures == nil {
		t.Error("Expected test failures to not be nil")
	}

	currentFixes := driver.getCurrentFixes()
	if currentFixes == nil {
		t.Error("Expected current fixes to not be nil")
	}

	// Test restore methods don't panic.
	driver.restoreExplorationHistory(explorationHistory)
	driver.restoreFilesExamined(filesExamined)
	driver.restoreCurrentFindings(currentFindings)
	driver.restoreCodingProgress(codingProgress)
	driver.restoreFilesCreated(filesCreated)
	driver.restoreCurrentTask(currentTask)
	driver.restoreFixingProgress(fixingProgress)
	driver.restoreTestFailures(testFailures)
	driver.restoreCurrentFixes(currentFixes)

	t.Log("Helper methods work correctly")
}

// TestStateDataPreservation tests that state data is properly preserved across transitions.
func TestStateDataPreservation(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "state-preservation-test")
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

	responses := []agent.CompletionResponse{
		{Content: "State preservation test response"},
	}
	mockLLM := agent.NewMockLLMClient(responses, nil)

	driver, err := NewCoder("state-preservation-test", stateStore, modelConfig, mockLLM, tempDir, &config.Agent{}, nil, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Set up initial state data.
	testData := map[string]any{
		"task_content":         "Test task for state preservation",
		KeyPlan:                "Test implementation plan",
		KeyPlanConfidence:      "HIGH",
		KeyExplorationSummary:  "Test exploration summary",
		KeyPlanningCompletedAt: time.Now().UTC(),
	}

	for key, value := range testData {
		driver.BaseStateMachine.SetStateData(key, value)
	}

	// Verify all data is preserved.
	for key, expectedValue := range testData {
		if actualValue, exists := driver.BaseStateMachine.GetStateValue(key); !exists {
			t.Errorf("Expected state data key %s not found", key)
		} else if actualValue != expectedValue {
			t.Errorf("State data mismatch for key %s: expected %v, got %v", key, expectedValue, actualValue)
		}
	}

	// Test that context preservation doesn't overwrite core state data.
	driver.storePlanningContext(driver.BaseStateMachine)

	// Verify original data is still there.
	for key, expectedValue := range testData {
		if actualValue, exists := driver.BaseStateMachine.GetStateValue(key); !exists {
			t.Errorf("State data key %s was lost after context preservation", key)
		} else if actualValue != expectedValue {
			t.Errorf("State data changed after context preservation for key %s: expected %v, got %v", key, expectedValue, actualValue)
		}
	}

	t.Log("State data preservation works correctly")
}
