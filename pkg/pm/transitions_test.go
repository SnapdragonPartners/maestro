package pm

import (
	"context"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// This test suite ensures that the PM state machine's transition table
// matches the documented states in STATES.md. It would have caught the
// bug where WAITING → WORKING was missing from validTransitions, causing
// the PM to fail silently when trying to enter WORKING state after
// detecting bootstrap requirements.
//
// Key tests:
// - TestAllValidTransitions: Verifies every documented transition actually works
// - TestInvalidTransitions: Ensures invalid transitions are rejected
// - TestCriticalBootstrapTransition: Regression test for the WAITING → WORKING bug
// - TestTransitionTableCompleteness: Catches orphaned states with no transitions
// - TestStateSymmetryWithDocumentation: Ensures GetValidStates() matches GetAllPMStates()
// - TestTransitionValidation: Verifies ValidateState() properly validates states

// TestAllValidTransitions verifies that all transitions documented in STATES.md
// are actually valid in the code. This test would have caught the missing
// WAITING → WORKING transition bug.
func TestAllValidTransitions(t *testing.T) {
	// Define all valid transitions as documented in STATES.md
	documentedTransitions := map[proto.State][]proto.State{
		StateWaiting: {
			StateWaiting,
			StateWorking,
			StateAwaitUser,
			StateAwaitArchitect, // Bootstrap spec (Spec 0) sent at startup
			proto.StateDone,
		},
		StateWorking: {
			StateWorking,
			StateAwaitUser,
			StatePreview,
			StateAwaitArchitect, // Bootstrap spec (Spec 0) sent from WORKING
			proto.StateError,
			proto.StateDone,
		},
		StateAwaitUser: {
			StateAwaitUser,
			StateWorking,
			proto.StateError,
			proto.StateDone,
		},
		StatePreview: {
			StatePreview,
			StateAwaitUser,
			StateAwaitArchitect,
			proto.StateError,
			proto.StateDone,
		},
		StateAwaitArchitect: {
			StateAwaitArchitect,
			StateWorking, // Architect provides feedback OR approval (stay engaged for tweaks)
			proto.StateError,
			proto.StateDone,
		},
		proto.StateError: {
			StateWaiting,
			proto.StateDone,
		},
		proto.StateDone: {
			// Terminal state - no outgoing transitions
		},
	}

	ctx := context.Background()

	for fromState, toStates := range documentedTransitions {
		for _, toState := range toStates {
			t.Run(fromState.String()+"->"+toState.String(), func(t *testing.T) {
				// Create a minimal PM driver in the source state
				sm := agent.NewBaseStateMachine("pm-test", fromState, nil, validTransitions)
				driver := &Driver{
					BaseStateMachine: sm,
				}

				// Attempt the transition
				err := driver.TransitionTo(ctx, toState, nil)
				if err != nil {
					t.Errorf("Valid transition %s → %s was rejected: %v", fromState, toState, err)
				}

				// Verify we're actually in the new state
				if driver.GetCurrentState() != toState {
					t.Errorf("After transition %s → %s, state is %s", fromState, toState, driver.GetCurrentState())
				}
			})
		}
	}
}

// TestInvalidTransitions verifies that invalid transitions are properly rejected.
// This ensures the state machine enforces its rules.
func TestInvalidTransitions(t *testing.T) {
	// Define some invalid transitions that should be rejected
	invalidTransitions := []struct {
		from proto.State
		to   proto.State
		name string
	}{
		{StateAwaitUser, StatePreview, "AWAIT_USER->PREVIEW (must go through WORKING)"},
		{StateAwaitUser, StateAwaitArchitect, "AWAIT_USER->AWAIT_ARCHITECT (invalid path)"},
		{StatePreview, StateWorking, "PREVIEW->WORKING (must use AWAIT_USER)"},
		{StateAwaitArchitect, StatePreview, "AWAIT_ARCHITECT->PREVIEW (invalid)"},
		{proto.StateDone, StateWaiting, "DONE->WAITING (terminal state)"},
		{proto.StateDone, StateWorking, "DONE->WORKING (terminal state)"},
	}

	ctx := context.Background()

	for _, tt := range invalidTransitions {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal PM driver in the source state
			sm := agent.NewBaseStateMachine("pm-test", tt.from, nil, validTransitions)
			driver := &Driver{
				BaseStateMachine: sm,
			}

			// Attempt the invalid transition
			err := driver.TransitionTo(ctx, tt.to, nil)
			if err == nil {
				t.Errorf("Invalid transition %s → %s was allowed (should be rejected)", tt.from, tt.to)
			}

			// Verify we're still in the original state (transition was rejected)
			if driver.GetCurrentState() != tt.from {
				t.Errorf("After rejected transition, state changed from %s to %s", tt.from, driver.GetCurrentState())
			}
		})
	}
}

// TestTransitionTableCompleteness verifies that every state has at least
// a self-transition (prevents accidentally removing states from the table).
func TestTransitionTableCompleteness(t *testing.T) {
	allStates := GetAllPMStates()

	for _, state := range allStates {
		t.Run("state_"+state.String(), func(t *testing.T) {
			validNextStates := ValidNextStates(state)

			// DONE is terminal - it's ok to have no transitions
			if state == proto.StateDone {
				if len(validNextStates) > 0 {
					t.Errorf("DONE state should have no outgoing transitions, got: %v", validNextStates)
				}
				return
			}

			// All other states should have at least one valid transition
			if len(validNextStates) == 0 {
				t.Errorf("State %s has no valid transitions (orphaned state)", state)
			}

			// Verify the transition table actually allows transitions to these states
			for _, nextState := range validNextStates {
				if !IsValidPMTransition(state, nextState) {
					t.Errorf("Transition %s → %s is in ValidNextStates but IsValidPMTransition returns false", state, nextState)
				}
			}
		})
	}
}

// TestStateSymmetryWithDocumentation verifies that the code's transition table
// matches what GetValidStates() returns (catches inconsistencies).
func TestStateSymmetryWithDocumentation(t *testing.T) {
	// Create a driver to access GetValidStates()
	sm := agent.NewBaseStateMachine("pm-test", StateWaiting, nil, validTransitions)
	driver := &Driver{
		BaseStateMachine: sm,
	}

	// Get all valid states from driver
	driverStates := driver.GetValidStates()

	// Get all states from helper
	helperStates := GetAllPMStates()

	// Verify they match
	if len(driverStates) != len(helperStates) {
		t.Errorf("GetValidStates() returns %d states but GetAllPMStates() returns %d", len(driverStates), len(helperStates))
	}

	// Build a map for easy lookup
	driverStateMap := make(map[proto.State]bool)
	for _, s := range driverStates {
		driverStateMap[s] = true
	}

	// Verify all helper states are in driver states
	for _, s := range helperStates {
		if !driverStateMap[s] {
			t.Errorf("State %s is in GetAllPMStates() but not in driver.GetValidStates()", s)
		}
	}
}

// TestTransitionValidation tests the ValidateState method to ensure it
// properly accepts valid states and rejects invalid ones.
func TestTransitionValidation(t *testing.T) {
	sm := agent.NewBaseStateMachine("pm-test", StateWaiting, nil, validTransitions)
	driver := &Driver{
		BaseStateMachine: sm,
	}

	// Test all valid PM states
	validStates := GetAllPMStates()
	for _, state := range validStates {
		t.Run("valid_"+state.String(), func(t *testing.T) {
			err := driver.ValidateState(state)
			if err != nil {
				t.Errorf("ValidateState(%s) returned error for valid state: %v", state, err)
			}
		})
	}

	// Test some invalid states
	invalidStates := []proto.State{
		"INVALID",
		"PLANNING", // Coder state, not PM
		"CODING",   // Coder state, not PM
		"",         // Empty state
	}

	for _, state := range invalidStates {
		t.Run("invalid_"+state.String(), func(t *testing.T) {
			err := driver.ValidateState(state)
			if err == nil {
				t.Errorf("ValidateState(%s) should return error for invalid state", state)
			}
		})
	}
}

// TestCriticalBootstrapTransition specifically tests the WAITING → WORKING
// transition that was missing and caused the bug. This is a regression test.
func TestCriticalBootstrapTransition(t *testing.T) {
	ctx := context.Background()

	// Create PM in WAITING state
	sm := agent.NewBaseStateMachine("pm-test", StateWaiting, nil, validTransitions)
	driver := &Driver{
		BaseStateMachine: sm,
	}

	// Verify we start in WAITING
	if driver.GetCurrentState() != StateWaiting {
		t.Fatalf("Expected initial state WAITING, got %s", driver.GetCurrentState())
	}

	// Attempt the critical transition (this is what failed before the fix)
	err := driver.TransitionTo(ctx, StateWorking, nil)
	if err != nil {
		t.Fatalf("WAITING → WORKING transition failed (this is the bug we fixed): %v", err)
	}

	// Verify we're now in WORKING
	if driver.GetCurrentState() != StateWorking {
		t.Errorf("After WAITING → WORKING transition, state is %s (expected WORKING)", driver.GetCurrentState())
	}
}
