package coder

import (
	"testing"

	"orchestrator/pkg/proto"
)

func TestPlanningCompletionFromEffect_SetsApprovalRequest(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	effectData := map[string]any{
		"evidence":   "All requirements already satisfied in existing codebase",
		"confidence": "HIGH",
	}

	err := coder.processPlanningCompletionFromEffect(sm, effectData)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if coder.pendingApprovalRequest == nil {
		t.Fatal("Expected pendingApprovalRequest to be set")
	}
	if coder.pendingApprovalRequest.Type != proto.ApprovalTypeCompletion {
		t.Errorf("Expected ApprovalTypeCompletion, got: %s", coder.pendingApprovalRequest.Type)
	}

	details, exists := sm.GetStateValue(KeyCompletionDetails)
	if !exists || details != "All requirements already satisfied in existing codebase" {
		t.Errorf("Expected evidence in KeyCompletionDetails, got: %v", details)
	}
}

func TestPlanningCompletionFromEffect_RequiresEvidence(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	effectData := map[string]any{
		"confidence": "HIGH",
	}

	err := coder.processPlanningCompletionFromEffect(sm, effectData)
	if err == nil {
		t.Error("Expected error when evidence is missing")
	}
}

func TestCodingSideCompletionDoesNotSetPendingApproval(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyCompletionDetails, "Story already implemented")

	if coder.pendingApprovalRequest != nil {
		t.Error("Coding-side completion should NOT set pendingApprovalRequest (that's PLAN_REVIEW only)")
	}

	details, exists := sm.GetStateValue(KeyCompletionDetails)
	if !exists || details != "Story already implemented" {
		t.Errorf("Expected evidence stored directly in state, got: %v", details)
	}
}

func TestFSMAllowsCodingToCodeReview(t *testing.T) {
	if !IsValidCoderTransition(StateCoding, StateCodeReview) {
		t.Error("CODING → CODE_REVIEW should be a valid transition (zero-diff completion)")
	}
}

func TestFSMDisallowsCodingToPlanReview(t *testing.T) {
	if IsValidCoderTransition(StateCoding, StatePlanReview) {
		t.Error("CODING → PLAN_REVIEW should NOT be a valid transition")
	}
}

func TestFSMAllowsPlanningToPlanReview(t *testing.T) {
	if !IsValidCoderTransition(StatePlanning, StatePlanReview) {
		t.Error("PLANNING → PLAN_REVIEW should be a valid transition")
	}
}
