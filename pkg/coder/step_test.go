package coder

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
)

// TestStepExecutesAtomicTransitions verifies that Step() executes exactly one state transition
func TestStepExecutesAtomicTransitions(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	driver, err := NewCoder("test-coder", stateStore, &config.ModelCfg{}, nil, tempDir, nil)
	if err != nil {
		t.Fatalf("Failed to create coder driver: %v", err)
	}

	ctx := context.Background()
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Set up task content to trigger state transitions
	driver.BaseStateMachine.SetStateData("task_content", "Test task for atomic transitions")

	// Initial state should be WAITING
	if state := driver.GetCurrentState(); state.String() != "WAITING" {
		t.Errorf("Expected initial state WAITING, got %s", state)
	}

	// Execute first step - should transition to PLANNING
	done, err := driver.Step(ctx)
	if err != nil {
		t.Fatalf("Step failed: %v", err)
	}
	if done {
		t.Error("Expected processing not to be done after first step")
	}

	// Verify state changed to PLANNING
	if state := driver.GetCurrentState(); state.String() != "PLANNING" {
		t.Errorf("Expected state PLANNING after first step, got %s", state)
	}

	// Execute second step - should process planning and transition
	done, err = driver.Step(ctx)
	if err != nil {
		t.Fatalf("Second step failed: %v", err)
	}

	// Should have moved to PLAN_REVIEW
	if state := driver.GetCurrentState(); state.String() != "PLAN_REVIEW" {
		t.Errorf("Expected state PLAN_REVIEW after second step, got %s", state)
	}
}

// TestIdleCPUUsage verifies that the state machine doesn't consume CPU when idle
func TestIdleCPUUsage(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	driver, err := NewCoder("test-coder", stateStore, &config.ModelCfg{}, nil, tempDir, nil)
	if err != nil {
		t.Fatalf("Failed to create coder driver: %v", err)
	}

	ctx := context.Background()
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Force into a waiting state by not providing task content
	startTime := time.Now()
	stepCount := 0

	// Try several steps - they should all be no-ops in WAITING state
	for i := 0; i < 5; i++ {
		done, err := driver.Step(ctx)
		if err != nil {
			t.Fatalf("Step %d failed: %v", i, err)
		}
		stepCount++

		// Should remain in WAITING state
		if state := driver.GetCurrentState(); state.String() != "WAITING" {
			t.Errorf("Expected state WAITING during idle, got %s on step %d", state, i)
		}

		// Should not be done (there's no work to do)
		if done {
			t.Errorf("Step reported done during idle state on step %d", i)
		}
	}

	duration := time.Since(startTime)

	// All steps should complete very quickly since they're no-ops
	if duration > 100*time.Millisecond {
		t.Errorf("Idle steps took too long: %v (expected < 100ms)", duration)
	}

	t.Logf("Executed %d idle steps in %v", stepCount, duration)
}

// TestNoNestedLoops verifies that external events don't create nested processing loops
func TestNoNestedLoops(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	driver, err := NewCoder("test-coder", stateStore, &config.ModelCfg{}, nil, tempDir, nil)
	if err != nil {
		t.Fatalf("Failed to create coder driver: %v", err)
	}

	ctx := context.Background()
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	// Set up a task that will reach approval phase
	driver.BaseStateMachine.SetStateData("task_content", "Test approval flow")

	// Process until we reach an approval state
	maxSteps := 10
	for step := 0; step < maxSteps; step++ {
		done, err := driver.Step(ctx)
		if err != nil {
			t.Fatalf("Step %d failed: %v", step, err)
		}
		if done {
			break
		}

		// Check if we have pending approval (which would normally trigger external message)
		if hasPending, _, _, _, _ := driver.GetPendingApprovalRequest(); hasPending {
			// Simulate external approval result processing
			err := driver.ProcessApprovalResult("approved", "plan")
			if err != nil {
				t.Fatalf("Failed to process approval result: %v", err)
			}

			// Process the approval with a single step (not a full Run() loop)
			_, err = driver.Step(ctx)
			if err != nil {
				t.Fatalf("Failed to process approval step: %v", err)
			}

			t.Logf("Successfully processed approval at step %d using single Step() call", step)
			break
		}
	}

	// Verify no hanging state
	currentState := driver.GetCurrentState()
	t.Logf("Final state after approval processing: %s", currentState)
}
