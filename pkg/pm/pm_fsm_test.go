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
		{StateWorking, "WORKING"},
		{StateAwaitUser, "AWAIT_USER"},
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
		{StateWaiting, StateWaiting, "WAITING -> WAITING (polling for state changes)"},
		{StateWaiting, StateAwaitUser, "WAITING -> AWAIT_USER (interview starts)"},
		{StateWaiting, StatePreview, "WAITING -> PREVIEW (spec file upload)"},
		{StateWaiting, proto.StateDone, "WAITING -> DONE (shutdown)"},

		// WORKING transitions
		{StateWorking, StateWorking, "WORKING -> WORKING (continue working)"},
		{StateWorking, StateAwaitUser, "WORKING -> AWAIT_USER (needs user input)"},
		{StateWorking, StatePreview, "WORKING -> PREVIEW (spec ready for review)"},
		{StateWorking, proto.StateError, "WORKING -> ERROR (work failed)"},
		{StateWorking, proto.StateDone, "WORKING -> DONE (shutdown)"},

		// AWAIT_USER transitions
		{StateAwaitUser, StateAwaitUser, "AWAIT_USER -> AWAIT_USER (still waiting)"},
		{StateAwaitUser, StateWorking, "AWAIT_USER -> WORKING (user responded)"},
		{StateAwaitUser, proto.StateError, "AWAIT_USER -> ERROR (timeout/error)"},
		{StateAwaitUser, proto.StateDone, "AWAIT_USER -> DONE (shutdown)"},

		// PREVIEW transitions
		{StatePreview, StatePreview, "PREVIEW -> PREVIEW (waiting for valid action)"},
		{StatePreview, StateAwaitUser, "PREVIEW -> AWAIT_USER (continue interview)"},
		{StatePreview, StateAwaitArchitect, "PREVIEW -> AWAIT_ARCHITECT (submit to architect)"},
		{StatePreview, proto.StateError, "PREVIEW -> ERROR (error)"},
		{StatePreview, proto.StateDone, "PREVIEW -> DONE (shutdown)"},

		// AWAIT_ARCHITECT transitions
		{StateAwaitArchitect, StateAwaitArchitect, "AWAIT_ARCHITECT -> AWAIT_ARCHITECT (still waiting)"},
		{StateAwaitArchitect, StateWorking, "AWAIT_ARCHITECT -> WORKING (architect feedback)"},
		{StateAwaitArchitect, StateWaiting, "AWAIT_ARCHITECT -> WAITING (architect approved)"},
		{StateAwaitArchitect, proto.StateError, "AWAIT_ARCHITECT -> ERROR (error)"},
		{StateAwaitArchitect, proto.StateDone, "AWAIT_ARCHITECT -> DONE (shutdown)"},

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
		{StateWaiting, proto.StateError, "WAITING -> ERROR (invalid)"},

		// Invalid AWAIT_USER transitions
		{StateAwaitUser, StateWaiting, "AWAIT_USER -> WAITING (invalid - must go through WORKING)"},

		// Invalid DONE transitions (DONE should only transition to WAITING via restart policy)
		{proto.StateDone, StateWorking, "DONE -> WORKING (invalid)"},
		{proto.StateDone, StateAwaitUser, "DONE -> AWAIT_USER (invalid)"},
		{proto.StateDone, proto.StateError, "DONE -> ERROR (invalid)"},

		// Invalid ERROR transitions
		{proto.StateError, StateWorking, "ERROR -> WORKING (invalid)"},
		{proto.StateError, StateAwaitUser, "ERROR -> AWAIT_USER (invalid)"},
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
		StateWorking,
		StateAwaitUser,
		StatePreview,
		StateAwaitArchitect,
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
		{StateWorking, false},
		{StateAwaitUser, false},
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
		StateWorking,
		StateAwaitUser,
		StatePreview,
		StateAwaitArchitect,
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
		{StateWaiting, []proto.State{StateWaiting, StateWorking, StateAwaitUser, StatePreview, proto.StateDone}},
		{StateWorking, []proto.State{StateWorking, StateAwaitUser, StatePreview, proto.StateError, proto.StateDone}},
		{StateAwaitUser, []proto.State{StateAwaitUser, StateWorking, proto.StateError, proto.StateDone}},
		{StatePreview, []proto.State{StatePreview, StateAwaitUser, StateAwaitArchitect, proto.StateError, proto.StateDone}},
		{StateAwaitArchitect, []proto.State{StateAwaitArchitect, StateWorking, StateWaiting, proto.StateError, proto.StateDone}},
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
	// Simulate a typical interview flow with preview and architect review
	transitions := []struct {
		from proto.State
		to   proto.State
		desc string
	}{
		{StateWaiting, StateAwaitUser, "PM asks initial question"},
		{StateAwaitUser, StateWorking, "User responds"},
		{StateWorking, StateAwaitUser, "PM asks follow-up"},
		{StateAwaitUser, StateWorking, "User responds again"},
		{StateWorking, StatePreview, "PM generates spec for preview"},
		{StatePreview, StateAwaitArchitect, "User submits for development"},
		{StateAwaitArchitect, StateWaiting, "Architect approves spec"},
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
	// Test error during working
	if !IsValidPMTransition(StateWorking, proto.StateError) {
		t.Error("Should be able to transition from WORKING to ERROR")
	}
	if !IsValidPMTransition(proto.StateError, StateWaiting) {
		t.Error("Should be able to recover from ERROR to WAITING")
	}

	// Test error during await
	if !IsValidPMTransition(StateAwaitUser, proto.StateError) {
		t.Error("Should be able to transition from AWAIT_USER to ERROR")
	}
}

// TestPMIterativeConversation tests back-and-forth conversation flow.
func TestPMIterativeConversation(t *testing.T) {
	// This tests the iterative conversation where PM asks multiple questions
	// and eventually goes through preview and architect approval
	transitions := []struct {
		from proto.State
		to   proto.State
		desc string
	}{
		{StateWaiting, StateAwaitUser, "Start interview"},
		{StateAwaitUser, StateWorking, "User responds"},
		{StateWorking, StateAwaitUser, "PM asks another question"},
		{StateAwaitUser, StateWorking, "User responds again"},
		{StateWorking, StateAwaitUser, "PM asks final question"},
		{StateAwaitUser, StateWorking, "User provides final input"},
		{StateWorking, StatePreview, "PM generates spec for preview"},
		{StatePreview, StateAwaitArchitect, "User submits to architect"},
		{StateAwaitArchitect, StateWaiting, "Architect approves spec"},
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
		IsValidPMTransition(StateWaiting, StateWorking)
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
