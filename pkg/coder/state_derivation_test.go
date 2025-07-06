package coder

import (
	"reflect"
	"sort"
	"testing"
	
	"orchestrator/pkg/agent"
)

// TestGetAllCoderStates verifies that all states are correctly derived from ValidCoderTransitions
func TestGetAllCoderStates(t *testing.T) {
	derivedStates := GetAllCoderStates()
	
	// Expected states based on current ValidCoderTransitions map
	expectedStates := []CoderState{
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
		result := IsCoderState(tc.state)
		if result != tc.expected {
			t.Errorf("IsCoderState(%q) = %v, expected %v (%s)", tc.state, result, tc.expected, tc.desc)
		}
	}
}

// TestGetCoderStateConstants verifies dynamic constants map generation
func TestGetCoderStateConstants(t *testing.T) {
	constants := GetCoderStateConstants()
	derivedStates := GetAllCoderStates()
	
	// Should have same number of constants as derived states
	if len(constants) != len(derivedStates) {
		t.Errorf("Expected %d constants, got %d", len(derivedStates), len(constants))
	}
	
	// Verify each derived state has a corresponding constant
	for _, state := range derivedStates {
		stateStr := string(state)
		constant, exists := constants[stateStr]
		
		if !exists {
			t.Errorf("Missing constant for state %q", stateStr)
			continue
		}
		
		if constant != state {
			t.Errorf("Constant mismatch for %q: got %q, expected %q", stateStr, string(constant), stateStr)
		}
	}
	
	// Verify all constants map to valid states
	for name, constant := range constants {
		if string(constant) != name {
			t.Errorf("Constant name mismatch: key=%q, value=%q", name, string(constant))
		}
		
		if !IsCoderState(name) {
			t.Errorf("Constant %q is not a valid coder state", name)
		}
	}
}

// TestDriverIsCoderState verifies the driver's isCoderState method uses dynamic derivation
func TestDriverIsCoderState(t *testing.T) {
	driver := &CoderDriver{}
	
	// Test all dynamically derived states
	derivedStates := GetAllCoderStates()
	for _, state := range derivedStates {
		agentState := state.ToAgentState()
		if !driver.isCoderState(agentState) {
			t.Errorf("Driver should recognize %q as a coder state", string(state))
		}
	}
	
	// Test some non-coder states
	nonCoderStates := []string{"WAITING", "DONE", "ERROR", "INVALID"}
	for _, stateStr := range nonCoderStates {
		agentState := agent.State(stateStr)
		if driver.isCoderState(agentState) {
			t.Errorf("Driver should not recognize %q as a coder state", stateStr)
		}
	}
}

// TestStateDerivation_NewStateAddition tests that adding new states works automatically
func TestStateDerivation_NewStateAddition(t *testing.T) {
	// Save original transitions
	originalTransitions := make(map[CoderState][]CoderState)
	for k, v := range ValidCoderTransitions {
		originalTransitions[k] = append([]CoderState{}, v...)
	}
	
	// Define a new test state
	testState := CoderState("TEST_STATE")
	
	// Add new state to transitions map temporarily
	ValidCoderTransitions[testState] = []CoderState{StatePlanning}
	ValidCoderTransitions[StatePlanning] = append(ValidCoderTransitions[StatePlanning], testState)
	
	defer func() {
		// Restore original transitions
		ValidCoderTransitions = originalTransitions
	}()
	
	// Verify the new state is automatically detected
	derivedStates := GetAllCoderStates()
	found := false
	for _, state := range derivedStates {
		if state == testState {
			found = true
			break
		}
	}
	
	if !found {
		t.Errorf("New state %q was not automatically derived", string(testState))
	}
	
	// Verify IsCoderState recognizes the new state
	if !IsCoderState(string(testState)) {
		t.Errorf("IsCoderState should recognize new state %q", string(testState))
	}
}

// TestStateDerivation_EmptyTransitions tests edge case of empty transitions
func TestStateDerivation_EmptyTransitions(t *testing.T) {
	// Save original transitions
	originalTransitions := make(map[CoderState][]CoderState)
	for k, v := range ValidCoderTransitions {
		originalTransitions[k] = append([]CoderState{}, v...)
	}
	
	// Temporarily set empty transitions
	ValidCoderTransitions = make(map[CoderState][]CoderState)
	
	defer func() {
		// Restore original transitions
		ValidCoderTransitions = originalTransitions
	}()
	
	// Should return empty slice
	derivedStates := GetAllCoderStates()
	if len(derivedStates) != 0 {
		t.Errorf("Expected empty state list, got %d states: %v", len(derivedStates), derivedStates)
	}
	
	// Should return false for any state
	if IsCoderState("PLANNING") {
		t.Error("Should not recognize any state when transitions are empty")
	}
}

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

