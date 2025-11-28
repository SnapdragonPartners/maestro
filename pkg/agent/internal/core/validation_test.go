package core

import (
	"context"
	"testing"

	"orchestrator/pkg/proto"
)

// TestAllowSelfLoop tests the self-loop permission function.
func TestAllowSelfLoop(t *testing.T) {
	tests := []struct {
		name   string
		from   proto.State
		to     proto.State
		expect bool
	}{
		{
			name:   "same state allowed",
			from:   proto.StateWaiting,
			to:     proto.StateWaiting,
			expect: true,
		},
		{
			name:   "different states not allowed",
			from:   proto.StateWaiting,
			to:     proto.StateDone,
			expect: false,
		},
		{
			name:   "empty states are same",
			from:   proto.State(""),
			to:     proto.State(""),
			expect: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := allowSelfLoop(tt.from, tt.to)
			if result != tt.expect {
				t.Errorf("expected %v, got %v", tt.expect, result)
			}
		})
	}
}

// TestIsValidTransition tests state transition validation.
func TestIsValidTransition(t *testing.T) {
	// Create a test transition table
	testTable := TransitionTable{
		proto.StateWaiting:     {proto.StateDone, proto.State("WORKING")},
		proto.StateDone:        {proto.StateWaiting},
		proto.State("WORKING"): {proto.StateDone},
	}

	sm := NewBaseStateMachine("test-agent", proto.StateWaiting, nil, testTable)

	tests := []struct {
		name   string
		from   proto.State
		to     proto.State
		expect bool
	}{
		{
			name:   "allowed transition",
			from:   proto.StateWaiting,
			to:     proto.StateDone,
			expect: true,
		},
		{
			name:   "disallowed transition",
			from:   proto.StateDone,
			to:     proto.State("WORKING"),
			expect: false,
		},
		{
			name:   "self-loop allowed",
			from:   proto.StateWaiting,
			to:     proto.StateWaiting,
			expect: true,
		},
		{
			name:   "transition to error always allowed",
			from:   proto.StateWaiting,
			to:     proto.StateError,
			expect: true,
		},
		{
			name:   "transition from error to working",
			from:   proto.StateError,
			to:     proto.State("WORKING"),
			expect: false, // Not in table
		},
		{
			name:   "unknown source state",
			from:   proto.State("UNKNOWN"),
			to:     proto.StateDone,
			expect: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := sm.IsValidTransition(tt.from, tt.to)
			if result != tt.expect {
				t.Errorf("expected %v for %s->%s, got %v", tt.expect, tt.from, tt.to, result)
			}
		})
	}
}

// TestCloneTransitionTable tests transition table deep copying.
func TestCloneTransitionTable(t *testing.T) {
	original := TransitionTable{
		proto.StateWaiting: {proto.StateDone, proto.State("WORKING")},
		proto.StateDone:    {proto.StateWaiting},
	}

	cloned := CloneTransitionTable(original)

	// Verify deep copy
	if len(cloned) != len(original) {
		t.Errorf("expected cloned table to have %d entries, got %d", len(original), len(cloned))
	}

	// Verify contents match
	for key, origValues := range original {
		clonedValues, exists := cloned[key]
		if !exists {
			t.Errorf("expected cloned table to have key %s", key)
			continue
		}

		if len(clonedValues) != len(origValues) {
			t.Errorf("expected %d values for key %s, got %d", len(origValues), key, len(clonedValues))
			continue
		}

		for i, origValue := range origValues {
			if clonedValues[i] != origValue {
				t.Errorf("expected value %s at index %d for key %s, got %s", origValue, i, key, clonedValues[i])
			}
		}
	}

	// Verify it's a deep copy by modifying original
	original[proto.StateWaiting] = append(original[proto.StateWaiting], proto.StateError)

	if len(cloned[proto.StateWaiting]) == len(original[proto.StateWaiting]) {
		t.Error("expected cloned table to be independent of original")
	}
}

// TestCloneEmptyTable tests cloning an empty transition table.
func TestCloneEmptyTable(t *testing.T) {
	original := TransitionTable{}
	cloned := CloneTransitionTable(original)

	if len(cloned) != 0 {
		t.Errorf("expected empty cloned table, got %d entries", len(cloned))
	}
}

// TestTransitionToWithValidation tests that TransitionTo respects validation.
func TestTransitionToWithValidation(t *testing.T) {
	testTable := TransitionTable{
		proto.StateWaiting: {proto.StateDone},
		proto.StateDone:    {proto.StateWaiting},
	}

	sm := NewBaseStateMachine("test-agent", proto.StateWaiting, nil, testTable)
	ctx := context.Background()

	// Valid transition
	err := sm.TransitionTo(ctx, proto.StateDone, nil)
	if err != nil {
		t.Errorf("expected valid transition to succeed, got error: %v", err)
	}

	if sm.GetCurrentState() != proto.StateDone {
		t.Errorf("expected current state to be DONE, got %s", sm.GetCurrentState())
	}

	// Invalid transition (DONE -> WORKING not in table)
	err = sm.TransitionTo(ctx, proto.State("WORKING"), nil)
	if err == nil {
		t.Error("expected invalid transition to fail")
	}

	// Current state should remain unchanged
	if sm.GetCurrentState() != proto.StateDone {
		t.Errorf("expected current state to remain DONE after failed transition, got %s", sm.GetCurrentState())
	}

	// Self-loop should work
	err = sm.TransitionTo(ctx, proto.StateDone, nil)
	if err != nil {
		t.Errorf("expected self-loop to succeed, got error: %v", err)
	}

	// Transition to error should always work
	err = sm.TransitionTo(ctx, proto.StateError, nil)
	if err != nil {
		t.Errorf("expected transition to ERROR to succeed, got error: %v", err)
	}

	if sm.GetCurrentState() != proto.StateError {
		t.Errorf("expected current state to be ERROR, got %s", sm.GetCurrentState())
	}
}
