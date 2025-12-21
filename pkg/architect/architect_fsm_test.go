package architect

import (
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

func TestArchitectStateString(t *testing.T) {
	tests := []struct {
		state    proto.State
		expected string
	}{
		{StateWaiting, "WAITING"},
		{StateSetup, "SETUP"},
		{StateDispatching, "DISPATCHING"},
		{StateMonitoring, "MONITORING"},
		{StateRequest, "REQUEST"},
		{StateEscalated, "ESCALATED"},
		{StateDone, "DONE"},
		{StateError, "ERROR"},
		{proto.State("INVALID"), "INVALID"}, // Invalid state
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
	// Test all valid transitions as defined in STATES.md.
	validTransitions := []struct {
		from proto.State
		to   proto.State
		name string
	}{
		// WAITING transitions.
		{StateWaiting, StateSetup, "WAITING -> SETUP (any request received)"},
		{StateWaiting, StateError, "WAITING -> ERROR (channel closed/abnormal shutdown)"},

		// SETUP transitions.
		{StateSetup, StateRequest, "SETUP -> REQUEST (workspace ready)"},
		{StateSetup, StateError, "SETUP -> ERROR (workspace setup failed)"},

		// DISPATCHING transitions.
		{StateDispatching, StateMonitoring, "DISPATCHING -> MONITORING (stories on work-queue)"},
		{StateDispatching, StateDone, "DISPATCHING -> DONE (no stories left)"},

		// MONITORING transitions.
		{StateMonitoring, StateRequest, "MONITORING -> REQUEST (coder request)"},
		{StateMonitoring, StateError, "MONITORING -> ERROR (channel closed/abnormal shutdown)"},

		// REQUEST transitions.
		{StateRequest, StateWaiting, "REQUEST -> WAITING (no spec work)"},
		{StateRequest, StateMonitoring, "REQUEST -> MONITORING (approve non-code/request changes)"},
		{StateRequest, StateDispatching, "REQUEST -> DISPATCHING (successful merge or spec approval)"},
		{StateRequest, StateEscalated, "REQUEST -> ESCALATED (cannot answer)"},
		{StateRequest, StateError, "REQUEST -> ERROR (abandon/unrecoverable)"},

		// ESCALATED transitions.
		{StateEscalated, StateRequest, "ESCALATED -> REQUEST (human answer supplied)"},
		{StateEscalated, StateError, "ESCALATED -> ERROR (timeout/no answer)"},

		// DONE transitions.
		{StateDone, StateWaiting, "DONE -> WAITING (new spec arrives)"},

		// ERROR transitions.
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
	// Test a comprehensive set of invalid transitions.
	invalidTransitions := []struct {
		from proto.State
		to   proto.State
		name string
	}{
		// Invalid WAITING transitions (must go through SETUP now).
		{StateWaiting, StateRequest, "WAITING -> REQUEST (invalid, must go through SETUP)"},
		{StateWaiting, StateDispatching, "WAITING -> DISPATCHING (invalid)"},
		{StateWaiting, StateMonitoring, "WAITING -> MONITORING (invalid)"},
		{StateWaiting, StateEscalated, "WAITING -> ESCALATED (invalid)"},
		{StateWaiting, StateDone, "WAITING -> DONE (invalid)"},

		// Invalid SETUP transitions (can only go to REQUEST or ERROR).
		{StateSetup, StateWaiting, "SETUP -> WAITING (invalid)"},
		{StateSetup, StateDispatching, "SETUP -> DISPATCHING (invalid)"},
		{StateSetup, StateMonitoring, "SETUP -> MONITORING (invalid)"},
		{StateSetup, StateEscalated, "SETUP -> ESCALATED (invalid)"},
		{StateSetup, StateDone, "SETUP -> DONE (invalid)"},

		// Invalid DISPATCHING transitions.
		{StateDispatching, StateWaiting, "DISPATCHING -> WAITING (invalid)"},
		{StateDispatching, StateSetup, "DISPATCHING -> SETUP (invalid)"},
		{StateDispatching, StateRequest, "DISPATCHING -> REQUEST (invalid)"},
		{StateDispatching, StateEscalated, "DISPATCHING -> ESCALATED (invalid)"},
		{StateDispatching, StateError, "DISPATCHING -> ERROR (invalid)"},

		// Invalid MONITORING transitions.
		{StateMonitoring, StateWaiting, "MONITORING -> WAITING (invalid)"},
		{StateMonitoring, StateSetup, "MONITORING -> SETUP (invalid)"},
		{StateMonitoring, StateDispatching, "MONITORING -> DISPATCHING (invalid)"},
		{StateMonitoring, StateEscalated, "MONITORING -> ESCALATED (invalid)"},
		{StateMonitoring, StateDone, "MONITORING -> DONE (invalid)"},

		// Invalid REQUEST transitions (REQUEST -> WAITING is valid, REQUEST -> SETUP is invalid)
		{StateRequest, StateSetup, "REQUEST -> SETUP (invalid)"},
		{StateRequest, StateDone, "REQUEST -> DONE (invalid)"},

		// Invalid ESCALATED transitions.
		{StateEscalated, StateWaiting, "ESCALATED -> WAITING (invalid)"},
		{StateEscalated, StateSetup, "ESCALATED -> SETUP (invalid)"},
		{StateEscalated, StateDispatching, "ESCALATED -> DISPATCHING (invalid)"},
		{StateEscalated, StateMonitoring, "ESCALATED -> MONITORING (invalid)"},
		{StateEscalated, StateDone, "ESCALATED -> DONE (invalid)"},

		// Invalid DONE transitions (can only go to WAITING)
		{StateDone, StateSetup, "DONE -> SETUP (invalid)"},
		{StateDone, StateDispatching, "DONE -> DISPATCHING (invalid)"},
		{StateDone, StateMonitoring, "DONE -> MONITORING (invalid)"},
		{StateDone, StateRequest, "DONE -> REQUEST (invalid)"},
		{StateDone, StateEscalated, "DONE -> ESCALATED (invalid)"},
		{StateDone, StateError, "DONE -> ERROR (invalid)"},

		// Invalid ERROR transitions (can only go to WAITING)
		{StateError, StateSetup, "ERROR -> SETUP (invalid)"},
		{StateError, StateDispatching, "ERROR -> DISPATCHING (invalid)"},
		{StateError, StateMonitoring, "ERROR -> MONITORING (invalid)"},
		{StateError, StateRequest, "ERROR -> REQUEST (invalid)"},
		{StateError, StateEscalated, "ERROR -> ESCALATED (invalid)"},
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
	expected := []proto.State{
		StateWaiting,
		StateSetup,
		StateDispatching,
		StateMonitoring,
		StateRequest,
		StateEscalated,
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
		state    proto.State
		expected bool
	}{
		{StateWaiting, false},
		{StateSetup, false},
		{StateDispatching, false},
		{StateMonitoring, false},
		{StateRequest, false},
		{StateEscalated, false},
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
	// Test all valid states.
	validStates := []proto.State{
		StateWaiting,
		StateSetup,
		StateDispatching,
		StateMonitoring,
		StateRequest,
		StateEscalated,
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

	// Test invalid states.
	invalidStates := []proto.State{
		proto.State("INVALID"),
		proto.State("UNKNOWN"),
		proto.State("BADSTATE"),
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
	// Test the ValidNextStates helper function.
	tests := []struct {
		from     proto.State
		expected []proto.State
	}{
		{StateWaiting, []proto.State{StateSetup, StateError}},
		{StateSetup, []proto.State{StateRequest, StateError}},
		{StateDispatching, []proto.State{StateMonitoring, StateDone}},
		{StateMonitoring, []proto.State{StateRequest, StateError}},
		{StateRequest, []proto.State{StateWaiting, StateMonitoring, StateDispatching, StateEscalated, StateError}},
		{StateEscalated, []proto.State{StateRequest, StateError}},
		{StateDone, []proto.State{StateWaiting}},
		{StateError, []proto.State{StateWaiting}},
	}

	for _, test := range tests {
		t.Run(test.from.String(), func(t *testing.T) {
			result := ValidNextStates(test.from)
			if len(result) != len(test.expected) {
				t.Errorf("Expected %d next states, got %d", len(test.expected), len(result))
			}

			// Check that all expected states are present.
			resultSet := make(map[proto.State]bool)
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
	// Verify that all target states in the transition map are valid states.
	allStates := GetAllArchitectStates()
	stateSet := make(map[proto.State]bool)
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

// Benchmarks for performance validation.
func BenchmarkIsValidArchitectTransition(b *testing.B) {
	for i := 0; i < b.N; i++ {
		IsValidArchitectTransition(StateWaiting, StateSetup)
	}
}

func BenchmarkArchitectStateString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = string(StateWaiting)
	}
}
