package coder

import (
	"reflect"
	"sort"
	"testing"

	"orchestrator/pkg/proto"
)

// TestGetAllCoderStates verifies that all states are correctly derived from ValidCoderTransitions
func TestGetAllCoderStates(t *testing.T) {
	derivedStates := GetAllCoderStates()

	// Expected states based on current CoderTransitions map
	expectedStates := []proto.State{
		StatePlanning, StateCoding, StateTesting, StateFixing,
		StatePlanReview, StateCodeReview, StateQuestion,
	}

	// Sort expected states for comparison
	sort.Slice(expectedStates, func(i, j int) bool {
		return string(expectedStates[i]) < string(expectedStates[j])
	})

	if len(derivedStates) != len(expectedStates) {
		t.Errorf("Expected %d states, got %d", len(expectedStates), len(derivedStates))
		t.Logf("Expected: %v", expectedStates)
		t.Logf("Derived: %v", derivedStates)
	}

	// Convert to string slices for easier comparison
	expectedStrings := make([]string, len(expectedStates))
	derivedStrings := make([]string, len(derivedStates))

	for i, state := range expectedStates {
		expectedStrings[i] = string(state)
	}
	for i, state := range derivedStates {
		derivedStrings[i] = string(state)
	}

	if !reflect.DeepEqual(expectedStrings, derivedStrings) {
		t.Errorf("Derived states don't match expected")
		t.Logf("Expected: %v", expectedStrings)
		t.Logf("Derived: %v", derivedStrings)
	}
}

// TestGetAllCoderStates_Deterministic verifies that the function returns consistent results
func TestGetAllCoderStates_Deterministic(t *testing.T) {
	states1 := GetAllCoderStates()
	states2 := GetAllCoderStates()

	if len(states1) != len(states2) {
		t.Errorf("Function is not deterministic: got different lengths %d vs %d", len(states1), len(states2))
	}

	for i, state := range states1 {
		if i >= len(states2) || state != states2[i] {
			t.Errorf("Function is not deterministic: state mismatch at index %d", i)
			break
		}
	}
}

// TestIsCoderState verifies state validation functionality
func TestIsCoderState(t *testing.T) {
	testCases := []struct {
		state    string
		expected bool
		desc     string
	}{
		{"PLANNING", true, "Valid coder state"},
		{"CODING", true, "Valid coder state"},
		{"TESTING", true, "Valid coder state"},
		{"FIXING", true, "Valid coder state"},
		{"PLAN_REVIEW", true, "Valid coder state"},
		{"CODE_REVIEW", true, "Valid coder state"},
		{"QUESTION", true, "Valid coder state"},
		{"WAITING", false, "Base agent state"},
		{"DONE", false, "Base agent state"},
		{"ERROR", false, "Base agent state"},
		{"INVALID", false, "Non-existent state"},
		{"", false, "Empty string"},
		{"planning", false, "Wrong case"},
	}

	for _, tc := range testCases {
		result := IsCoderState(proto.State(tc.state))
		if result != tc.expected {
			t.Errorf("IsCoderState(%q) = %v, expected %v (%s)", tc.state, result, tc.expected, tc.desc)
		}
	}
}

// TestGetCoderStateConstants - REMOVED
// This test is obsolete because GetCoderStateConstants function was removed
// State constants are now directly defined in coder_fsm.go

// TestDriverIsCoderState verifies the IsCoderState function uses dynamic derivation
func TestDriverIsCoderState(t *testing.T) {
	// Test all dynamically derived states
	derivedStates := GetAllCoderStates()
	for _, agentState := range derivedStates {
		if !IsCoderState(agentState) {
			t.Errorf("Driver should recognize %q as a coder state", string(agentState))
		}
	}

	// Test some non-coder states
	nonCoderStates := []string{"WAITING", "DONE", "ERROR", "INVALID"}
	for _, stateStr := range nonCoderStates {
		agentState := proto.State(stateStr)
		if IsCoderState(agentState) {
			t.Errorf("Driver should not recognize %q as a coder state", stateStr)
		}
	}
}

// TestStateDerivation_NewStateAddition - REMOVED
// This test is obsolete because CoderTransitions is now immutable and canonical
// No longer supports runtime modification of transition map

// TestStateDerivation_EmptyTransitions - REMOVED
// This test is obsolete because CoderTransitions is now immutable and canonical
// No longer supports runtime modification of transition map

// TestStateDerivation_Performance verifies the functions perform well
func TestStateDerivation_Performance(t *testing.T) {
	// Run derivation multiple times to ensure it's reasonably fast
	const iterations = 1000

	for i := 0; i < iterations; i++ {
		states := GetAllCoderStates()
		if len(states) == 0 {
			t.Error("Should derive non-empty state list")
			break
		}
	}

	// Test IsCoderState performance
	for i := 0; i < iterations; i++ {
		if !IsCoderState("PLANNING") {
			t.Error("Should recognize PLANNING as coder state")
			break
		}
	}
}
