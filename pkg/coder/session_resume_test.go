package coder

import (
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// TestSessionResumeKeys verifies that the session resume state data keys are defined.
func TestSessionResumeKeys(t *testing.T) {
	// Verify KeyCodingSessionID constant
	if KeyCodingSessionID != "coding_session_id" {
		t.Errorf("expected KeyCodingSessionID to be 'coding_session_id', got: %s", KeyCodingSessionID)
	}

	// Verify KeyResumeInput constant
	if KeyResumeInput != "resume_input" {
		t.Errorf("expected KeyResumeInput to be 'resume_input', got: %s", KeyResumeInput)
	}
}

// TestSessionIDStorageInStateMachine verifies that session IDs can be stored and retrieved.
func TestSessionIDStorageInStateMachine(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", proto.StateWaiting, nil, nil)

	// Store a session ID
	testSessionID := "a1b2c3d4-e5f6-7890-abcd-ef1234567890"
	sm.SetStateData(KeyCodingSessionID, testSessionID)

	// Retrieve and verify
	value, exists := sm.GetStateValue(KeyCodingSessionID)
	if !exists {
		t.Fatal("KeyCodingSessionID was not stored")
	}

	sessionID, ok := value.(string)
	if !ok {
		t.Fatalf("expected string, got: %T", value)
	}

	if sessionID != testSessionID {
		t.Errorf("expected session ID %q, got: %q", testSessionID, sessionID)
	}
}

// TestResumeInputStorageInStateMachine verifies that resume input can be stored and retrieved.
func TestResumeInputStorageInStateMachine(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", proto.StateWaiting, nil, nil)

	// Store resume input (simulating test failure feedback)
	testInput := "Tests failed with 3 errors:\n1. TestFoo failed\n2. TestBar failed\n3. TestBaz failed"
	sm.SetStateData(KeyResumeInput, testInput)

	// Retrieve and verify
	value, exists := sm.GetStateValue(KeyResumeInput)
	if !exists {
		t.Fatal("KeyResumeInput was not stored")
	}

	resumeInput, ok := value.(string)
	if !ok {
		t.Fatalf("expected string, got: %T", value)
	}

	if resumeInput != testInput {
		t.Errorf("expected resume input %q, got: %q", testInput, resumeInput)
	}
}

// TestResumeInputClearing verifies that resume input can be cleared after use.
func TestResumeInputClearing(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", proto.StateWaiting, nil, nil)

	// Store resume input
	sm.SetStateData(KeyResumeInput, "some feedback")

	// Clear it (as done in handleClaudeCodeCoding after using the input)
	sm.SetStateData(KeyResumeInput, nil)

	// Verify it's cleared
	value, exists := sm.GetStateValue(KeyResumeInput)
	if exists && value != nil {
		t.Error("KeyResumeInput should be cleared after setting to nil")
	}
}

// TestSessionResumeCondition tests the condition for resuming a session.
func TestSessionResumeCondition(t *testing.T) {
	tests := []struct {
		name          string
		sessionID     string
		resumeInput   string
		shouldResume  bool
	}{
		{
			name:          "both present - should resume",
			sessionID:     "test-session-123",
			resumeInput:   "Tests failed, please fix",
			shouldResume:  true,
		},
		{
			name:          "only session ID - should not resume",
			sessionID:     "test-session-123",
			resumeInput:   "",
			shouldResume:  false,
		},
		{
			name:          "only resume input - should not resume",
			sessionID:     "",
			resumeInput:   "Tests failed, please fix",
			shouldResume:  false,
		},
		{
			name:          "neither present - should not resume",
			sessionID:     "",
			resumeInput:   "",
			shouldResume:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, nil)

			if tc.sessionID != "" {
				sm.SetStateData(KeyCodingSessionID, tc.sessionID)
			}
			if tc.resumeInput != "" {
				sm.SetStateData(KeyResumeInput, tc.resumeInput)
			}

			// Get values (simulating handleClaudeCodeCoding logic)
			existingSessionID := ""
			if val, exists := sm.GetStateValue(KeyCodingSessionID); exists {
				if s, ok := val.(string); ok {
					existingSessionID = s
				}
			}

			resumeInput := ""
			if val, exists := sm.GetStateValue(KeyResumeInput); exists {
				if s, ok := val.(string); ok {
					resumeInput = s
				}
			}

			shouldResume := existingSessionID != "" && resumeInput != ""

			if shouldResume != tc.shouldResume {
				t.Errorf("expected shouldResume=%v, got %v", tc.shouldResume, shouldResume)
			}
		})
	}
}

// TestResumeInputFromTestFailure simulates TESTING → CODING transition with resume input.
func TestResumeInputFromTestFailure(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", StateTesting, nil, nil)

	// Store existing session ID (set during first CODING pass)
	sm.SetStateData(KeyCodingSessionID, "session-abc-123")

	// Simulate test failure - this is what handleTesting does
	testFailureMessage := "Build failed:\n  pkg/foo/bar.go:42:13: undefined: someFunc\n\nPlease fix these compilation errors."
	sm.SetStateData(KeyResumeInput, testFailureMessage)

	// Verify session ID still exists
	if val, exists := sm.GetStateValue(KeyCodingSessionID); !exists {
		t.Fatal("session ID should be preserved")
	} else if val != "session-abc-123" {
		t.Errorf("session ID changed unexpectedly: %v", val)
	}

	// Verify resume input is set
	if val, exists := sm.GetStateValue(KeyResumeInput); !exists {
		t.Fatal("resume input should be set")
	} else if val != testFailureMessage {
		t.Errorf("resume input mismatch: got %v", val)
	}
}

// TestResumeInputFromCodeReview simulates CODE_REVIEW → CODING transition with architect feedback.
func TestResumeInputFromCodeReview(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", StateCodeReview, nil, nil)

	// Store existing session ID
	sm.SetStateData(KeyCodingSessionID, "session-def-456")

	// Simulate architect feedback - this is what processApprovalResult does for needs_changes
	feedbackMessage := "Code review feedback - changes requested:\n\nPlease add error handling for the edge case when input is nil.\n\nPlease address these issues and continue implementation."
	sm.SetStateData(KeyResumeInput, feedbackMessage)

	// Verify both are set correctly
	sessionID, exists := sm.GetStateValue(KeyCodingSessionID)
	if !exists || sessionID != "session-def-456" {
		t.Fatal("session ID should be preserved")
	}

	resumeInput, exists := sm.GetStateValue(KeyResumeInput)
	if !exists || resumeInput != feedbackMessage {
		t.Fatal("resume input should contain architect feedback")
	}
}

// TestResumeInputFromPrepareMerge simulates PREPARE_MERGE → CODING transition on rebase conflict.
func TestResumeInputFromPrepareMerge(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", StatePrepareMerge, nil, nil)

	// Store existing session ID
	sm.SetStateData(KeyCodingSessionID, "session-ghi-789")

	// Simulate rebase conflict feedback - this is what handlePrepareMerge does
	rebaseMessage := "Git push failed with non-fast-forward error. Auto-rebase was performed successfully.\n\nYour branch has been rebased onto the latest main. Please review any changes and proceed with implementation."
	sm.SetStateData(KeyResumeInput, rebaseMessage)

	// Verify both are set correctly
	sessionID, exists := sm.GetStateValue(KeyCodingSessionID)
	if !exists || sessionID != "session-ghi-789" {
		t.Fatal("session ID should be preserved")
	}

	resumeInput, exists := sm.GetStateValue(KeyResumeInput)
	if !exists || resumeInput != rebaseMessage {
		t.Fatal("resume input should contain rebase information")
	}
}

// TestSessionIDPersistsInStateData verifies session ID persists in state data.
// Note: State data persists regardless of state transitions - this tests the basic persistence.
func TestSessionIDPersistsInStateData(t *testing.T) {
	sm := agent.NewBaseStateMachine("test-coder", StateCoding, nil, nil)

	// Set session ID
	sessionID := "persistent-session-xyz"
	sm.SetStateData(KeyCodingSessionID, sessionID)

	// Session ID should persist in state data
	val, exists := sm.GetStateValue(KeyCodingSessionID)
	if !exists || val != sessionID {
		t.Errorf("session ID should persist in state data, got: %v", val)
	}

	// Set additional data (simulating what happens across transitions)
	sm.SetStateData(KeyTestOutput, "test output")
	sm.SetStateData(KeyCodeReviewCompletedAt, "2024-01-01T00:00:00Z")

	// Session ID should still be there
	val, exists = sm.GetStateValue(KeyCodingSessionID)
	if !exists || val != sessionID {
		t.Errorf("session ID should persist after other data is set, got: %v", val)
	}

	// Setting resume input shouldn't affect session ID
	sm.SetStateData(KeyResumeInput, "feedback from architect")

	val, exists = sm.GetStateValue(KeyCodingSessionID)
	if !exists || val != sessionID {
		t.Errorf("session ID should persist after resume input is set, got: %v", val)
	}
}
