package claude

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

// TestBuildCommand_NewSession tests buildCommand for a new session without resume.
func TestBuildCommand_NewSession(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		SessionID:    "test-session-123",
		SystemPrompt: "You are a helpful coder",
		InitialInput: "Implement feature X",
	}

	cmd := r.buildCommand(opts)
	cmdStr := strings.Join(cmd, " ")

	// Should include session-id flag
	if !strings.Contains(cmdStr, "--session-id test-session-123") {
		t.Errorf("expected --session-id flag, got: %s", cmdStr)
	}

	// Should include system prompt for new session
	if !strings.Contains(cmdStr, "--append-system-prompt") {
		t.Errorf("expected --append-system-prompt flag, got: %s", cmdStr)
	}

	// Should NOT include --resume flag
	if strings.Contains(cmdStr, "--resume") {
		t.Errorf("expected no --resume flag for new session, got: %s", cmdStr)
	}

	// Should use InitialInput as positional argument
	if !strings.HasSuffix(cmdStr, "-- Implement feature X") {
		t.Errorf("expected InitialInput as last positional arg, got: %s", cmdStr)
	}
}

// TestBuildCommand_Resume tests buildCommand for resuming an existing session.
func TestBuildCommand_Resume(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		SessionID:    "existing-session-456",
		Resume:       true,
		ResumeInput:  "Tests failed. Please fix the compilation errors.",
		SystemPrompt: "Should be ignored for resume",
		InitialInput: "Should also be ignored",
	}

	cmd := r.buildCommand(opts)
	cmdStr := strings.Join(cmd, " ")

	// Should include session-id flag
	if !strings.Contains(cmdStr, "--session-id existing-session-456") {
		t.Errorf("expected --session-id flag, got: %s", cmdStr)
	}

	// Should include --resume flag
	if !strings.Contains(cmdStr, "--resume") {
		t.Errorf("expected --resume flag, got: %s", cmdStr)
	}

	// Should NOT include system prompt when resuming
	if strings.Contains(cmdStr, "--append-system-prompt") {
		t.Errorf("expected no --append-system-prompt for resume, got: %s", cmdStr)
	}

	// Should use ResumeInput as positional argument
	if !strings.HasSuffix(cmdStr, "-- Tests failed. Please fix the compilation errors.") {
		t.Errorf("expected ResumeInput as last positional arg, got: %s", cmdStr)
	}
}

// TestBuildCommand_ResumeWithoutInput tests resume without additional input.
func TestBuildCommand_ResumeWithoutInput(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		SessionID:   "existing-session-789",
		Resume:      true,
		ResumeInput: "", // No additional feedback
	}

	cmd := r.buildCommand(opts)
	cmdStr := strings.Join(cmd, " ")

	// Should include --resume flag
	if !strings.Contains(cmdStr, "--resume") {
		t.Errorf("expected --resume flag, got: %s", cmdStr)
	}

	// When ResumeInput is empty, should not have trailing positional arg
	// The command should end with --resume (no -- <input>)
	if strings.HasSuffix(cmdStr, "-- ") {
		t.Errorf("expected no trailing empty positional arg, got: %s", cmdStr)
	}
}

// TestBuildCommand_ResumeRequiresSessionID tests that resume without session ID
// falls back to new session behavior.
func TestBuildCommand_ResumeRequiresSessionID(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		Resume:       true,          // Resume flag set...
		SessionID:    "",            // ...but no session ID
		ResumeInput:  "some input",
		InitialInput: "fallback input",
	}

	cmd := r.buildCommand(opts)
	cmdStr := strings.Join(cmd, " ")

	// Without SessionID, --resume should not be added
	if strings.Contains(cmdStr, "--resume") {
		t.Errorf("expected no --resume without session ID, got: %s", cmdStr)
	}

	// Should fall back to InitialInput
	if !strings.HasSuffix(cmdStr, "-- fallback input") {
		t.Errorf("expected fallback to InitialInput, got: %s", cmdStr)
	}
}

// TestBuildCommand_NoSessionID tests buildCommand without a session ID.
func TestBuildCommand_NoSessionID(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		InitialInput: "Start fresh",
	}

	cmd := r.buildCommand(opts)
	cmdStr := strings.Join(cmd, " ")

	// Should not include session-id flag when empty
	if strings.Contains(cmdStr, "--session-id") {
		t.Errorf("expected no --session-id flag when empty, got: %s", cmdStr)
	}
}

// TestBuildCommand_Model tests that model is correctly passed.
func TestBuildCommand_Model(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		Model:        "claude-sonnet-4-20250514",
		SessionID:    "test-session",
		InitialInput: "test",
	}

	cmd := r.buildCommand(opts)
	cmdStr := strings.Join(cmd, " ")

	if !strings.Contains(cmdStr, "--model claude-sonnet-4-20250514") {
		t.Errorf("expected --model flag, got: %s", cmdStr)
	}
}

// TestSessionIDFormat tests that generated session IDs are valid UUIDs.
func TestSessionIDFormat(t *testing.T) {
	// Test that uuid.New().String() produces valid format
	sessionID := uuid.New().String()

	// UUID should be 36 characters (8-4-4-4-12 with hyphens)
	if len(sessionID) != 36 {
		t.Errorf("expected session ID length 36, got %d: %s", len(sessionID), sessionID)
	}

	// Should be parseable as UUID
	_, err := uuid.Parse(sessionID)
	if err != nil {
		t.Errorf("generated session ID is not valid UUID: %s, error: %v", sessionID, err)
	}
}

// TestSessionIDUniqueness tests that each generated session ID is unique.
func TestSessionIDUniqueness(t *testing.T) {
	seen := make(map[string]bool)
	iterations := 1000

	for i := 0; i < iterations; i++ {
		sessionID := uuid.New().String()
		if seen[sessionID] {
			t.Errorf("duplicate session ID generated: %s", sessionID)
		}
		seen[sessionID] = true
	}
}

// TestBuildCommand_CommandOrder tests the order of flags in buildCommand.
func TestBuildCommand_CommandOrder(t *testing.T) {
	r := &Runner{}
	opts := &RunOptions{
		Model:        "claude-sonnet-4-20250514",
		SessionID:    "test-session",
		SystemPrompt: "You are helpful",
		InitialInput: "Do the thing",
	}

	cmd := r.buildCommand(opts)

	// First element should be "claude"
	if cmd[0] != "claude" {
		t.Errorf("expected first element to be 'claude', got: %s", cmd[0])
	}

	// Should have --print early
	printIdx := -1
	for i, arg := range cmd {
		if arg == "--print" {
			printIdx = i
			break
		}
	}
	if printIdx == -1 {
		t.Error("expected --print flag")
	}

	// -- separator should be near the end
	separatorIdx := -1
	for i, arg := range cmd {
		if arg == "--" {
			separatorIdx = i
			break
		}
	}
	if separatorIdx == -1 {
		t.Error("expected -- separator")
	}

	// Positional arg should be last
	if cmd[len(cmd)-1] != "Do the thing" {
		t.Errorf("expected last element to be InitialInput, got: %s", cmd[len(cmd)-1])
	}
}
