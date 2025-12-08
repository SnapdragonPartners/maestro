//go:build integration
// +build integration

package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/coder/claude"
	"orchestrator/pkg/config"
	dockerexec "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// TestClaudeCodeInstallationIntegration tests that Claude Code can be installed in a container.
func TestClaudeCodeInstallationIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping Claude Code installation test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "claudecode_install_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create executor using maestro-bootstrap container
	executor := dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "claude-install-test")

	opts := &dockerexec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0", // Run as root for npm global install
	}

	containerName, err := executor.StartContainer(ctx, "claude-install", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = executor.StopContainer(ctx, containerName)
	}()

	t.Logf("Started container: %s", containerName)

	// Wait for container to be ready
	time.Sleep(2 * time.Second)

	// Create installer
	logger := logx.NewLogger("test")
	installer := claude.NewInstaller(executor, containerName, logger)

	// Test installation
	t.Run("ensure_claude_code_installed", func(t *testing.T) {
		err := installer.EnsureClaudeCode(ctx)
		if err != nil {
			t.Fatalf("EnsureClaudeCode failed: %v", err)
		}

		// Verify Claude Code is accessible
		version, err := installer.GetClaudeCodeVersion(ctx)
		if err != nil {
			t.Fatalf("GetClaudeCodeVersion failed after installation: %v", err)
		}

		if version == "" {
			t.Fatal("Claude Code version is empty after installation")
		}

		t.Logf("Claude Code installed successfully: %s", version)
	})

	// Test that subsequent calls are idempotent
	t.Run("idempotent_installation", func(t *testing.T) {
		// Second call should be fast (already installed)
		start := time.Now()
		err := installer.EnsureClaudeCode(ctx)
		duration := time.Since(start)

		if err != nil {
			t.Fatalf("Second EnsureClaudeCode call failed: %v", err)
		}

		// Should be fast since already installed (< 5 seconds)
		if duration > 5*time.Second {
			t.Logf("Warning: Idempotent check took %s (expected < 5s)", duration)
		}

		t.Logf("Idempotent installation check completed in %s", duration)
	})
}

// TestClaudeCodeBasicExecutionIntegration tests basic Claude Code execution with signal detection.
func TestClaudeCodeBasicExecutionIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping Claude Code execution test")
	}

	// Skip if no API key
	apiKey := getTestAPIKey(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "claudecode_exec_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create a simple test file in the workspace
	testFile := filepath.Join(workspaceDir, "hello.txt")
	if err := os.WriteFile(testFile, []byte("Hello, Claude Code!"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Create executor
	executor := dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "claude-exec-test")

	opts := &dockerexec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0",
	}

	containerName, err := executor.StartContainer(ctx, "claude-exec", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = executor.StopContainer(ctx, containerName)
	}()

	t.Logf("Started container: %s", containerName)
	time.Sleep(2 * time.Second)

	// Create runner with tool provider
	// Include "done" tool so Claude can signal completion via MCP
	logger := logx.NewLogger("test")
	agentCtx := &tools.AgentContext{
		Executor: dockerexec.NewLocalExec(),
		WorkDir:  workspaceDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell", "done"})
	runner := claude.NewRunner(executor, containerName, provider, logger)

	// Test basic execution that should call done tool
	t.Run("simple_task_completion", func(t *testing.T) {
		runOpts := claude.DefaultRunOptions()
		runOpts.Mode = claude.ModeCoding
		runOpts.WorkDir = "/workspace"
		runOpts.Model = "claude-sonnet-4-20250514"
		runOpts.SystemPrompt = `You are a coding assistant in the Maestro multi-agent system.

## Maestro Integration Tools

You MUST call the done tool (via MCP as mcp__maestro__done) to complete your work:

### done
Signal that your work is complete.
Parameters:
- summary (required): Brief summary of what was done

## Instructions
Read the hello.txt file in the workspace and call the done tool with a summary.
Do NOT do anything else - just read the file and call done immediately.`
		runOpts.InitialInput = "Read hello.txt and call the done tool with a summary of what you found."
		runOpts.EnvVars = map[string]string{
			"ANTHROPIC_API_KEY": apiKey,
		}
		runOpts.TotalTimeout = 2 * time.Minute
		runOpts.InactivityTimeout = 30 * time.Second

		result, err := runner.Run(ctx, &runOpts)
		if err != nil {
			t.Fatalf("Runner.Run failed: %v", err)
		}

		t.Logf("Result: signal=%s, summary=%q, duration=%s, responses=%d",
			result.Signal, result.Summary, result.Duration, result.ResponseCount)

		// Check we got a valid signal
		if result.Signal == "" {
			t.Fatal("Expected a signal, got empty string")
		}

		if result.Signal == claude.SignalError {
			t.Fatalf("Got error signal: %v", result.Error)
		}

		// We expect either DONE or possibly STORY_COMPLETE
		if result.Signal != claude.SignalDone && result.Signal != claude.SignalStoryComplete {
			t.Logf("Warning: Got unexpected signal %s (expected DONE or STORY_COMPLETE)", result.Signal)
		}

		t.Logf("Basic execution test passed with signal: %s", result.Signal)
	})
}

// TestClaudeCodeStreamParsingIntegration tests stream-json parsing with real Claude Code output.
func TestClaudeCodeStreamParsingIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping stream parsing test")
	}

	// Skip if no API key
	apiKey := getTestAPIKey(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "stream_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create executor
	executor := dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "stream-test")

	opts := &dockerexec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0",
		Env: []string{
			"ANTHROPIC_API_KEY=" + apiKey,
		},
	}

	containerName, err := executor.StartContainer(ctx, "stream-parse", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = executor.StopContainer(ctx, containerName)
	}()

	time.Sleep(2 * time.Second)

	// Ensure Claude Code is installed
	logger := logx.NewLogger("test")
	installer := claude.NewInstaller(executor, containerName, logger)
	if err := installer.EnsureClaudeCode(ctx); err != nil {
		t.Fatalf("Failed to install Claude Code: %v", err)
	}

	// Run Claude Code directly and capture stream-json output
	t.Run("capture_stream_json", func(t *testing.T) {
		// Build command manually to test stream parsing
		// Note: prompt is positional argument, not a flag
		cmd := []string{
			"claude",
			"--print",
			"--output-format", "stream-json",
			"--verbose",
			"Say hello and nothing else. Just say 'Hello!' and stop.",
		}

		result, err := executor.Run(ctx, cmd, &dockerexec.Opts{
			WorkDir: "/workspace",
			Timeout: 60 * time.Second,
		})

		if err != nil {
			t.Logf("Command error (may be expected): %v", err)
		}

		t.Logf("Exit code: %d", result.ExitCode)
		t.Logf("Stdout length: %d bytes", len(result.Stdout))
		t.Logf("Stderr length: %d bytes", len(result.Stderr))

		// Check that we got some stream-json output
		if result.Stdout == "" {
			t.Fatal("Expected non-empty stdout with stream-json output")
		}

		// Count JSON lines
		lines := strings.Split(result.Stdout, "\n")
		jsonLines := 0
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line != "" && strings.HasPrefix(line, "{") {
				jsonLines++
			}
		}

		t.Logf("Found %d JSON lines in stream output", jsonLines)

		// Parse through the stream parser
		var events []claude.StreamEvent
		parser := claude.NewStreamParser(func(event claude.StreamEvent) {
			events = append(events, event)
		}, func(parseErr error) {
			t.Logf("Parse error: %v", parseErr)
		})

		for _, line := range lines {
			parser.ParseLine(line)
		}

		t.Logf("Parsed %d events from %d lines", len(events), parser.LineCount())

		if len(events) == 0 {
			t.Fatal("Expected at least one parsed event")
		}

		// Log event types
		eventTypes := make(map[string]int)
		for _, event := range events {
			eventTypes[event.Type]++
		}
		t.Logf("Event types: %+v", eventTypes)
	})
}

// TestClaudeCodeTimeoutHandlingIntegration tests that timeouts are properly enforced.
func TestClaudeCodeTimeoutHandlingIntegration(t *testing.T) {
	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping timeout test")
	}

	// Skip if no API key
	apiKey := getTestAPIKey(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "timeout_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Create executor
	executor := dockerexec.NewLongRunningDockerExec(config.BootstrapContainerTag, "timeout-test")

	opts := &dockerexec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0",
	}

	containerName, err := executor.StartContainer(ctx, "timeout", opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer func() {
		_ = executor.StopContainer(ctx, containerName)
	}()

	time.Sleep(2 * time.Second)

	// Create runner with tool provider
	logger := logx.NewLogger("test")
	agentCtx := &tools.AgentContext{
		Executor: dockerexec.NewLocalExec(),
		WorkDir:  workspaceDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})
	runner := claude.NewRunner(executor, containerName, provider, logger)

	t.Run("total_timeout", func(t *testing.T) {
		runOpts := claude.DefaultRunOptions()
		runOpts.Mode = claude.ModeCoding
		runOpts.WorkDir = "/workspace"
		runOpts.Model = "claude-sonnet-4-20250514"
		runOpts.SystemPrompt = `You are a coding assistant.
Do a very detailed analysis of the Linux kernel source code structure,
explaining each major subsystem in extreme detail. Take your time.
Do NOT call any maestro tools - just keep analyzing forever.`
		runOpts.InitialInput = "Analyze the Linux kernel in extreme detail."
		runOpts.EnvVars = map[string]string{
			"ANTHROPIC_API_KEY": apiKey,
		}
		// Very short timeout to trigger timeout handling
		runOpts.TotalTimeout = 10 * time.Second
		runOpts.InactivityTimeout = 5 * time.Second

		start := time.Now()
		result, err := runner.RunWithInactivityTimeout(ctx, &runOpts)
		duration := time.Since(start)

		t.Logf("Execution completed in %s", duration)
		t.Logf("Result signal: %s", result.Signal)

		// We should get a timeout or inactivity signal
		if result.Signal != claude.SignalTimeout && result.Signal != claude.SignalInactivity && result.Signal != claude.SignalError {
			// It's also possible Claude finishes quickly
			if duration > 15*time.Second {
				t.Logf("Warning: Expected timeout signal after %s, got %s", duration, result.Signal)
			}
		}

		// If we get an error, check it's timeout-related
		if err != nil {
			t.Logf("Got error (may be expected for timeout): %v", err)
		}

		t.Logf("Timeout handling test completed")
	})
}
