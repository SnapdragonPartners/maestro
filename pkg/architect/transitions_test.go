package architect

import (
	"context"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// This test suite ensures that the architect state machine's transition table
// matches the documented states in STATES.md, following the same pattern as PM tests.
//
// Key tests:
// - TestAllValidTransitions: Verifies every documented transition actually works
// - TestInvalidTransitions: Ensures invalid transitions are rejected
// - TestTransitionTableCompleteness: Catches orphaned states with no transitions
// - TestStateSymmetryWithDocumentation: Ensures GetValidStates() matches GetAllArchitectStates()
// - TestTransitionValidation: Verifies ValidateState() properly validates states

// TestAllValidTransitions verifies that all transitions documented in STATES.md
// are actually valid in the code.
func TestAllValidTransitions(t *testing.T) {
	// Define all valid transitions as documented in STATES.md
	documentedTransitions := map[proto.State][]proto.State{
		StateWaiting: {
			StateSetup,
			StateError,
		},
		StateSetup: {
			StateRequest,
			StateError,
		},
		StateDispatching: {
			StateMonitoring,
			StateDone,
		},
		StateMonitoring: {
			StateRequest,
			StateError,
		},
		StateRequest: {
			StateWaiting,
			StateMonitoring,
			StateDispatching,
			StateEscalated,
			StateError,
		},
		StateEscalated: {
			StateRequest,
			StateError,
		},
		StateDone: {
			StateWaiting,
		},
		StateError: {
			StateWaiting,
		},
	}

	ctx := context.Background()

	for fromState, toStates := range documentedTransitions {
		for _, toState := range toStates {
			t.Run(fromState.String()+"->"+toState.String(), func(t *testing.T) {
				// Create a minimal architect driver in the source state
				sm := agent.NewBaseStateMachine("architect-test", fromState, nil, architectTransitions)
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
		{StateWaiting, StateRequest, "WAITING->REQUEST (must go through SETUP)"},
		{StateWaiting, StateDispatching, "WAITING->DISPATCHING (must go through SETUP)"},
		{StateWaiting, StateMonitoring, "WAITING->MONITORING (must go through SETUP)"},
		{StateSetup, StateWaiting, "SETUP->WAITING (invalid)"},
		{StateSetup, StateDispatching, "SETUP->DISPATCHING (invalid)"},
		{StateDispatching, StateRequest, "DISPATCHING->REQUEST (invalid)"},
		{StateDispatching, StateEscalated, "DISPATCHING->ESCALATED (invalid)"},
		{StateMonitoring, StateDispatching, "MONITORING->DISPATCHING (must go through REQUEST)"},
		{StateMonitoring, StateEscalated, "MONITORING->ESCALATED (must go through REQUEST)"},
		{StateEscalated, StateWaiting, "ESCALATED->WAITING (must go through REQUEST)"},
		{StateEscalated, StateMonitoring, "ESCALATED->MONITORING (invalid)"},
		{StateDone, StateMonitoring, "DONE->MONITORING (terminal state)"},
		{StateError, StateRequest, "ERROR->REQUEST (must go through WAITING)"},
	}

	ctx := context.Background()

	for _, tt := range invalidTransitions {
		t.Run(tt.name, func(t *testing.T) {
			// Create a minimal architect driver in the source state
			sm := agent.NewBaseStateMachine("architect-test", tt.from, nil, architectTransitions)
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
// one valid transition (except DONE which can be terminal).
func TestTransitionTableCompleteness(t *testing.T) {
	allStates := GetAllArchitectStates()

	for _, state := range allStates {
		t.Run("state_"+state.String(), func(t *testing.T) {
			// SUSPEND transitions are handled dynamically by BaseStateMachine (returns to originating state)
			if state == proto.StateSuspend {
				return
			}

			validNextStates := ValidNextStates(state)

			// All states should have at least one valid transition
			// (Even terminal states like DONE have recovery transitions to WAITING)
			if len(validNextStates) == 0 {
				t.Errorf("State %s has no valid transitions (orphaned state)", state)
			}

			// Verify the transition table actually allows transitions to these states
			for _, nextState := range validNextStates {
				if !IsValidArchitectTransition(state, nextState) {
					t.Errorf("Transition %s → %s is in ValidNextStates but IsValidArchitectTransition returns false", state, nextState)
				}
			}
		})
	}
}

// TestStateSymmetryWithDocumentation verifies that the code's transition table
// matches what GetValidStates() returns (catches inconsistencies).
func TestStateSymmetryWithDocumentation(t *testing.T) {
	// Create a driver to access GetValidStates()
	sm := agent.NewBaseStateMachine("architect-test", StateWaiting, nil, architectTransitions)
	driver := &Driver{
		BaseStateMachine: sm,
	}

	// Get all valid states from driver
	driverStates := driver.GetValidStates()

	// Get all states from helper
	helperStates := GetAllArchitectStates()

	// Verify they match
	if len(driverStates) != len(helperStates) {
		t.Errorf("GetValidStates() returns %d states but GetAllArchitectStates() returns %d", len(driverStates), len(helperStates))
	}

	// Build a map for easy lookup
	driverStateMap := make(map[proto.State]bool)
	for _, s := range driverStates {
		driverStateMap[s] = true
	}

	// Verify all helper states are in driver states
	for _, s := range helperStates {
		if !driverStateMap[s] {
			t.Errorf("State %s is in GetAllArchitectStates() but not in driver.GetValidStates()", s)
		}
	}
}

// TestTransitionValidation tests the ValidateState method to ensure it
// properly accepts valid states and rejects invalid ones.
func TestTransitionValidation(t *testing.T) {
	sm := agent.NewBaseStateMachine("architect-test", StateWaiting, nil, architectTransitions)
	driver := &Driver{
		BaseStateMachine: sm,
	}

	// Test all valid architect states
	validStates := GetAllArchitectStates()
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
		"PLANNING", // Coder state, not architect
		"CODING",   // Coder state, not architect
		"WORKING",  // PM state, not architect
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

// TestCriticalTransitions specifically tests critical transitions that enable key workflows.
func TestCriticalTransitions(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name      string
		from      proto.State
		to        proto.State
		rationale string
	}{
		{
			name:      "WAITING_to_SETUP",
			from:      StateWaiting,
			to:        StateSetup,
			rationale: "Architect must transition to SETUP to prepare workspace before processing requests",
		},
		{
			name:      "SETUP_to_REQUEST",
			from:      StateSetup,
			to:        StateRequest,
			rationale: "After workspace setup, architect processes the pending request",
		},
		{
			name:      "REQUEST_to_DISPATCHING",
			from:      StateRequest,
			to:        StateDispatching,
			rationale: "After spec approval, architect must dispatch stories",
		},
		{
			name:      "DISPATCHING_to_MONITORING",
			from:      StateDispatching,
			to:        StateMonitoring,
			rationale: "After dispatching, architect monitors coder progress",
		},
		{
			name:      "MONITORING_to_REQUEST",
			from:      StateMonitoring,
			to:        StateRequest,
			rationale: "Handle coder questions and approval requests",
		},
		{
			name:      "REQUEST_to_ESCALATED",
			from:      StateRequest,
			to:        StateEscalated,
			rationale: "Escalate when iteration limits exceeded",
		},
		{
			name:      "ESCALATED_to_REQUEST",
			from:      StateEscalated,
			to:        StateRequest,
			rationale: "Resume work after human guidance",
		},
		{
			name:      "DONE_to_WAITING",
			from:      StateDone,
			to:        StateWaiting,
			rationale: "Ready for next spec after completion",
		},
		{
			name:      "ERROR_to_WAITING",
			from:      StateError,
			to:        StateWaiting,
			rationale: "Recover from errors and wait for new work",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sm := agent.NewBaseStateMachine("architect-test", tt.from, nil, architectTransitions)
			driver := &Driver{
				BaseStateMachine: sm,
			}

			// Verify we start in the expected state
			if driver.GetCurrentState() != tt.from {
				t.Fatalf("Expected initial state %s, got %s", tt.from, driver.GetCurrentState())
			}

			// Attempt the critical transition
			err := driver.TransitionTo(ctx, tt.to, nil)
			if err != nil {
				t.Errorf("%s transition failed (%s): %v", tt.name, tt.rationale, err)
			}

			// Verify we're now in the target state
			if driver.GetCurrentState() != tt.to {
				t.Errorf("After %s transition, state is %s (expected %s)", tt.name, driver.GetCurrentState(), tt.to)
			}
		})
	}
}
