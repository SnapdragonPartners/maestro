package coder

import (
	"context"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
)

// TestCoderHealthStoryIntegration tests the complete flow from PLANNING to DONE
func TestCoderHealthStoryIntegration(t *testing.T) {
	// Create temp directory
	tempDir, err := os.MkdirTemp("", "coder-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create state store
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create test config
	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	// Create driver in mock mode (no LLM client)
	driver, err := NewCoder("test-coder", stateStore, modelConfig, nil, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	// Initialize driver
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Process /health endpoint task
	healthTask := "Create a /health endpoint that returns JSON with status:ok and timestamp"
	if err := driver.ProcessTask(ctx, healthTask); err != nil {
		t.Fatalf("Failed to process health task: %v", err)
	}

	// Verify final state is DONE
	finalState := driver.GetCurrentState()
	if finalState != proto.StateDone {
		// Get state data for debugging
		stateData := driver.GetStateData()
		t.Errorf("Expected final state to be DONE, got %s. State data: %+v", finalState, stateData)
	}

	// Verify state data contains expected values
	stateData := driver.GetStateData()

	// Check that planning was completed
	if _, exists := stateData["planning_completed_at"]; !exists {
		t.Error("Expected planning_completed_at to be set")
	}

	// Check that coding was completed
	if _, exists := stateData["coding_completed_at"]; !exists {
		t.Error("Expected coding_completed_at to be set")
	}

	// Check that testing was completed
	if _, exists := stateData["testing_completed_at"]; !exists {
		t.Error("Expected testing_completed_at to be set")
	}

	// Check that code review was completed
	if _, exists := stateData["code_review_completed_at"]; !exists {
		t.Error("Expected code_review_completed_at to be set")
	}

	// Verify the task content is preserved
	if taskContent, exists := stateData["task_content"]; !exists || taskContent != healthTask {
		t.Errorf("Expected task_content to be preserved, got %v", taskContent)
	}
}

// TestCoderQuestionFlow tests the QUESTION state with origin tracking
func TestCoderQuestionFlow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "coder-test")
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

	driver, err := NewCoder("test-coder", stateStore, modelConfig, nil, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Process task that triggers a question
	questionTask := "I need help understanding this unclear requirement"
	if err := driver.ProcessTask(ctx, questionTask); err != nil {
		t.Fatalf("Failed to process question task: %v", err)
	}

	// Should be in QUESTION state
	currentState := driver.GetCurrentState()
	if currentState != StateQuestion {
		t.Errorf("Expected state to be QUESTION, got %s", currentState)
	}

	// Check that question data is set correctly
	stateData := driver.GetStateData()
	if origin, exists := stateData["question_origin"]; !exists || origin != string(StatePlanning) {
		t.Errorf("Expected question_origin to be PLANNING, got %v", origin)
	}

	// Simulate architect answer
	if err := driver.ProcessAnswer("Here's the clarification you need..."); err != nil {
		t.Fatalf("Failed to process answer: %v", err)
	}

	// Continue processing
	if err := driver.Run(ctx); err != nil {
		t.Fatalf("Failed to continue processing after answer: %v", err)
	}

	// Should have returned to PLANNING and then progressed
	finalState := driver.GetCurrentState()
	if finalState == StateQuestion {
		t.Error("Should have moved out of QUESTION state after receiving answer")
	}
}

// TestCoderApprovalFlow tests the REQUEST→RESULT flow for approvals
func TestCoderApprovalFlow(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "coder-test")
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

	driver, err := NewCoder("test-coder", stateStore, modelConfig, nil, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()

	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Manually set state to PLAN_REVIEW to test approval flow
	driver.SetStateData("task_content", "Create API endpoint")
	driver.SetStateData("plan", "Mock plan: Create REST API with proper error handling")
	if err := driver.TransitionTo(ctx, StatePlanReview, nil); err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}

	// Process the state (should create pending approval request)
	_, _, err = driver.ProcessState(ctx)
	if err != nil {
		t.Fatalf("Failed to process PLAN_REVIEW state: %v", err)
	}

	// Check that pending approval request exists
	hasPending, approvalID, content, reason, approvalType := driver.GetPendingApprovalRequest()
	if !hasPending {
		t.Error("Expected pending approval request")
	}
	if content == "" || reason == "" {
		t.Error("Expected approval request to have content and reason")
	}
	if approvalID == "" {
		t.Error("Expected approval request to have correlation ID")
	}
	if approvalType != proto.ApprovalTypePlan {
		t.Errorf("Expected plan approval, got %s", approvalType)
	}

	// Simulate architect approval
	if err := driver.ProcessApprovalResult(proto.ApprovalStatusApproved.String(), proto.ApprovalTypePlan.String()); err != nil {
		t.Fatalf("Failed to process approval result: %v", err)
	}

	// Continue processing
	if err := driver.Run(ctx); err != nil {
		t.Fatalf("Failed to continue after approval: %v", err)
	}

	// Should have moved to CODING state
	currentState := driver.GetCurrentState()
	if currentState != StateCoding {
		t.Errorf("Expected state to be CODING after plan approval, got %s", currentState)
	}
}

// TestCoderFailureAndRetry tests failure scenarios and retry logic
func TestCoderFailureAndRetry(t *testing.T) {
	modelConfig := &config.ModelCfg{
		MaxContextTokens: 4096,
		MaxReplyTokens:   1024,
		CompactionBuffer: 512,
	}

	testCases := []struct {
		name        string
		taskContent string
		expectFlow  []proto.State
	}{
		{
			name:        "Test failure and fix cycle",
			taskContent: "Create endpoint that should test fail initially",
			expectFlow:  []proto.State{StatePlanning, StatePlanReview, StateCoding, StateTesting, StateCoding, StateTesting, StateCodeReview, proto.StateDone},
		},
		{
			name:        "Normal successful flow",
			taskContent: "Create simple endpoint that works",
			expectFlow:  []proto.State{StatePlanning, StatePlanReview, StateCoding, StateTesting, StateCodeReview, proto.StateDone},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "coder-test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tempDir)

			stateStore, err := state.NewStore(tempDir)
			if err != nil {
				t.Fatalf("Failed to create state store: %v", err)
			}

			driver, err := NewCoder("test-coder", stateStore, modelConfig, nil, tempDir, &config.Agent{}, nil)
			if err != nil {
				t.Fatalf("Failed to create driver: %v", err)
			}

			ctx := context.Background()
			if err := driver.Initialize(ctx); err != nil {
				t.Fatalf("Failed to initialize driver: %v", err)
			}

			var stateTrace []proto.State
			stateTrace = append(stateTrace, driver.GetCurrentState())

			// Process the task
			if err := driver.ProcessTask(ctx, tc.taskContent); err != nil {
				t.Fatalf("Failed to process task: %v", err)
			}

			// Track state progression with timeout
			timeout := time.After(30 * time.Second)
			ticker := time.NewTicker(100 * time.Millisecond)
			defer ticker.Stop()

			for {
				select {
				case <-timeout:
					t.Fatalf("Test timed out, final state: %s, trace: %v", driver.GetCurrentState(), stateTrace)
				case <-ticker.C:
					currentState := driver.GetCurrentState()
					if len(stateTrace) == 0 || stateTrace[len(stateTrace)-1] != currentState {
						stateTrace = append(stateTrace, currentState)
					}

					if currentState == proto.StateDone || currentState == proto.StateError {
						goto testComplete
					}
				}
			}

		testComplete:
			finalState := driver.GetCurrentState()
			if finalState != proto.StateDone {
				t.Errorf("Expected final state DONE, got %s. State trace: %v", finalState, stateTrace)
			}

			t.Logf("State progression for %s: %v", tc.name, stateTrace)
		})
	}
}

// TestCoderStateManagement tests the unified approval result management
func TestCoderStateManagement(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "coder-state-test")
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

	driver, err := NewCoder("test-coder", stateStore, modelConfig, nil, tempDir, &config.Agent{}, nil)
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	ctx := context.Background()
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Test approval result processing
	err = driver.ProcessApprovalResult(proto.ApprovalStatusApproved.String(), proto.ApprovalTypePlan.String())
	if err != nil {
		t.Fatalf("Failed to process approval result: %v", err)
	}

	// Verify approval result is stored correctly
	stateData := driver.GetStateData()
	if approvalData, exists := stateData["plan_approval_result"]; exists {
		if result, ok := approvalData.(*proto.ApprovalResult); ok {
			if result.Type != proto.ApprovalTypePlan {
				t.Errorf("Expected approval type 'plan', got %s", result.Type)
			}
			if result.Status != proto.ApprovalStatusApproved {
				t.Errorf("Expected approval status 'APPROVED', got %s", result.Status)
			}
			if result.ReviewedAt.IsZero() {
				t.Error("Approval result should have a timestamp")
			}
		} else {
			t.Error("Approval result should be of type *ApprovalResult")
		}
	} else {
		t.Error("Approval result should be stored in state data")
	}

	t.Log("Approval result management working correctly")
}
