package pm

import (
	"testing"

	"orchestrator/pkg/proto"
)

func TestPMStateString(t *testing.T) {
	tests := []struct {
		state    proto.State
		expected string
	}{
		{StateWaiting, "WAITING"},
		{StateInterviewing, "INTERVIEWING"},
		{StateDrafting, "DRAFTING"},
		{StateSubmitting, "SUBMITTING"},
		{proto.StateDone, "DONE"},
		{proto.StateError, "ERROR"},
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

func TestIsValidPMTransition(t *testing.T) {
	// Test all valid transitions as defined in states.go.
	validTransitions := []struct {
		from proto.State
		to   proto.State
		name string
	}{
		// WAITING transitions
		{StateWaiting, StateInterviewing, "WAITING -> INTERVIEWING (interview starts)"},
		{StateWaiting, StateSubmitting, "WAITING -> SUBMITTING (spec file upload)"},
		{StateWaiting, proto.StateDone, "WAITING -> DONE (shutdown)"},

		// INTERVIEWING transitions
		{StateInterviewing, StateDrafting, "INTERVIEWING -> DRAFTING (ready to draft)"},
		{StateInterviewing, proto.StateError, "INTERVIEWING -> ERROR (interview failed)"},
		{StateInterviewing, proto.StateDone, "INTERVIEWING -> DONE (shutdown)"},

		// DRAFTING transitions
		{StateDrafting, StateSubmitting, "DRAFTING -> SUBMITTING (spec ready)"},
		{StateDrafting, StateInterviewing, "DRAFTING -> INTERVIEWING (needs refinement)"},
		{StateDrafting, proto.StateError, "DRAFTING -> ERROR (draft failed)"},
		{StateDrafting, proto.StateDone, "DRAFTING -> DONE (shutdown)"},

		// SUBMITTING transitions
		{StateSubmitting, StateWaiting, "SUBMITTING -> WAITING (next interview)"},
		{StateSubmitting, proto.StateError, "SUBMITTING -> ERROR (submit failed)"},
		{StateSubmitting, proto.StateDone, "SUBMITTING -> DONE (shutdown)"},

		// ERROR transitions
		{proto.StateError, StateWaiting, "ERROR -> WAITING (restart)"},
		{proto.StateError, proto.StateDone, "ERROR -> DONE (shutdown)"},
	}

	for _, test := range validTransitions {
		t.Run(test.name, func(t *testing.T) {
			if !IsValidPMTransition(test.from, test.to) {
				t.Errorf("Expected transition from %s to %s to be valid", test.from, test.to)
			}
		})
	}
}

func TestInvalidPMTransitions(t *testing.T) {
	// Test a comprehensive set of invalid transitions.
	invalidTransitions := []struct {
		from proto.State
		to   proto.State
		name string
	}{
		// Invalid WAITING transitions
		{StateWaiting, StateDrafting, "WAITING -> DRAFTING (invalid)"},
		{StateWaiting, proto.StateError, "WAITING -> ERROR (invalid)"},

		// Invalid INTERVIEWING transitions
		{StateInterviewing, StateWaiting, "INTERVIEWING -> WAITING (invalid)"},
		{StateInterviewing, StateSubmitting, "INTERVIEWING -> SUBMITTING (invalid)"},

		// Invalid DRAFTING transitions
		{StateDrafting, StateWaiting, "DRAFTING -> WAITING (invalid)"},

		// Invalid SUBMITTING transitions
		{StateSubmitting, StateInterviewing, "SUBMITTING -> INTERVIEWING (invalid)"},
		{StateSubmitting, StateDrafting, "SUBMITTING -> DRAFTING (invalid)"},

		// Invalid DONE transitions (DONE should only transition to WAITING via restart policy)
		{proto.StateDone, StateInterviewing, "DONE -> INTERVIEWING (invalid)"},
		{proto.StateDone, StateDrafting, "DONE -> DRAFTING (invalid)"},
		{proto.StateDone, StateSubmitting, "DONE -> SUBMITTING (invalid)"},
		{proto.StateDone, proto.StateError, "DONE -> ERROR (invalid)"},

		// Invalid ERROR transitions
		{proto.StateError, StateInterviewing, "ERROR -> INTERVIEWING (invalid)"},
		{proto.StateError, StateDrafting, "ERROR -> DRAFTING (invalid)"},
		{proto.StateError, StateSubmitting, "ERROR -> SUBMITTING (invalid)"},
	}

	for _, test := range invalidTransitions {
		t.Run(test.name, func(t *testing.T) {
			if IsValidPMTransition(test.from, test.to) {
				t.Errorf("Expected transition from %s to %s to be invalid", test.from, test.to)
			}
		})
	}
}

func TestGetAllPMStates(t *testing.T) {
	states := GetAllPMStates()
	expected := []proto.State{
		StateWaiting,
		StateInterviewing,
		StateDrafting,
		StateSubmitting,
		proto.StateDone,
		proto.StateError,
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
		{StateInterviewing, false},
		{StateDrafting, false},
		{StateSubmitting, false},
		{proto.StateDone, true},
		{proto.StateError, true},
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

func TestIsValidPMState(t *testing.T) {
	// Test all valid states
	validStates := []proto.State{
		StateWaiting,
		StateInterviewing,
		StateDrafting,
		StateSubmitting,
		proto.StateDone,
		proto.StateError,
	}

	for _, state := range validStates {
		t.Run(state.String(), func(t *testing.T) {
			if !IsValidPMState(state) {
				t.Errorf("Expected %s to be a valid state", state)
			}
		})
	}

	// Test invalid states
	invalidStates := []proto.State{
		proto.State("INVALID"),
		proto.State("UNKNOWN"),
		proto.State("BADSTATE"),
		proto.State("SCOPING"), // Valid for architect but not PM
	}

	for _, state := range invalidStates {
		t.Run("InvalidState_"+string(state), func(t *testing.T) {
			if IsValidPMState(state) {
				t.Errorf("Expected %s to be an invalid state", state)
			}
		})
	}
}

func TestTransitionMapCompleteness(t *testing.T) {
	// Verify that all states defined in GetAllPMStates() have entries in the transition map
	allStates := GetAllPMStates()

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
		from     proto.State
		expected []proto.State
	}{
		{StateWaiting, []proto.State{StateInterviewing, StateSubmitting, proto.StateDone}},
		{StateInterviewing, []proto.State{StateDrafting, proto.StateError, proto.StateDone}},
		{StateDrafting, []proto.State{StateSubmitting, StateInterviewing, proto.StateError, proto.StateDone}},
		{StateSubmitting, []proto.State{StateWaiting, proto.StateError, proto.StateDone}},
		{proto.StateError, []proto.State{StateWaiting, proto.StateDone}},
	}

	for _, test := range tests {
		t.Run(test.from.String(), func(t *testing.T) {
			result := ValidNextStates(test.from)
			if len(result) != len(test.expected) {
				t.Errorf("Expected %d next states, got %d", len(test.expected), len(result))
			}

			// Check that all expected states are present
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
	// Verify that all target states in the transition map are valid states
	allStates := GetAllPMStates()
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

// TestPMStateFlow tests the typical happy path flow through PM states.
func TestPMStateFlow(t *testing.T) {
	// Simulate a typical interview flow
	transitions := []struct {
		from proto.State
		to   proto.State
		desc string
	}{
		{StateWaiting, StateInterviewing, "User starts interview"},
		{StateInterviewing, StateDrafting, "Interview complete, drafting spec"},
		{StateDrafting, StateSubmitting, "Draft complete, submitting"},
		{StateSubmitting, StateWaiting, "Submitted, ready for next interview"},
	}

	currentState := StateWaiting
	for _, tr := range transitions {
		t.Run(tr.desc, func(t *testing.T) {
			if currentState != tr.from {
				t.Fatalf("State mismatch: expected to be in %s, but in %s", tr.from, currentState)
			}
			if !IsValidPMTransition(tr.from, tr.to) {
				t.Errorf("Transition %s -> %s should be valid", tr.from, tr.to)
			}
			currentState = tr.to
		})
	}
}

// TestPMErrorRecoveryFlow tests error handling and recovery.
func TestPMErrorRecoveryFlow(t *testing.T) {
	// Test error during interview
	if !IsValidPMTransition(StateInterviewing, proto.StateError) {
		t.Error("Should be able to transition from INTERVIEWING to ERROR")
	}
	if !IsValidPMTransition(proto.StateError, StateWaiting) {
		t.Error("Should be able to recover from ERROR to WAITING")
	}

	// Test error during drafting
	if !IsValidPMTransition(StateDrafting, proto.StateError) {
		t.Error("Should be able to transition from DRAFTING to ERROR")
	}

	// Test error during submission
	if !IsValidPMTransition(StateSubmitting, proto.StateError) {
		t.Error("Should be able to transition from SUBMITTING to ERROR")
	}
}

// TestPMRefinementLoop tests going back from DRAFTING to INTERVIEWING.
func TestPMRefinementLoop(t *testing.T) {
	// This tests the refinement loop where draft needs more info
	transitions := []struct {
		from proto.State
		to   proto.State
		desc string
	}{
		{StateWaiting, StateInterviewing, "Start interview"},
		{StateInterviewing, StateDrafting, "First draft attempt"},
		{StateDrafting, StateInterviewing, "Need more info, back to interview"},
		{StateInterviewing, StateDrafting, "Second draft attempt"},
		{StateDrafting, StateSubmitting, "Draft looks good"},
	}

	for _, tr := range transitions {
		t.Run(tr.desc, func(t *testing.T) {
			if !IsValidPMTransition(tr.from, tr.to) {
				t.Errorf("Transition %s -> %s should be valid for: %s", tr.from, tr.to, tr.desc)
			}
		})
	}
}

// Benchmarks for performance validation.
func BenchmarkIsValidPMTransition(b *testing.B) {
	for i := 0; i < b.N; i++ {
		IsValidPMTransition(StateWaiting, StateInterviewing)
	}
}

func BenchmarkPMStateString(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = string(StateWaiting)
	}
}

func BenchmarkGetAllPMStates(b *testing.B) {
	for i := 0; i < b.N; i++ {
		_ = GetAllPMStates()
	}
}
