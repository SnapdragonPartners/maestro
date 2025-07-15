package coder

import (
	"testing"

	"orchestrator/pkg/agent"
)

func TestSetupStateTransitions(t *testing.T) {
	// Test WAITING → SETUP
	if !IsValidCoderTransition(agent.StateWaiting, StateSetup) {
		t.Error("WAITING → SETUP should be valid")
	}

	// Test SETUP → PLANNING
	if !IsValidCoderTransition(StateSetup, StatePlanning) {
		t.Error("SETUP → PLANNING should be valid")
	}

	// Test SETUP → ERROR
	if !IsValidCoderTransition(StateSetup, agent.StateError) {
		t.Error("SETUP → ERROR should be valid")
	}

	// Test DONE → SETUP (non-terminal)
	if !IsValidCoderTransition(agent.StateDone, StateSetup) {
		t.Error("DONE → SETUP should be valid (non-terminal)")
	}

	// Test ERROR → SETUP (non-terminal)
	if !IsValidCoderTransition(agent.StateError, StateSetup) {
		t.Error("ERROR → SETUP should be valid (non-terminal)")
	}

	// Test invalid transitions
	if IsValidCoderTransition(agent.StateWaiting, StatePlanning) {
		t.Error("WAITING → PLANNING should no longer be valid (must go through SETUP)")
	}
}

func TestSetupStateInValidStates(t *testing.T) {
	validStates := GetValidStates()
	
	// Check that SETUP is included
	found := false
	for _, state := range validStates {
		if state == StateSetup {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("SETUP state should be in valid states list")
	}
}

func TestSetupStateIsCoderState(t *testing.T) {
	if !IsCoderState(StateSetup) {
		t.Error("SETUP should be recognized as a coder state")
	}
}