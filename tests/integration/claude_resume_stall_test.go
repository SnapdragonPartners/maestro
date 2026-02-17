//go:build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"orchestrator/pkg/coder/claude"
	"orchestrator/pkg/config"
	dockerexec "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// longNeedsChangesFeedback is the exact pattern of feedback that caused Claude Code
// to hang in production: a multi-paragraph NEEDS_CHANGES review with backticks,
// numbered lists, and code references (~1600 chars).
const longNeedsChangesFeedback = `Plan review feedback - changes requested:

NEEDS_CHANGES. The plan mostly matches the story, but it introduces requirements/changes that are not in scope and also contains a likely mismatch with the already-implemented templates/feedback.html.

Issues to address:
1) Template/data contract mismatch (Acceptance: handler passes correct FeedbackData fields). Your plan says the template expects a Percentage field and that FeedbackData includes Percentage. However, the existing templates/feedback.html (from the prior story) computes the percentage internally from .TimesCorrect/.TimesAnswered and does not reference .Percentage. Either:
   - update the feedback template to use .Percentage (and adjust acceptance accordingly in this story's scope), or
   - remove Percentage from FeedbackData and still compute it in the handler only if you need it for session/logging, but don't claim it's required by the template.
   The acceptance criteria for this story explicitly requires computing Percentage and passing it to the template, so if you keep the existing template unchanged, the plan must add a step to update templates/feedback.html to display .Percentage when .WasFirstAnswer is false.

2) Unnecessary rename: The story requirement says "Modify handlers/quiz.go SubmitAnswerHandler…", implying that function already exists. Your plan proposes renaming AnswerHandler to SubmitAnswerHandler and updating references/tests. That's a larger refactor than requested and risks breaking routes/tests unnecessarily. Prefer: modify the existing SubmitAnswerHandler (or, if it doesn't exist, clarify with repo evidence). If the code currently uses AnswerHandler, update the plan to justify the rename as required, or better, just implement the logic in the current handler used by /quiz/answer without renaming.

3) Out-of-scope work: Adding a new /quiz/next route and NextQuestionHandler is not in the stated task/acceptance criteria for this story. The acceptance criteria only covers behavior of POST /quiz/answer plus rendering feedback.html with correct fields. Including /quiz/next changes may be fine in a separate story but should be removed here unless the story explicitly includes it.

4) Guard placement: Acceptance requires "When session.ShowingFeedback is true and POST /quiz/answer is hit… does not record stats again." Your plan says "after session validation and reading the submitted answer". To be safe, the guard should occur immediately after session retrieval/validation and before parsing form / computing correctness / any db call.

Please revise your plan and resubmit.`

// setupResumeTestContainer creates a container with Claude Code installed and returns
// the executor, container name, and a runner ready for testing.
// The caller is responsible for stopping the container via the returned cleanup function.
//
// If RESUME_TEST_WORKSPACE is set, uses that directory as the workspace (for testing
// with a real codebase). Otherwise creates a minimal workspace with a single Go file.
func setupResumeTestContainer(t *testing.T, ctx context.Context, apiKey string) (
	*claude.Runner, func(), string,
) {
	t.Helper()

	var workspaceDir string
	if envDir := os.Getenv("RESUME_TEST_WORKSPACE"); envDir != "" {
		// Use the provided workspace (e.g., a real codebase)
		absDir, err := filepath.Abs(envDir)
		if err != nil {
			t.Fatalf("Failed to resolve workspace path: %v", err)
		}
		workspaceDir = absDir
		t.Logf("Using provided workspace: %s", workspaceDir)
	} else {
		// Create a minimal workspace
		tempDir := t.TempDir()
		workspaceDir = filepath.Join(tempDir, "resume_stall_test")
		if err := os.MkdirAll(workspaceDir, 0755); err != nil {
			t.Fatalf("Failed to create workspace dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(workspaceDir, "main.go"), []byte(`package main

func main() {
	println("hello world")
}
`), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}
	}

	executor := dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "resume-stall-test")

	opts := &dockerexec.Opts{
		WorkDir:        workspaceDir,
		User:           "0:0",
		ClaudeCodeMode: true, // Persist session state across calls
	}

	containerName, err := executor.StartContainer(ctx, "resume-stall", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}

	cleanup := func() {
		_ = executor.StopContainer(ctx, containerName)
	}

	t.Logf("Started container: %s", containerName)
	time.Sleep(2 * time.Second)

	logger := logx.NewLogger("resume-stall-test")
	agentCtx := &tools.AgentContext{
		Executor: dockerexec.NewLocalExec(),
		WorkDir:  workspaceDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"done"})
	runner := claude.NewRunner(executor, containerName, provider, logger)

	return runner, cleanup, apiKey
}

// TestClaudeCodeResumeWithLongFeedback_DoubleDash reproduces the production stall
// where Claude Code hangs (0 responses, inactivity timeout) when resuming a session
// with long NEEDS_CHANGES feedback passed via the `--` trailing argument.
//
// Expected behavior with the bug present: the resume step hits the inactivity timeout
// and returns SignalInactivity with 0 responses.
//
// This test creates a real session, then resumes with the exact feedback pattern
// that caused the production failure.
func TestClaudeCodeResumeWithLongFeedback_DoubleDash(t *testing.T) {
	if !isDockerAvailable() {
		t.Skip("Docker not available")
	}
	apiKey := getTestAPIKey(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	runner, cleanup, _ := setupResumeTestContainer(t, ctx, apiKey)
	defer cleanup()

	// Step 1: Create an initial session with a substantial prompt that generates
	// a longer conversation (closer to production where 41 responses accumulated).
	// The production stall correlated with larger sessions per GitHub issue #21067.
	t.Log("Step 1: Creating initial session with substantial context...")
	initOpts := claude.DefaultRunOptions()
	initOpts.Mode = claude.ModePlanning
	initOpts.WorkDir = "/workspace"
	// Use the model from RESUME_TEST_MODEL env var, defaulting to sonnet (production model).
	// The stall was only observed in production with sonnet, not with haiku in earlier test runs.
	model := os.Getenv("RESUME_TEST_MODEL")
	if model == "" {
		model = "claude-sonnet-4-5"
	}
	initOpts.Model = model
	initOpts.SystemPrompt = `You are a coding assistant in a multi-agent system. You have access to file tools (Read, Write, Edit, Glob, Grep) and must explore the workspace thoroughly before creating a plan.

Your task requires careful analysis:
1. First, list all files in the workspace using Glob
2. Read each file you find
3. Analyze the code structure, dependencies, and patterns
4. Create a detailed implementation plan covering:
   - All files to modify
   - Specific functions to add or change
   - Error handling strategy
   - Test approach
5. Only after thorough exploration, call the submit_plan MCP tool with your complete plan

Be thorough - read files, explore the structure, and create a comprehensive plan.`
	initOpts.InitialInput = `Create a detailed plan to refactor the main.go file to:
1. Add proper error handling with custom error types
2. Add a configuration system using environment variables
3. Add structured logging with log levels
4. Add graceful shutdown handling with signal trapping
5. Add health check endpoint
6. Add unit tests for each component

Explore the workspace thoroughly first, then create the plan.`
	initOpts.EnvVars = map[string]string{
		"ANTHROPIC_API_KEY": apiKey,
	}
	initOpts.TotalTimeout = 5 * time.Minute
	initOpts.InactivityTimeout = 60 * time.Second

	initResult, err := runner.Run(ctx, &initOpts, nil)
	if err != nil {
		t.Fatalf("Initial session failed: %v", err)
	}

	sessionID := initResult.SessionID
	if sessionID == "" {
		t.Fatal("Expected session ID from initial run, got empty")
	}
	t.Logf("Initial session completed: signal=%s, session=%s, responses=%d, duration=%s",
		initResult.Signal, sessionID, initResult.ResponseCount, initResult.Duration)

	// Step 2: Resume with the long NEEDS_CHANGES feedback (the failing case)
	t.Log("Step 2: Resuming session with long feedback via -- (current approach)...")
	resumeOpts := claude.DefaultRunOptions()
	resumeOpts.Mode = claude.ModePlanning
	resumeOpts.Model = model
	resumeOpts.SessionID = sessionID
	resumeOpts.Resume = true
	resumeOpts.ResumeInput = longNeedsChangesFeedback
	resumeOpts.EnvVars = map[string]string{
		"ANTHROPIC_API_KEY": apiKey,
	}
	// Use a shorter inactivity timeout so the test doesn't take forever if it hangs
	resumeOpts.TotalTimeout = 3 * time.Minute
	resumeOpts.InactivityTimeout = 60 * time.Second

	resumeResult, err := runner.Run(ctx, &resumeOpts, nil)
	if err != nil {
		t.Fatalf("Resume call returned error: %v", err)
	}

	t.Logf("Resume completed: signal=%s, responses=%d, duration=%s",
		resumeResult.Signal, resumeResult.ResponseCount, resumeResult.Duration)

	// Document the outcome. If the bug is present, we expect:
	// - SignalInactivity (Claude Code hung, killed by inactivity timeout)
	// - 0 responses
	if resumeResult.Signal == claude.SignalInactivity {
		t.Errorf("REPRODUCED: Claude Code stalled on resume with long feedback "+
			"(signal=%s, responses=%d). This confirms the production bug where "+
			"long NEEDS_CHANGES feedback via -- causes Claude Code to hang.",
			resumeResult.Signal, resumeResult.ResponseCount)
	} else if resumeResult.ResponseCount == 0 {
		t.Errorf("Resume produced 0 responses with signal=%s — possible stall variant",
			resumeResult.Signal)
	} else {
		t.Logf("Resume succeeded without stalling (signal=%s, responses=%d) — "+
			"bug may be fixed in current Claude Code version",
			resumeResult.Signal, resumeResult.ResponseCount)
	}
}
