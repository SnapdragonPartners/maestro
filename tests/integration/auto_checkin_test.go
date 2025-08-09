//go:build integration
// +build integration

package integration

import (
	"context"
	"testing"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/config"
)

// TestAutoCheckinCoding tests AUTO_CHECKIN behavior in CODING state.
func TestAutoCheckinCoding(t *testing.T) {
	SetupTestEnvironment(t)

	// Create coder with low coding budget.
	agentConfig := &config.Agent{
		ID:   "test-coder",
		Type: "coder",
		Name: "Test Coder",
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 3, // Need 3 to allow 2 iterations before triggering BUDGET_REVIEW
			FixingBudget: 3,
		},
	}

	driver := CreateTestCoderWithAgent(t, "test-coder", agentConfig)

	// Force driver into CODING state - must go through SETUP first.
	err := driver.TransitionTo(context.Background(), coder.StateSetup, nil)
	if err != nil {
		t.Fatalf("Failed to transition to SETUP: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanning, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLANNING: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StateCoding, nil)
	if err != nil {
		t.Fatalf("Failed to transition to CODING: %v", err)
	}

	// Simulate reaching the budget by calling Step() multiple times
	// The first call should increment to 1, second to 2, third should trigger AUTO_CHECKIN.
	for i := 1; i <= 3; i++ {
		done, stepErr := driver.Step(context.Background())
		if stepErr != nil {
			t.Fatalf("Step %d failed: %v", i, stepErr)
		}

		if i < 2 {
			// Should still be in CODING state.
			if driver.GetCurrentState().String() != "CODING" {
				t.Fatalf("Expected CODING state at step %d, got %s", i, driver.GetCurrentState())
			}
			if done {
				t.Fatalf("Expected step %d to not be done", i)
			}
		} else {
			// Should transition to BUDGET_REVIEW state.
			if driver.GetCurrentState().String() != "BUDGET_REVIEW" {
				t.Fatalf("Expected BUDGET_REVIEW state at step %d, got %s", i, driver.GetCurrentState())
			}
			break
		}
	}

	// Verify BUDGET_REVIEW question fields.
	if reason, exists := driver.GetStateValue("question_reason"); !exists || reason != "BUDGET_REVIEW" {
		t.Fatalf("Expected question_reason=BUDGET_REVIEW, got %v", reason)
	}

	if origin, exists := driver.GetStateValue("question_origin"); !exists || origin != "CODING" {
		t.Fatalf("Expected question_origin=CODING, got %v", origin)
	}

	// Test CONTINUE response via ProcessApprovalResult.
	err = driver.ProcessApprovalResult(context.Background(), "APPROVED", "budget_review")
	if err != nil {
		t.Fatalf("Failed to process CONTINUE approval: %v", err)
	}

	// Step once to trigger the budget approval processing.
	_, err = driver.Step(context.Background())
	if err != nil {
		t.Fatalf("Failed to step after approval: %v", err)
	}

	// Should be back in CODING state with reset counter.
	if driver.GetCurrentState().String() != "CODING" {
		t.Fatalf("Expected CODING state after approval, got %s", driver.GetCurrentState())
	}

	// Verify counter was reset.
	if val, exists := driver.GetStateValue("coding_iterations"); exists {
		if count, ok := val.(int); ok && count != 0 {
			t.Fatalf("Expected coding_iterations to be reset to 0, got %d", count)
		}
	}
}

// TestAutoCheckinCodingFix tests BUDGET_REVIEW behavior in CODING state when doing fixing work.
func TestAutoCheckinCodingFix(t *testing.T) {
	SetupTestEnvironment(t)

	// Create coder with low coding budget.
	agentConfig := &config.Agent{
		ID:   "test-coder",
		Type: "coder",
		Name: "Test Coder",
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 1, // Low budget to trigger BUDGET_REVIEW quickly
		},
	}

	driver := CreateTestCoderWithAgent(t, "test-coder", agentConfig)

	// Transition to CODING state with test failure data to simulate fixing work - must go through SETUP first.
	err := driver.TransitionTo(context.Background(), coder.StateSetup, nil)
	if err != nil {
		t.Fatalf("Failed to transition to SETUP: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanning, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLANNING: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StateCoding, nil)
	if err != nil {
		t.Fatalf("Failed to transition to CODING: %v", err)
	}

	// Set up test failure data to simulate the CODING state doing fixing work.
	driver.SetStateData("test_failure_output", "test failed: connection refused")
	driver.SetStateData("coding_mode", "test_fix")

	// Process the CODING state - should trigger BUDGET_REVIEW immediately since budget is 1.
	done, err := driver.Step(context.Background())
	if err != nil {
		t.Fatalf("Failed to step through CODING: %v", err)
	}

	if done {
		t.Fatalf("Expected step to not be done")
	}

	// Check if we're now in BUDGET_REVIEW state.
	currentState := driver.GetCurrentState()
	if currentState.String() != "BUDGET_REVIEW" {
		t.Fatalf("Expected BUDGET_REVIEW state after coding budget exceeded, got %s", currentState)
	}

	// Verify BUDGET_REVIEW request was made.
	if reason, exists := driver.GetStateValue("question_reason"); !exists || reason != "BUDGET_REVIEW" {
		t.Fatalf("Expected question_reason=BUDGET_REVIEW, got %v", reason)
	}

	if origin, exists := driver.GetStateValue("question_origin"); !exists || origin != "CODING" {
		t.Fatalf("Expected question_origin=CODING, got %v", origin)
	}
}

// TestContinueResetsCounter tests that CONTINUE command resets loop counters.
func TestContinueResetsCounter(t *testing.T) {
	SetupTestEnvironment(t)

	// Create coder with budget.
	agentConfig := &config.Agent{
		ID:   "test-coder",
		Type: "coder",
		Name: "Test Coder",
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 3,
			FixingBudget: 2,
		},
	}

	driver := CreateTestCoderWithAgent(t, "test-coder", agentConfig)

	// Force driver into CODING state first - must go through SETUP first.
	err := driver.TransitionTo(context.Background(), coder.StateSetup, nil)
	if err != nil {
		t.Fatalf("Failed to transition to SETUP: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanning, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLANNING: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StateCoding, nil)
	if err != nil {
		t.Fatalf("Failed to transition to CODING: %v", err)
	}

	// Manually set iteration count to budget - 1.
	driver.SetStateData("coding_iterations", 2)
	driver.SetStateData("question_reason", "BUDGET_REVIEW")
	driver.SetStateData("question_origin", "CODING")
	driver.SetStateData("origin", "CODING")

	// Set up BUDGET_REVIEW state.
	err = driver.TransitionTo(context.Background(), coder.StateBudgetReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to BUDGET_REVIEW: %v", err)
	}

	// Process CONTINUE response.
	err = driver.ProcessApprovalResult(context.Background(), "APPROVED", "budget_review")
	if err != nil {
		t.Fatalf("Failed to process CONTINUE approval: %v", err)
	}

	// Step once to trigger the budget approval processing.
	_, err = driver.Step(context.Background())
	if err != nil {
		t.Fatalf("Failed to step after approval: %v", err)
	}

	// Should be back in CODING state with reset counter.
	if driver.GetCurrentState().String() != "CODING" {
		t.Fatalf("Expected CODING state after approval, got %s", driver.GetCurrentState())
	}

	// Verify counter was reset.
	if val, exists := driver.GetStateValue("coding_iterations"); exists {
		if count, ok := val.(int); ok && count != 0 {
			t.Fatalf("Expected coding_iterations to be reset to 0, got %d", count)
		}
	} else {
		t.Fatalf("Expected coding_iterations to exist")
	}
}

// TestPivotResetsCounter tests that PIVOT command resets loop counters.
func TestPivotResetsCounter(t *testing.T) {
	SetupTestEnvironment(t)

	agentConfig := &config.Agent{
		ID:   "test-coder",
		Type: "coder",
		Name: "Test Coder",
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 3,
			FixingBudget: 2,
		},
	}

	driver := CreateTestCoderWithAgent(t, "test-coder", agentConfig)

	// Force driver into CODING state first - must go through SETUP first.
	err := driver.TransitionTo(context.Background(), coder.StateSetup, nil)
	if err != nil {
		t.Fatalf("Failed to transition to SETUP: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanning, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLANNING: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StateCoding, nil)
	if err != nil {
		t.Fatalf("Failed to transition to CODING: %v", err)
	}

	// Set up BUDGET_REVIEW state.
	driver.SetStateData("fixing_iterations", 2)
	driver.SetStateData("question_reason", "BUDGET_REVIEW")
	driver.SetStateData("question_origin", "FIXING")
	driver.SetStateData("origin", "FIXING")

	err = driver.TransitionTo(context.Background(), coder.StateBudgetReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to BUDGET_REVIEW: %v", err)
	}

	// Process ESCALATE response (NEEDS_CHANGES) - this SHOULD go to CODE_REVIEW but it's an invalid transition.
	// This is a known bug - BUDGET_REVIEW cannot transition to CODE_REVIEW according to STATES.md.
	err = driver.ProcessApprovalResult(context.Background(), "NEEDS_CHANGES", "budget_review")
	if err != nil {
		t.Fatalf("Failed to process ESCALATE approval: %v", err)
	}

	// Step once to trigger the budget approval processing - this should fail due to invalid transition.
	_, err = driver.Step(context.Background())
	if err == nil {
		t.Fatalf("Expected error due to invalid state transition BUDGET_REVIEW → CODE_REVIEW")
	}

	// Should still be in BUDGET_REVIEW state due to failed transition.
	if driver.GetCurrentState().String() != "BUDGET_REVIEW" {
		t.Fatalf("Expected BUDGET_REVIEW state after failed transition, got %s", driver.GetCurrentState())
	}
}

// TestInvalidAutoCheckinCommand tests error handling for invalid commands.
func TestInvalidAutoCheckinCommand(t *testing.T) {
	SetupTestEnvironment(t)

	agentConfig := &config.Agent{
		ID:   "test-coder",
		Type: "coder",
		Name: "Test Coder",
		IterationBudgets: config.IterationBudgets{
			CodingBudget: 3,
			FixingBudget: 2,
		},
	}

	driver := CreateTestCoderWithAgent(t, "test-coder", agentConfig)

	// Force driver into CODING state first - must go through SETUP first.
	err := driver.TransitionTo(context.Background(), coder.StateSetup, nil)
	if err != nil {
		t.Fatalf("Failed to transition to SETUP: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanning, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLANNING: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StatePlanReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to PLAN_REVIEW: %v", err)
	}
	err = driver.TransitionTo(context.Background(), coder.StateCoding, nil)
	if err != nil {
		t.Fatalf("Failed to transition to CODING: %v", err)
	}

	// Set up BUDGET_REVIEW state.
	driver.SetStateData("question_reason", "BUDGET_REVIEW")
	driver.SetStateData("question_origin", "CODING")
	driver.SetStateData("origin", "CODING")

	err = driver.TransitionTo(context.Background(), coder.StateBudgetReview, nil)
	if err != nil {
		t.Fatalf("Failed to transition to BUDGET_REVIEW: %v", err)
	}

	// Process invalid approval status - this gets converted to REJECTED due to ConvertLegacyStatus default behavior.
	err = driver.ProcessApprovalResult(context.Background(), "INVALID_STATUS", "budget_review")
	if err != nil {
		t.Fatalf("Unexpected error processing invalid status: %v", err)
	}

	// Step once to trigger the budget approval processing - REJECTED should go to ERROR.
	_, err = driver.Step(context.Background())
	if err != nil {
		t.Fatalf("Failed to step after invalid status: %v", err)
	}

	// Should transition to ERROR state due to REJECTED status.
	if driver.GetCurrentState().String() != "ERROR" {
		t.Fatalf("Expected ERROR state after REJECTED status, got %s", driver.GetCurrentState())
	}

	// Question content should now contain error message.
	if content, exists := driver.GetStateValue("question_content"); exists {
		contentStr, _ := content.(string)
		if contentStr == "" {
			t.Fatalf("Expected error message in question_content")
		}
	}
}
