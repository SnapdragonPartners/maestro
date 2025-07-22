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
			CodingBudget: 2, // Low budget to trigger AUTO_CHECKIN quickly
			FixingBudget: 3,
		},
	}

	driver := CreateTestCoderWithAgent(t, "test-coder", agentConfig)

	// Force driver into CODING state.
	err := driver.TransitionTo(context.Background(), coder.StateCoding, nil)
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
			// Should transition to QUESTION state.
			if driver.GetCurrentState().String() != "QUESTION" {
				t.Fatalf("Expected QUESTION state at step %d, got %s", i, driver.GetCurrentState())
			}
			break
		}
	}

	// Verify AUTO_CHECKIN question fields.
	if reason, exists := driver.GetStateValue("question_reason"); !exists || reason != "AUTO_CHECKIN" {
		t.Fatalf("Expected question_reason=AUTO_CHECKIN, got %v", reason)
	}

	if origin, exists := driver.GetStateValue("question_origin"); !exists || origin != "CODING" {
		t.Fatalf("Expected question_origin=CODING, got %v", origin)
	}

	// Test CONTINUE response.
	err = driver.ProcessAnswer("CONTINUE 5")
	if err != nil {
		t.Fatalf("Failed to process CONTINUE answer: %v", err)
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

	// Transition to CODING state with test failure data to simulate fixing work.
	err := driver.TransitionTo(context.Background(), coder.StateCoding, nil)
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

	// Manually set iteration count to budget - 1.
	driver.SetStateData("coding_iterations", 2)
	driver.SetStateData("question_reason", "AUTO_CHECKIN")
	driver.SetStateData("question_origin", "CODING")

	// Process CONTINUE 2 answer.
	err := driver.ProcessAnswer("CONTINUE 2")
	if err != nil {
		t.Fatalf("Failed to process CONTINUE answer: %v", err)
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

	// Set up AUTO_CHECKIN state.
	driver.SetStateData("fixing_iterations", 2)
	driver.SetStateData("question_reason", "AUTO_CHECKIN")
	driver.SetStateData("question_origin", "FIXING")

	// Process PIVOT answer.
	err := driver.ProcessAnswer("PIVOT")
	if err != nil {
		t.Fatalf("Failed to process PIVOT answer: %v", err)
	}

	// Verify counter was reset.
	if val, exists := driver.GetStateValue("fixing_iterations"); exists {
		if count, ok := val.(int); ok && count != 0 {
			t.Fatalf("Expected fixing_iterations to be reset to 0, got %d", count)
		}
	} else {
		t.Fatalf("Expected fixing_iterations to exist")
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

	// Set up AUTO_CHECKIN state.
	driver.SetStateData("question_reason", "AUTO_CHECKIN")
	driver.SetStateData("question_origin", "CODING")

	// Process invalid command.
	err := driver.ProcessAnswer("INVALID_COMMAND")
	if err == nil {
		t.Fatalf("Expected error for invalid AUTO_CHECKIN command")
	}

	// Should still be in AUTO_CHECKIN state (question fields should remain)
	if reason, exists := driver.GetStateValue("question_reason"); !exists || reason != "AUTO_CHECKIN" {
		t.Fatalf("Expected to remain in AUTO_CHECKIN state after invalid command")
	}

	// Question content should now contain error message.
	if content, exists := driver.GetStateValue("question_content"); exists {
		contentStr, _ := content.(string)
		if contentStr == "" {
			t.Fatalf("Expected error message in question_content")
		}
	}
}
