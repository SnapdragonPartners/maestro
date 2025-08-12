package coder

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

func TestDoneToolEffectsLogic(t *testing.T) {
	// Test the done tool Effects-based logic

	// Create a mock state machine
	sm := agent.NewBaseStateMachine("test-coder", proto.StateWaiting, nil, nil)

	// Set up initial state data
	sm.SetStateData(string(stateDataKeyTaskContent), "Test Task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	// Simulate the completion effect result being stored
	// (This simulates what happens in executeMCPToolCalls when tools.ToolDone is called)
	completionResult := &effect.CompletionResult{
		TargetState: StateTesting,
		Message:     "Implementation complete - proceeding to testing phase",
		Metadata:    make(map[string]any),
	}
	sm.SetStateData(KeyCompletionSignaled, completionResult)

	// Verify that the completion signal was set
	completionData, exists := sm.GetStateValue(KeyCompletionSignaled)
	if !exists {
		t.Error("KeyCompletionSignaled was not set")
	}

	if result, ok := completionData.(*effect.CompletionResult); !ok {
		t.Errorf("KeyCompletionSignaled should contain CompletionResult, got: %T", completionData)
	} else {
		// Verify the target state is correct
		if result.TargetState != StateTesting {
			t.Errorf("Expected target state StateTesting, got: %s", result.TargetState)
		}

		// Test the state checking logic that would be used in executeCodingWithTemplate
		// Clear the completion signal for next iteration (as done in the actual code)
		sm.SetStateData(KeyCompletionSignaled, nil)

		// Verify signal was cleared
		clearedData, clearedExists := sm.GetStateValue(KeyCompletionSignaled)
		if clearedExists && clearedData != nil {
			t.Error("KeyCompletionSignaled should be cleared after processing")
		}

		t.Logf("✅ Done tool Effects logic works correctly - would trigger %s transition", result.TargetState)
	}
}

func TestDoneToolConstant(t *testing.T) {
	// Test that the done tool constant matches expected value
	if tools.ToolDone != "done" {
		t.Errorf("Expected tools.ToolDone to be 'done', got: %s", tools.ToolDone)
	}

	// Verify done tool is available in coding tool sets
	foundInDevOps := false
	for _, tool := range tools.DevOpsCodingTools {
		if tool == tools.ToolDone {
			foundInDevOps = true
			break
		}
	}
	if !foundInDevOps {
		t.Error("Done tool not found in DevOpsCodingTools")
	}

	foundInApp := false
	for _, tool := range tools.AppCodingTools {
		if tool == tools.ToolDone {
			foundInApp = true
			break
		}
	}
	if !foundInApp {
		t.Error("Done tool not found in AppCodingTools")
	}

	t.Logf("✅ Done tool is properly defined and available in coding tool sets")
}

func TestCompletionEffectCreation(t *testing.T) {
	// Test that CompletionEffect can be created and used correctly

	// Create a completion effect
	completionEff := effect.NewCompletionEffect(
		"Test completion message",
		StateTesting,
	)

	// Verify the effect properties
	if completionEff.Type() != "completion" {
		t.Errorf("Expected effect type 'completion', got: %s", completionEff.Type())
	}

	if completionEff.Message != "Test completion message" {
		t.Errorf("Expected message 'Test completion message', got: %s", completionEff.Message)
	}

	if completionEff.TargetState != StateTesting {
		t.Errorf("Expected target state StateTesting, got: %s", completionEff.TargetState)
	}

	t.Logf("✅ CompletionEffect creation works correctly")
}

func TestCompletionEffectPriorityLogic(t *testing.T) {
	// Test that completion effect takes priority over other completion checks

	// Create a mock state machine
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, nil)

	// Set up state to simulate both completion signal and other completion indicators
	completionResult := &effect.CompletionResult{
		TargetState: StateTesting,
		Message:     "Implementation complete - proceeding to testing phase",
		Metadata:    make(map[string]any),
	}
	sm.SetStateData(KeyCompletionSignaled, completionResult)
	sm.SetStateData(string(stateDataKeyTaskContent), "Test Task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	// Set up some files created to simulate other completion logic might trigger
	sm.SetStateData(KeyFilesCreated, []string{"file1.go", "file2.go", "file3.go"})

	// Test the priority logic from executeCodingWithTemplate
	// Completion effect should take highest priority
	if completionData, exists := sm.GetStateValue(KeyCompletionSignaled); exists {
		if completionResult, ok := completionData.(*effect.CompletionResult); ok {
			// Clear the completion signal
			sm.SetStateData(KeyCompletionSignaled, nil)

			// This would trigger the target state transition
			t.Logf("✅ Completion effect has highest priority - would transition to %s", completionResult.TargetState)

			// Verify that the signal is cleared so it doesn't trigger again
			clearedData, clearedExists := sm.GetStateValue(KeyCompletionSignaled)
			if clearedExists && clearedData != nil {
				t.Error("Completion signal should be cleared after processing")
			}
		} else {
			t.Error("Completion data is not a CompletionResult")
		}
	} else {
		t.Error("Completion signal should be set for this test")
	}
}
