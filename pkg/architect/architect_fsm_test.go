package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/agent"
)

func TestArchitectStateString(t *testing.T) {
	tests := []struct {
		state    agent.State
		expected string
	}{
		{StateWaiting, "WAITING"},
		{StateScoping, "SCOPING"},
		{StateDispatching, "DISPATCHING"},
		{StateMonitoring, "MONITORING"},
		{StateRequest, "REQUEST"},
		{StateEscalated, "ESCALATED"},
		{StateMerging, "MERGING"},
		{StateDone, "DONE"},
		{StateError, "ERROR"},
		{agent.State("INVALID"), "INVALID"}, // Invalid state
	}

	for _, test := range tests {
		t.Run(test.expected, func(t *testing.T) {
			result := string(test.state)
			if result != test.expected {
				t.Errorf("Expected %s, got %s", test.expected, result)
			}
		})
	}
}

func TestIsValidArchitectTransition(t *testing.T) {
	// Test all valid transitions as defined in STATES.md
	validTransitions := []struct {
		from agent.State
		to   agent.State
		name string
	}{
		// WAITING transitions
		{StateWaiting, StateScoping, "WAITING -> SCOPING (spec received)"},

		// SCOPING transitions
		{StateScoping, StateDispatching, "SCOPING -> DISPATCHING (stories queued)"},
		{StateScoping, StateError, "SCOPING -> ERROR (unrecoverable error)"},

		// DISPATCHING transitions
		{StateDispatching, StateMonitoring, "DISPATCHING -> MONITORING (stories on work-queue)"},
		{StateDispatching, StateDone, "DISPATCHING -> DONE (no stories left)"},

		// MONITORING transitions
		{StateMonitoring, StateRequest, "MONITORING -> REQUEST (coder request)"},
		{StateMonitoring, StateMerging, "MONITORING -> MERGING (approved code-review)"},

		// REQUEST transitions
		{StateRequest, StateMonitoring, "REQUEST -> MONITORING (approve non-code/request changes)"},
		{StateRequest, StateMerging, "REQUEST -> MERGING (approve code-review)"},
		{StateRequest, StateEscalated, "REQUEST -> ESCALATED (cannot answer)"},
		{StateRequest, StateError, "REQUEST -> ERROR (abandon/unrecoverable)"},

		// ESCALATED transitions
		{StateEscalated, StateRequest, "ESCALATED -> REQUEST (human answer supplied)"},
		{StateEscalated, StateError, "ESCALATED -> ERROR (timeout/no answer)"},

		// MERGING transitions
		{StateMerging, StateDispatching, "MERGING -> DISPATCHING (merge succeeds)"},
		{StateMerging, StateError, "MERGING -> ERROR (merge failure)"},

		// DONE transitions
		{StateDone, StateWaiting, "DONE -> WAITING (new spec arrives)"},

		// ERROR transitions
		{StateError, StateWaiting, "ERROR -> WAITING (recovery/restart)"},
	}

	for _, test := range validTransitions {
		t.Run(test.name, func(t *testing.T) {
			if !IsValidArchitectTransition(test.from, test.to) {
				t.Errorf("Expected transition from %s to %s to be valid", test.from, test.to)
			}
		})
	}
}

func TestInvalidArchitectTransitions(t *testing.T) {
	// Test a comprehensive set of invalid transitions
	invalidTransitions := []struct {
		from agent.State
		to   agent.State
		name string
	}{
		// Invalid WAITING transitions
		{StateWaiting, StateDispatching, "WAITING -> DISPATCHING (invalid)"},
		{StateWaiting, StateMonitoring, "WAITING -> MONITORING (invalid)"},
		{StateWaiting, StateRequest, "WAITING -> REQUEST (invalid)"},
		{StateWaiting, StateEscalated, "WAITING -> ESCALATED (invalid)"},
		{StateWaiting, StateMerging, "WAITING -> MERGING (invalid)"},
		{StateWaiting, StateDone, "WAITING -> DONE (invalid)"},
		{StateWaiting, StateError, "WAITING -> ERROR (invalid)"},

		// Invalid SCOPING transitions
		{StateScoping, StateWaiting, "SCOPING -> WAITING (invalid)"},
		{StateScoping, StateMonitoring, "SCOPING -> MONITORING (invalid)"},
		{StateScoping, StateRequest, "SCOPING -> REQUEST (invalid)"},
		{StateScoping, StateEscalated, "SCOPING -> ESCALATED (invalid)"},
		{StateScoping, StateMerging, "SCOPING -> MERGING (invalid)"},
		{StateScoping, StateDone, "SCOPING -> DONE (invalid)"},

		// Invalid DISPATCHING transitions
		{StateDispatching, StateWaiting, "DISPATCHING -> WAITING (invalid)"},
		{StateDispatching, StateScoping, "DISPATCHING -> SCOPING (invalid)"},
		{StateDispatching, StateRequest, "DISPATCHING -> REQUEST (invalid)"},
		{StateDispatching, StateEscalated, "DISPATCHING -> ESCALATED (invalid)"},
		{StateDispatching, StateMerging, "DISPATCHING -> MERGING (invalid)"},
		{StateDispatching, StateError, "DISPATCHING -> ERROR (invalid)"},

		// Invalid MONITORING transitions
		{StateMonitoring, StateWaiting, "MONITORING -> WAITING (invalid)"},
		{StateMonitoring, StateScoping, "MONITORING -> SCOPING (invalid)"},
		{StateMonitoring, StateDispatching, "MONITORING -> DISPATCHING (invalid)"},
		{StateMonitoring, StateEscalated, "MONITORING -> ESCALATED (invalid)"},
		{StateMonitoring, StateDone, "MONITORING -> DONE (invalid)"},
		{StateMonitoring, StateError, "MONITORING -> ERROR (invalid)"},

		// Invalid REQUEST transitions (should not go directly to DISPATCHING)
		{StateRequest, StateWaiting, "REQUEST -> WAITING (invalid)"},
		{StateRequest, StateScoping, "REQUEST -> SCOPING (invalid)"},
		{StateRequest, StateDispatching, "REQUEST -> DISPATCHING (invalid)"},
		{StateRequest, StateDone, "REQUEST -> DONE (invalid)"},

		// Invalid ESCALATED transitions
		{StateEscalated, StateWaiting, "ESCALATED -> WAITING (invalid)"},
		{StateEscalated, StateScoping, "ESCALATED -> SCOPING (invalid)"},
		{StateEscalated, StateDispatching, "ESCALATED -> DISPATCHING (invalid)"},
		{StateEscalated, StateMonitoring, "ESCALATED -> MONITORING (invalid)"},
		{StateEscalated, StateMerging, "ESCALATED -> MERGING (invalid)"},
		{StateEscalated, StateDone, "ESCALATED -> DONE (invalid)"},

		// Invalid MERGING transitions
		{StateMerging, StateWaiting, "MERGING -> WAITING (invalid)"},
		{StateMerging, StateScoping, "MERGING -> SCOPING (invalid)"},
		{StateMerging, StateMonitoring, "MERGING -> MONITORING (invalid)"},
		{StateMerging, StateRequest, "MERGING -> REQUEST (invalid)"},
		{StateMerging, StateEscalated, "MERGING -> ESCALATED (invalid)"},
		{StateMerging, StateDone, "MERGING -> DONE (invalid)"},

		// Invalid DONE transitions (can only go to WAITING)
		{StateDone, StateScoping, "DONE -> SCOPING (invalid)"},
		{StateDone, StateDispatching, "DONE -> DISPATCHING (invalid)"},
		{StateDone, StateMonitoring, "DONE -> MONITORING (invalid)"},
		{StateDone, StateRequest, "DONE -> REQUEST (invalid)"},
		{StateDone, StateEscalated, "DONE -> ESCALATED (invalid)"},
		{StateDone, StateMerging, "DONE -> MERGING (invalid)"},
		{StateDone, StateError, "DONE -> ERROR (invalid)"},

		// Invalid ERROR transitions (can only go to WAITING)
		{StateError, StateScoping, "ERROR -> SCOPING (invalid)"},
		{StateError, StateDispatching, "ERROR -> DISPATCHING (invalid)"},
		{StateError, StateMonitoring, "ERROR -> MONITORING (invalid)"},
		{StateError, StateRequest, "ERROR -> REQUEST (invalid)"},
		{StateError, StateEscalated, "ERROR -> ESCALATED (invalid)"},
		{StateError, StateMerging, "ERROR -> MERGING (invalid)"},
		{StateError, StateDone, "ERROR -> DONE (invalid)"},
	}

	for _, test := range invalidTransitions {
		t.Run(test.name, func(t *testing.T) {
			if IsValidArchitectTransition(test.from, test.to) {
				t.Errorf("Expected transition from %s to %s to be invalid", test.from, test.to)
			}
		})
	}
}

func TestEscalationTimeout(t *testing.T) {
	expected := 2 * time.Hour
	if EscalationTimeout != expected {
		t.Errorf("Expected EscalationTimeout to be %v, got %v", expected, EscalationTimeout)
	}
}

func TestGetAllArchitectStates(t *testing.T) {
	states := GetAllArchitectStates()
	expected := []agent.State{
		StateWaiting,
		StateScoping,
		StateDispatching,
		StateMonitoring,
		StateRequest,
		StateEscalated,
		StateMerging,
		StateDone,
		StateError,
	}

	if len(states) != len(expected) {
		t.Errorf("Expected %d states, got %d", len(expected), len(states))
	}

	for i, state := range states {
		if state != expected[i] {
			t.Errorf("Expected state at index %d to be %s, got %s", i, expected[i], state)
		}
	}
}

func TestIsTerminalState(t *testing.T) {
	tests := []struct {
		state    agent.State
		expected bool
	}{
		{StateWaiting, false},
		{StateScoping, false},
		{StateDispatching, false},
		{StateMonitoring, false},
		{StateRequest, false},
		{StateEscalated, false},
		{StateMerging, false},
		{StateDone, true},
		{StateError, true},
	}

	for _, test := range tests {
		t.Run(test.state.String(), func(t *testing.T) {
			result := IsTerminalState(test.state)
			if result != test.expected {
				t.Errorf("Expected IsTerminalState(%s) to be %v, got %v", test.state, test.expected, result)
			}
		})
	}
}

func TestIsValidArchitectState(t *testing.T) {
	// Test all valid states
	validStates := []agent.State{
		StateWaiting,
		StateScoping,
		StateDispatching,
		StateMonitoring,
		StateRequest,
		StateEscalated,
		StateMerging,
		StateDone,
		StateError,
	}

	for _, state := range validStates {
		t.Run(state.String(), func(t *testing.T) {
			if !IsValidArchitectState(state) {
				t.Errorf("Expected %s to be a valid state", state)
			}
		})
	}

	// Test invalid states
	invalidStates := []agent.State{
		agent.State("INVALID"),
		agent.State("UNKNOWN"),
		agent.State("BADSTATE"),
	}

	for _, state := range invalidStates {
		t.Run("InvalidState", func(t *testing.T) {
			if IsValidArchitectState(state) {
				t.Errorf("Expected %s to be an invalid state", state)
			}
		})
	}
}

func TestTransitionMapCompleteness(t *testing.T) {
	// Verify that all states defined in GetAllArchitectStates() have entries in the transition map
	allStates := GetAllArchitectStates()

	for _, state := range allStates {
		t.Run(state.String(), func(t *testing.T) {
			nextStates := ValidNextStates(state)
			if nextStates == nil {
				t.Errorf("State %s has no valid next states defined", state)
			}
		})
	}
}

func TestValidNextStates(t *testing.T) {
	// Test the ValidNextStates helper function
	tests := []struct {
		from     agent.State
		expected []agent.State
	}{
		{StateWaiting, []agent.State{StateScoping}},
		{StateScoping, []agent.State{StateDispatching, StateError}},
		{StateDispatching, []agent.State{StateMonitoring, StateDone}},
		{StateMonitoring, []agent.State{StateRequest, StateMerging}},
		{StateRequest, []agent.State{StateMonitoring, StateMerging, StateEscalated, StateError}},
		{StateEscalated, []agent.State{StateRequest, StateError}},
		{StateMerging, []agent.State{StateDispatching, StateError}},
		{StateDone, []agent.State{StateWaiting}},
		{StateError, []agent.State{StateWaiting}},
	}

	for _, test := range tests {
		t.Run(test.from.String(), func(t *testing.T) {
			result := ValidNextStates(test.from)
			if len(result) != len(test.expected) {
				t.Errorf("Expected %d next states, got %d", len(test.expected), len(result))
			}

			// Check that all expected states are present
			resultSet := make(map[agent.State]bool)
			for _, state := range result {
				resultSet[state] = true
			}

			for _, expected := range test.expected {
				if !resultSet[expected] {
					t.Errorf("Expected state %s not found in ValidNextStates(%s)", expected, test.from)
				}
			}
		})
	}
}

func TestTransitionMapIntegrity(t *testing.T) {
	// Verify that all target states in the transition map are valid states
	allStates := GetAllArchitectStates()
	stateSet := make(map[agent.State]bool)
	for _, state := range allStates {
		stateSet[state] = true
	}

	for _, fromState := range allStates {
		toStates := ValidNextStates(fromState)
		for _, toState := range toStates {
			if !stateSet[toState] {
				t.Errorf("Transition from %s to %s references invalid target state", fromState, toState)
			}
		}
	}
}

// Benchmarks for performance validation
func BenchmarkIsValidArchitectTransition(b *testing.B) {
	for i := 0; i < b.N; i++ {
		IsValidArchitectTransition(StateWaiting, StateScoping)
	}
}

func BenchmarkArchitectStateString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = string(StateWaiting)
	}
}
