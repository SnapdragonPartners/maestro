// Tests for code_review.go functions.
package coder

import (
	"context"
	"strings"
	"testing"

	"orchestrator/pkg/effect"
	"orchestrator/pkg/git"
	"orchestrator/pkg/proto"
)

// =============================================================================
// buildCompletionEvidence tests
// =============================================================================

func TestBuildCompletionEvidence_TestsPassed(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{
		HasWork:   true,
		Reasons:   []string{"staged changes", "untracked files"},
		Staged:    true,
		Untracked: true,
	}

	evidence := coder.buildCompletionEvidence(true, "All 5 tests passed", string(proto.StoryTypeApp), workResult, "abc123")

	if !strings.Contains(evidence, "✅ All tests passing") {
		t.Error("Expected passing test indicator")
	}
	if !strings.Contains(evidence, "All 5 tests passed") {
		t.Error("Expected test output in evidence")
	}
	if !strings.Contains(evidence, "💻 Application story") {
		t.Error("Expected app story indicator")
	}
	if !strings.Contains(evidence, "Staged changes present") {
		t.Error("Expected staged changes indicator")
	}
	if !strings.Contains(evidence, "Untracked files present") {
		t.Error("Expected untracked files indicator")
	}
}

func TestBuildCompletionEvidence_TestsFailed(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{
		HasWork:  true,
		Reasons:  []string{"unstaged changes"},
		Unstaged: true,
	}

	evidence := coder.buildCompletionEvidence(false, "Test failure output", string(proto.StoryTypeApp), workResult, "abc123")

	if !strings.Contains(evidence, "❌ Tests not run or failed") {
		t.Error("Expected failing test indicator")
	}
	if !strings.Contains(evidence, "Test failure output") {
		t.Error("Expected test output in evidence")
	}
}

func TestBuildCompletionEvidence_DevOpsStory(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{
		HasWork: true,
		Reasons: []string{"ahead of base"},
		Ahead:   true,
	}

	evidence := coder.buildCompletionEvidence(true, "", string(proto.StoryTypeDevOps), workResult, "abc123")

	if !strings.Contains(evidence, "🐳 DevOps story") {
		t.Error("Expected DevOps story indicator")
	}
	if !strings.Contains(evidence, "Container build and validation") {
		t.Error("Expected container validation message")
	}
	if !strings.Contains(evidence, "Commits ahead of base branch") {
		t.Error("Expected ahead indicator")
	}
}

func TestBuildCompletionEvidence_NoWorkRequired(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{
		HasWork: false,
		Reasons: []string{"existing implementation is correct"},
	}

	evidence := coder.buildCompletionEvidence(true, "", string(proto.StoryTypeApp), workResult, "abc123")

	if !strings.Contains(evidence, "No work required") {
		t.Error("Expected no work required indicator")
	}
	if !strings.Contains(evidence, "existing implementation is correct") {
		t.Error("Expected verification reason")
	}
	if !strings.Contains(evidence, "📝 No code changes required") {
		t.Error("Expected no changes indicator")
	}
}

func TestBuildCompletionEvidence_WithWork(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{HasWork: true, Reasons: []string{"staged changes"}}
	evidence := coder.buildCompletionEvidence(true, "", string(proto.StoryTypeApp), workResult, "abc123")

	if !strings.Contains(evidence, "📝 Code changes made") {
		t.Error("Expected code changes indicator when work detected")
	}
}

func TestBuildCompletionEvidence_NoWork(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{HasWork: false}
	evidence := coder.buildCompletionEvidence(true, "", string(proto.StoryTypeApp), workResult, "abc123")

	if !strings.Contains(evidence, "📝 No code changes required") {
		t.Error("Expected no changes indicator")
	}
}

func TestBuildCompletionEvidence_IncludesHeadSHA(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{HasWork: true}
	evidence := coder.buildCompletionEvidence(true, "", string(proto.StoryTypeApp), workResult, "deadbeef123")

	if !strings.Contains(evidence, "deadbeef123") {
		t.Error("Expected HEAD SHA in evidence")
	}
	if !strings.Contains(evidence, "Workspace HEAD") {
		t.Error("Expected Workspace HEAD label")
	}
}

// =============================================================================
// getCodeReviewContent tests
// =============================================================================

func TestGetCodeReviewContent_Basic(t *testing.T) {
	coder := createTestCoder(t, nil)

	content := coder.getCodeReviewContent(
		"Implemented feature X",
		"Tests passing",
		"0.9",
		"Original story content",
		"1. Plan step",
		"knowledge pack",
	)

	// Should return non-empty content (rendered template or fallback)
	if content == "" {
		t.Error("Expected non-empty code review content")
	}
}

func TestGetCodeReviewContent_NoRenderer(t *testing.T) {
	coder := createTestCoder(t, nil)
	coder.renderer = nil

	content := coder.getCodeReviewContent(
		"Summary",
		"Evidence",
		"0.8",
		"story",
		"plan",
		"knowledge",
	)

	// Should return fallback content
	if content == "" {
		t.Error("Expected fallback content when renderer is nil")
	}
	if !strings.Contains(content, "Summary") {
		t.Error("Fallback should contain summary")
	}
}

// =============================================================================
// getCompletionRequestContent tests
// =============================================================================

func TestGetCompletionRequestContent_Basic(t *testing.T) {
	coder := createTestCoder(t, nil)

	content := coder.getCompletionRequestContent(
		"No changes needed",
		".gitignore already contains bin/",
		"Original task",
	)

	if content == "" {
		t.Error("Expected non-empty completion request content")
	}
}

func TestGetCompletionRequestContent_NoRenderer(t *testing.T) {
	coder := createTestCoder(t, nil)
	coder.renderer = nil

	content := coder.getCompletionRequestContent(
		"summary",
		"evidence",
		"task",
	)

	if content == "" {
		t.Error("Expected fallback content when renderer is nil")
	}
	if !strings.Contains(content, "summary") {
		t.Error("Fallback should contain summary")
	}
	if !strings.Contains(content, "evidence") {
		t.Error("Fallback should contain evidence")
	}
}

// =============================================================================
// handleCodeReview tests
// =============================================================================

// Note: handleCodeReview requires longRunningExecutor for git operations.
// Full integration tests would need a mock executor. These tests are skipped
// to document the behavior while keeping the test structure for future enhancement.

func TestHandleCodeReview_RequiresExecutor(t *testing.T) {
	// handleCodeReview immediately calls git.CheckWorkDone which requires longRunningExecutor.
	// Testing this function requires either:
	// 1. A mock executor injected into the coder
	// 2. A real git repository with proper setup
	t.Skip("Requires longRunningExecutor mock - see TestHandleCodeReview_WithWorkspace for integration test")
}

// =============================================================================
// processApprovalResult tests
// =============================================================================

func TestProcessApprovalResult_Approved(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set up required state values
	sm.SetStateData(KeyWorkspacePath, t.TempDir())
	sm.SetStateData(KeyLocalBranchName, "feature-test")

	result := &effect.ApprovalResult{
		Status:   proto.ApprovalStatusApproved,
		Feedback: "LGTM",
	}

	ctx := context.Background()
	nextState, done, err := coder.processApprovalResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != StatePrepareMerge {
		t.Errorf("Expected PREPARE_MERGE state for approved, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestProcessApprovalResult_NeedsChanges(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &effect.ApprovalResult{
		Status:   proto.ApprovalStatusNeedsChanges,
		Feedback: "Please fix the formatting",
	}

	ctx := context.Background()
	nextState, done, err := coder.processApprovalResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != StateCoding {
		t.Errorf("Expected CODING state for needs_changes, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestProcessApprovalResult_Rejected(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &effect.ApprovalResult{
		Status:   proto.ApprovalStatusRejected,
		Feedback: "Story cancelled",
	}

	ctx := context.Background()
	nextState, done, err := coder.processApprovalResult(ctx, sm, result)

	if err == nil {
		t.Error("Expected error for rejected status")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state for rejected, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestProcessApprovalResult_UnknownStatus(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &effect.ApprovalResult{
		Status:   proto.ApprovalStatus("invalid_status"),
		Feedback: "",
	}

	ctx := context.Background()
	nextState, done, err := coder.processApprovalResult(ctx, sm, result)

	if err == nil {
		t.Error("Expected error for unknown status")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state for unknown, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

// =============================================================================
// Completion review tests (Phase 1 stalling fixes)
// =============================================================================

func TestProcessApprovalResult_CompletionApproved_Bookkeeping(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyCodeApprovalResult, string(proto.ApprovalTypeCompletion))

	result := &effect.ApprovalResult{
		Status:   proto.ApprovalStatusApproved,
		Feedback: "Story is complete",
	}

	ctx := context.Background()
	nextState, _, err := coder.processApprovalResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected DONE state for approved completion, got: %s", nextState)
	}
	if _, exists := sm.GetStateValue(KeyStoryCompletedAt); !exists {
		t.Error("Expected KeyStoryCompletedAt to be set on completion approval")
	}
	if status, exists := sm.GetStateValue(KeyCompletionStatus); !exists || status != "APPROVED" {
		t.Errorf("Expected KeyCompletionStatus='APPROVED', got: %v", status)
	}
}

func TestProcessApprovalResult_CompletionRejected_ErrorWithFailureInfo(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyCodeApprovalResult, string(proto.ApprovalTypeCompletion))

	result := &effect.ApprovalResult{
		Status:   proto.ApprovalStatusRejected,
		Feedback: "Story needs real implementation work",
	}

	ctx := context.Background()
	nextState, _, err := coder.processApprovalResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error (should return nil err with StateError): %v", err)
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state for rejected completion, got: %s", nextState)
	}

	fiRaw, exists := sm.GetStateValue(KeyFailureInfo)
	if !exists {
		t.Fatal("Expected KeyFailureInfo to be set on completion rejection")
	}
	fi, ok := fiRaw.(proto.FailureInfo)
	if !ok {
		t.Fatalf("Expected FailureInfo type, got: %T", fiRaw)
	}
	if fi.Kind != proto.FailureKindStoryInvalid {
		t.Errorf("Expected FailureKindStoryInvalid, got: %s", fi.Kind)
	}
	if !strings.Contains(fi.Explanation, "Story needs real implementation work") {
		t.Error("Expected rejection feedback in FailureInfo.Explanation")
	}
}

func TestBuildCompletionEvidence_NoWorkTestsNotApplicable(t *testing.T) {
	coder := createTestCoder(t, nil)

	workResult := &git.WorkDoneResult{HasWork: false}
	evidence := coder.buildCompletionEvidence(false, "", string(proto.StoryTypeApp), workResult, "")

	if !strings.Contains(evidence, "No code changes required") {
		t.Error("Expected 'No code changes required' for zero-diff completion")
	}
	if strings.Contains(evidence, "Tests not run or failed") {
		t.Error("Should not show 'Tests not run or failed' for zero-diff completion")
	}
}
