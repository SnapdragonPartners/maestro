package claude

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// mockCaptureExecutor captures commands and returns configurable responses.
type mockCaptureExecutor struct {
	mu              sync.Mutex
	capturedCmds    [][]string
	claudeCommand   []string // The main "claude" command (most recent)
	callCount       int
	claudeCallCount int
}

func newMockCaptureExecutor() *mockCaptureExecutor {
	return &mockCaptureExecutor{
		capturedCmds: make([][]string, 0),
	}
}

func (m *mockCaptureExecutor) Name() exec.ExecutorType {
	return "mock"
}

func (m *mockCaptureExecutor) Available() bool {
	return true
}

func (m *mockCaptureExecutor) Run(_ context.Context, cmd []string, _ *exec.Opts) (exec.Result, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.callCount++
	m.capturedCmds = append(m.capturedCmds, cmd)

	// Determine what type of command this is and return appropriate response
	if len(cmd) == 0 {
		return exec.Result{ExitCode: 1, Stderr: "empty command"}, nil
	}

	switch {
	// Node/npm/claude version checks - return "installed"
	case cmd[0] == "node" && len(cmd) > 1 && cmd[1] == "--version":
		return exec.Result{ExitCode: 0, Stdout: "v20.0.0"}, nil

	case cmd[0] == "npm" && len(cmd) > 1 && cmd[1] == "--version":
		return exec.Result{ExitCode: 0, Stdout: "10.0.0"}, nil

	case cmd[0] == "claude" && len(cmd) > 1 && cmd[1] == "--version":
		return exec.Result{ExitCode: 0, Stdout: "claude-code 1.0.0"}, nil

	// User creation check (id command)
	case cmd[0] == "id":
		return exec.Result{ExitCode: 0, Stdout: "uid=1000(coder) gid=1000(coder)"}, nil

	// MCP proxy check (test -x)
	case cmd[0] == "test" || (cmd[0] == "sh" && strings.Contains(strings.Join(cmd, " "), "test -x")):
		return exec.Result{ExitCode: 0}, nil

	// Shell commands (for writing MCP config, etc.)
	case cmd[0] == "sh":
		return exec.Result{ExitCode: 0}, nil

	// Main Claude Code command
	case cmd[0] == "claude":
		m.claudeCommand = cmd
		m.claudeCallCount++
		// Return a valid "done" signal response
		doneJSON := `{"type":"result","subtype":"success","cost_usd":0.01,"duration_ms":1000,"duration_api_ms":900,"is_error":false,"num_turns":1,"result":"Task completed successfully","session_id":"test-session-123"}`
		return exec.Result{
			ExitCode: 0,
			Stdout:   doneJSON,
		}, nil

	default:
		// Unknown command - return success to avoid blocking
		return exec.Result{ExitCode: 0}, nil
	}
}

func (m *mockCaptureExecutor) GetClaudeCommand() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claudeCommand
}

func (m *mockCaptureExecutor) GetClaudeCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.claudeCallCount
}

// minimalToolProvider creates a tool provider for testing.
func minimalToolProvider() *tools.ToolProvider {
	// Create an empty tool provider - the MCP server will start but have no tools
	// This is sufficient for testing the command flow
	ctx := &tools.AgentContext{
		WorkDir: "/workspace",
		AgentID: "test-agent",
	}
	return tools.NewProvider(ctx, []string{})
}

// TestSessionResumeIntegration_NewSession tests that a new session generates and passes session ID.
func TestSessionResumeIntegration_NewSession(t *testing.T) {
	mockExec := newMockCaptureExecutor()
	logger := logx.NewLogger("test")
	toolProvider := minimalToolProvider()

	runner := NewRunner(mockExec, "test-container", toolProvider, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := DefaultRunOptions()
	opts.Mode = ModeCoding
	opts.Model = "claude-sonnet-4-20250514"
	opts.InitialInput = "Implement the feature"
	opts.SystemPrompt = "You are a coder"
	opts.TotalTimeout = 10 * time.Second

	result, err := runner.Run(ctx, &opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify session ID was generated and returned
	if result.SessionID == "" {
		t.Error("expected session ID to be generated, got empty string")
	}

	// Verify the claude command was called
	claudeCmd := mockExec.GetClaudeCommand()
	if len(claudeCmd) == 0 {
		t.Fatal("expected claude command to be captured")
	}

	// Verify --session-id flag is present
	cmdStr := strings.Join(claudeCmd, " ")
	if !strings.Contains(cmdStr, "--session-id") {
		t.Errorf("expected --session-id flag in command, got: %s", cmdStr)
	}

	// Verify session ID in command matches result
	sessionIDIndex := -1
	for i, arg := range claudeCmd {
		if arg == "--session-id" && i+1 < len(claudeCmd) {
			sessionIDIndex = i + 1
			break
		}
	}
	if sessionIDIndex == -1 {
		t.Fatal("--session-id flag found but no value after it")
	}
	if claudeCmd[sessionIDIndex] != result.SessionID {
		t.Errorf("session ID mismatch: command has %q, result has %q",
			claudeCmd[sessionIDIndex], result.SessionID)
	}

	// Verify --resume is NOT present (new session)
	if strings.Contains(cmdStr, "--resume") {
		t.Errorf("expected no --resume flag for new session, got: %s", cmdStr)
	}

	// Verify system prompt is present (new session)
	if !strings.Contains(cmdStr, "--append-system-prompt") {
		t.Errorf("expected --append-system-prompt for new session, got: %s", cmdStr)
	}

	// Verify initial input is present
	if !strings.Contains(cmdStr, "Implement the feature") {
		t.Errorf("expected initial input in command, got: %s", cmdStr)
	}
}

// TestSessionResumeIntegration_ResumeSession tests resuming an existing session.
func TestSessionResumeIntegration_ResumeSession(t *testing.T) {
	mockExec := newMockCaptureExecutor()
	logger := logx.NewLogger("test")
	toolProvider := minimalToolProvider()

	runner := NewRunner(mockExec, "test-container", toolProvider, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Simulate resuming from a previous session
	existingSessionID := "existing-session-abc-123"

	opts := DefaultRunOptions()
	opts.Mode = ModeCoding
	opts.Model = "claude-sonnet-4-20250514"
	opts.SessionID = existingSessionID
	opts.Resume = true
	opts.ResumeInput = "Tests failed. Please fix the compilation errors."
	opts.SystemPrompt = "This should be ignored for resume"
	opts.InitialInput = "This should also be ignored"
	opts.TotalTimeout = 10 * time.Second

	result, err := runner.Run(ctx, &opts)
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Verify session ID is preserved
	if result.SessionID != existingSessionID {
		t.Errorf("expected session ID %q, got %q", existingSessionID, result.SessionID)
	}

	claudeCmd := mockExec.GetClaudeCommand()
	if len(claudeCmd) == 0 {
		t.Fatal("expected claude command to be captured")
	}

	cmdStr := strings.Join(claudeCmd, " ")

	// Verify --session-id flag is NOT present (session ID is passed as arg to --resume)
	if strings.Contains(cmdStr, "--session-id") {
		t.Errorf("expected no --session-id flag (session ID goes to --resume), got: %s", cmdStr)
	}

	// Verify --resume flag IS present with session ID as its argument
	// In print mode, syntax is: --resume <session-id>
	if !strings.Contains(cmdStr, "--resume "+existingSessionID) {
		t.Errorf("expected --resume with session ID as argument, got: %s", cmdStr)
	}

	// Verify system prompt is NOT present (resume session)
	if strings.Contains(cmdStr, "--append-system-prompt") {
		t.Errorf("expected no --append-system-prompt for resume session, got: %s", cmdStr)
	}

	// Verify resume input is present (not initial input)
	if !strings.Contains(cmdStr, "Tests failed") {
		t.Errorf("expected resume input in command, got: %s", cmdStr)
	}
	if strings.Contains(cmdStr, "This should also be ignored") {
		t.Errorf("expected initial input to be ignored for resume, got: %s", cmdStr)
	}
}

// TestSessionResumeIntegration_SequentialSessions tests the full flow of new session followed by resume.
func TestSessionResumeIntegration_SequentialSessions(t *testing.T) {
	mockExec := newMockCaptureExecutor()
	logger := logx.NewLogger("test")
	toolProvider := minimalToolProvider()

	runner := NewRunner(mockExec, "test-container", toolProvider, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// First call: New session
	opts1 := DefaultRunOptions()
	opts1.Mode = ModeCoding
	opts1.InitialInput = "Start the task"
	opts1.SystemPrompt = "You are helpful"
	opts1.TotalTimeout = 10 * time.Second

	result1, err := runner.Run(ctx, &opts1)
	if err != nil {
		t.Fatalf("First run failed: %v", err)
	}

	firstSessionID := result1.SessionID
	if firstSessionID == "" {
		t.Fatal("expected session ID from first run")
	}

	firstClaudeCallCount := mockExec.GetClaudeCallCount()
	if firstClaudeCallCount != 1 {
		t.Errorf("expected 1 claude call after first run, got %d", firstClaudeCallCount)
	}

	// Second call: Resume with feedback
	opts2 := DefaultRunOptions()
	opts2.Mode = ModeCoding
	opts2.SessionID = firstSessionID
	opts2.Resume = true
	opts2.ResumeInput = "Build failed. Fix the errors."
	opts2.TotalTimeout = 10 * time.Second

	result2, err := runner.Run(ctx, &opts2)
	if err != nil {
		t.Fatalf("Second run failed: %v", err)
	}

	// Verify session ID is preserved
	if result2.SessionID != firstSessionID {
		t.Errorf("expected session ID %q to be preserved, got %q", firstSessionID, result2.SessionID)
	}

	secondClaudeCallCount := mockExec.GetClaudeCallCount()
	if secondClaudeCallCount != 2 {
		t.Errorf("expected 2 claude calls after second run, got %d", secondClaudeCallCount)
	}

	// Verify the second command has --resume
	claudeCmd := mockExec.GetClaudeCommand()
	cmdStr := strings.Join(claudeCmd, " ")
	if !strings.Contains(cmdStr, "--resume") {
		t.Errorf("expected --resume in second command, got: %s", cmdStr)
	}
	if !strings.Contains(cmdStr, "Build failed") {
		t.Errorf("expected resume input in second command, got: %s", cmdStr)
	}
}
