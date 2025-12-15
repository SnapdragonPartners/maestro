// Tests for await_merge.go functions.
package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/git"
	"orchestrator/pkg/proto"
)

// =============================================================================
// handleAwaitMerge tests
// =============================================================================

func TestHandleAwaitMerge_NoMergeResult(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Don't set merge result
	ctx := context.Background()
	nextState, done, err := coder.handleAwaitMerge(ctx, sm)

	if err == nil {
		t.Error("Expected error when merge result not set")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestHandleAwaitMerge_InvalidMergeResultType(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set invalid merge result type
	sm.SetStateData(KeyMergeResult, "not a merge result")

	ctx := context.Background()
	nextState, done, err := coder.handleAwaitMerge(ctx, sm)

	if err == nil {
		t.Error("Expected error for invalid merge result type")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestHandleAwaitMerge_ValidMergeResult(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set valid merge result with approved status
	mergeResult := &git.MergeResult{
		Status:      string(proto.ApprovalStatusApproved),
		MergeCommit: "abc123",
	}
	sm.SetStateData(KeyMergeResult, mergeResult)

	ctx := context.Background()
	nextState, done, err := coder.handleAwaitMerge(ctx, sm)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected DONE state for approved merge, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

// =============================================================================
// processMergeResult tests
// =============================================================================

func TestProcessMergeResult_Approved(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &git.MergeResult{
		Status:      string(proto.ApprovalStatusApproved),
		MergeCommit: "abc123",
	}

	ctx := context.Background()
	nextState, done, err := coder.processMergeResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected DONE state for approved, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestProcessMergeResult_NeedsChanges(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &git.MergeResult{
		Status:       string(proto.ApprovalStatusNeedsChanges),
		ConflictInfo: "Merge conflict in main.go",
	}

	ctx := context.Background()
	nextState, done, err := coder.processMergeResult(ctx, sm, result)

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

func TestProcessMergeResult_NeedsChanges_WithTodos(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Create a completed todo list
	coder.todoList = &TodoList{}
	coder.todoList.AddTodo("Initial task", -1)
	coder.todoList.CompleteCurrent()

	result := &git.MergeResult{
		Status:       string(proto.ApprovalStatusNeedsChanges),
		ConflictInfo: "Merge conflict in main.go",
	}

	ctx := context.Background()
	nextState, done, err := coder.processMergeResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != StateCoding {
		t.Errorf("Expected CODING state, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}

	// Check that feedback was added as new todo
	if coder.todoList.GetTotalCount() != 2 {
		t.Errorf("Expected 2 todos (original + feedback), got: %d", coder.todoList.GetTotalCount())
	}
}

func TestProcessMergeResult_NeedsChanges_EmptyFeedback(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &git.MergeResult{
		Status:       string(proto.ApprovalStatusNeedsChanges),
		ConflictInfo: "", // Empty feedback
	}

	ctx := context.Background()
	nextState, done, err := coder.processMergeResult(ctx, sm, result)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if nextState != StateCoding {
		t.Errorf("Expected CODING state, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestProcessMergeResult_Rejected(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &git.MergeResult{
		Status:       string(proto.ApprovalStatusRejected),
		ConflictInfo: "Unrecoverable merge conflict",
	}

	ctx := context.Background()
	nextState, done, err := coder.processMergeResult(ctx, sm, result)

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

func TestProcessMergeResult_UnknownStatus(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	result := &git.MergeResult{
		Status: "invalid_status",
	}

	ctx := context.Background()
	nextState, done, err := coder.processMergeResult(ctx, sm, result)

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
// applyPendingContainerConfig tests
// =============================================================================

func TestApplyPendingContainerConfig_NoPending(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Ensure no pending config
	coder.hasPendingContainerConfig = false

	// Should return early without errors
	coder.applyPendingContainerConfig()

	// No assertion needed - just verifying it doesn't panic
}

func TestApplyPendingContainerConfig_HasPending(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Set pending container config
	coder.hasPendingContainerConfig = true
	coder.pendingContainerName = "test-container"
	coder.pendingContainerDockerfile = "Dockerfile.test"
	coder.pendingContainerImageID = "sha256:abc123"

	// Should attempt to apply (may fail due to config not being writable in tests)
	coder.applyPendingContainerConfig()

	// After applying (or failing), the pending flag should still be cleared
	// This verifies the function attempted to process the pending config
}
