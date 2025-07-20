package coder

import (
	"testing"

	"orchestrator/pkg/proto"
)

func TestSetupStateTransitions(t *testing.T) {
	// Test WAITING → SETUP
	if !IsValidCoderTransition(proto.StateWaiting, StateSetup) {
		t.Error("WAITING → SETUP should be valid")
	}

	// Test SETUP → PLANNING
	if !IsValidCoderTransition(StateSetup, StatePlanning) {
		t.Error("SETUP → PLANNING should be valid")
	}

	// Test SETUP → ERROR
	if !IsValidCoderTransition(StateSetup, proto.StateError) {
		t.Error("SETUP → ERROR should be valid")
	}

	// Test DONE → SETUP (non-terminal)
	if !IsValidCoderTransition(proto.StateDone, StateSetup) {
		t.Error("DONE → SETUP should be valid (non-terminal)")
	}

	// Test ERROR → SETUP (non-terminal)
	if !IsValidCoderTransition(proto.StateError, StateSetup) {
		t.Error("ERROR → SETUP should be valid (non-terminal)")
	}

	// Test invalid transitions
	if IsValidCoderTransition(proto.StateWaiting, StatePlanning) {
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

func TestBudgetReviewStateTransitions(t *testing.T) {
	// Test CODING → BUDGET_REVIEW
	if !IsValidCoderTransition(StateCoding, StateBudgetReview) {
		t.Error("CODING → BUDGET_REVIEW should be valid")
	}

	// Test FIXING → BUDGET_REVIEW
	if !IsValidCoderTransition(StateFixing, StateBudgetReview) {
		t.Error("FIXING → BUDGET_REVIEW should be valid")
	}

	// Test BUDGET_REVIEW → CODING
	if !IsValidCoderTransition(StateBudgetReview, StateCoding) {
		t.Error("BUDGET_REVIEW → CODING should be valid")
	}

	// Test BUDGET_REVIEW → FIXING
	if !IsValidCoderTransition(StateBudgetReview, StateFixing) {
		t.Error("BUDGET_REVIEW → FIXING should be valid")
	}

	// Test BUDGET_REVIEW → CODE_REVIEW
	if !IsValidCoderTransition(StateBudgetReview, StateCodeReview) {
		t.Error("BUDGET_REVIEW → CODE_REVIEW should be valid")
	}

	// Test BUDGET_REVIEW → ERROR
	if !IsValidCoderTransition(StateBudgetReview, proto.StateError) {
		t.Error("BUDGET_REVIEW → ERROR should be valid")
	}

	// Test BUDGET_REVIEW → PLANNING (now valid with planning budget)
	if !IsValidCoderTransition(StateBudgetReview, StatePlanning) {
		t.Error("BUDGET_REVIEW → PLANNING should be valid")
	}

	// Test PLANNING → BUDGET_REVIEW
	if !IsValidCoderTransition(StatePlanning, StateBudgetReview) {
		t.Error("PLANNING → BUDGET_REVIEW should be valid")
	}

	if IsValidCoderTransition(StateBudgetReview, StateTesting) {
		t.Error("BUDGET_REVIEW → TESTING should not be valid")
	}
}

func TestBudgetReviewStateInValidStates(t *testing.T) {
	validStates := GetValidStates()

	// Check that BUDGET_REVIEW is included
	found := false
	for _, state := range validStates {
		if state == StateBudgetReview {
			found = true
			break
		}
	}

	if !found {
		t.Error("BUDGET_REVIEW state should be in valid states list")
	}
}

func TestBudgetReviewStateIsCoderState(t *testing.T) {
	if !IsCoderState(StateBudgetReview) {
		t.Error("BUDGET_REVIEW should be recognized as a coder state")
	}
}
