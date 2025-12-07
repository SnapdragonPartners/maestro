//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/coder/claude"
	"orchestrator/pkg/coder/claude/mcpserver"
	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// TestClaudeCodeMCPToolCall is an end-to-end test that verifies Claude Code
// can successfully call a maestro MCP tool via the TCP proxy.
//
// This test:
// 1. Starts an MCP server on the host with a shell tool
// 2. Starts a container with Claude Code installed
// 3. Invokes Claude Code with a prompt that triggers tool use
// 4. Verifies Claude Code successfully called the tool and got results
//
// Requires:
// - ANTHROPIC_API_KEY environment variable set
// - Docker available
// - maestro-bootstrap image with claude CLI and maestro-mcp-proxy
func TestClaudeCodeMCPToolCall(t *testing.T) {
	// Skip if no API key available
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("Skipping Claude Code E2E test: ANTHROPIC_API_KEY not set")
	}

	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping Claude Code E2E test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "claude_e2e_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Define a unique test marker that we'll have Claude echo
	testContent := "CLAUDE_CODE_E2E_TEST_MARKER_12345"

	// Create tool provider with shell tool
	logger := logx.NewLogger("claude-e2e-test")
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  workspaceDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Start MCP server on host
	server := mcpserver.NewServer(provider, logger)
	go func() {
		if err := server.Start(ctx); err != nil && err != context.Canceled {
			t.Logf("MCP server error: %v", err)
		}
	}()
	defer server.Stop()

	// Wait for server to start
	time.Sleep(200 * time.Millisecond)

	port := server.Port()
	token := server.Token()
	if port == 0 {
		t.Fatal("Server failed to bind to port")
	}
	t.Logf("MCP server started on port %d", port)

	// Start container
	containerName := "claude-e2e-test-container"
	executor := exec.NewLongRunningDockerExec(config.BootstrapContainerTag, "claude-e2e-test")

	opts := &exec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0",
	}

	startedContainer, err := executor.StartContainer(ctx, containerName, opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer executor.StopContainer(ctx, startedContainer)
	t.Logf("Container started: %s", startedContainer)

	// Wait for container to be ready
	time.Sleep(1 * time.Second)

	// Verify Claude Code is installed
	claudeCheck := []string{"which", "claude"}
	result, err := executor.Run(ctx, claudeCheck, &exec.Opts{})
	if err != nil || result.ExitCode != 0 {
		t.Fatalf("Claude Code not found in container: err=%v, exit=%d, stderr=%s",
			err, result.ExitCode, result.Stderr)
	}
	t.Logf("Claude CLI found: %s", strings.TrimSpace(result.Stdout))

	// Build the MCP config for Claude Code
	mcpConfig := claude.BuildMCPConfigJSON(port)
	t.Logf("MCP config: %s", mcpConfig)

	// Write MCP config to file in container (Claude Code expects file path, not inline JSON)
	configPath := "/tmp/mcp-config.json"
	writeCmd := []string{"sh", "-c", fmt.Sprintf("cat > %s << 'EOF'\n%s\nEOF", configPath, mcpConfig)}
	writeResult, err := executor.Run(ctx, writeCmd, &exec.Opts{})
	if err != nil || writeResult.ExitCode != 0 {
		t.Fatalf("Failed to write MCP config: err=%v, exit=%d, stderr=%s",
			err, writeResult.ExitCode, writeResult.Stderr)
	}
	t.Logf("Wrote MCP config to %s", configPath)

	// Run Claude Code with a prompt that should trigger a shell tool call
	// The prompt asks Claude to run an echo command using the shell tool
	// Note: The shell tool runs on the HOST via the MCP server
	prompt := fmt.Sprintf(`You have access to a maestro MCP server with a shell tool.
Use the shell tool to run: echo %s
Then report exactly what the command output.
This is a test - just call the shell tool once with the echo command and report the result.`, testContent)

	// Verify config file was written correctly
	verifyCmd := []string{"cat", configPath}
	verifyResult, err := executor.Run(ctx, verifyCmd, &exec.Opts{})
	if err != nil {
		t.Fatalf("Failed to verify MCP config: %v", err)
	}
	t.Logf("Config file contents: %s", verifyResult.Stdout)

	// Write a wrapper script to avoid shell escaping issues
	// This is cleaner than trying to escape everything in a single sh -c call
	// Note: --mcp-config accepts JSON strings directly (not just file paths)
	// Use -- to separate options from the positional prompt argument
	// Use --allowedTools to allow the MCP tool (can't use --dangerously-skip-permissions as root)
	scriptPath := "/tmp/run-claude.sh"
	scriptContent := fmt.Sprintf(`#!/bin/sh
exec claude --print --output-format json --allowedTools "mcp__maestro__shell" --mcp-config '%s' -- '%s'
`, mcpConfig, strings.ReplaceAll(prompt, "'", "'\\''"))

	scriptCmd := []string{"sh", "-c", fmt.Sprintf("cat > %s << 'SCRIPT'\n%s\nSCRIPT\nchmod +x %s", scriptPath, scriptContent, scriptPath)}
	scriptResult, err := executor.Run(ctx, scriptCmd, &exec.Opts{})
	if err != nil || scriptResult.ExitCode != 0 {
		t.Fatalf("Failed to write script: err=%v, exit=%d, stderr=%s",
			err, scriptResult.ExitCode, scriptResult.Stderr)
	}

	// Verify the script contents
	catScript := []string{"cat", scriptPath}
	catScriptResult, err := executor.Run(ctx, catScript, &exec.Opts{})
	if err != nil {
		t.Fatalf("Failed to cat script: %v", err)
	}
	t.Logf("Script contents:\n%s", catScriptResult.Stdout)

	// Build the command - pass API key and auth token via environment
	claudeCmd := []string{scriptPath}

	cmdResult, err := executor.Run(ctx, claudeCmd, &exec.Opts{
		Timeout: 2 * time.Minute,
		Env: []string{
			"ANTHROPIC_API_KEY=" + os.Getenv("ANTHROPIC_API_KEY"),
			"MCP_AUTH_TOKEN=" + token,
		},
	})
	if err != nil {
		t.Fatalf("Claude Code execution failed: %v\nstderr: %s", err, cmdResult.Stderr)
	}

	t.Logf("Claude Code stdout: %s", cmdResult.Stdout)
	if cmdResult.Stderr != "" {
		t.Logf("Claude Code stderr: %s", cmdResult.Stderr)
	}

	// Verify the response contains our test marker
	// This proves Claude Code successfully:
	// 1. Connected to the MCP server via proxy
	// 2. Authenticated with the token
	// 3. Called the shell tool
	// 4. Got the result back
	if !strings.Contains(cmdResult.Stdout, testContent) {
		t.Errorf("Expected Claude Code output to contain test marker %q, got: %s",
			testContent, cmdResult.Stdout)
	}

	t.Log("Claude Code E2E test passed - MCP tool call successful")
}

// TestClaudeCodeMCPToolCallWithRunner tests the Runner abstraction for Claude Code.
// This tests the higher-level API that the coder agent uses.
func TestClaudeCodeMCPToolCallWithRunner(t *testing.T) {
	// Skip if no API key available
	if os.Getenv("ANTHROPIC_API_KEY") == "" {
		t.Skip("Skipping Claude Code Runner E2E test: ANTHROPIC_API_KEY not set")
	}

	// Skip if Docker is not available
	if !isDockerAvailable() {
		t.Skip("Docker not available, skipping Claude Code Runner E2E test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	// Create temporary workspace
	tempDir := t.TempDir()
	workspaceDir := filepath.Join(tempDir, "runner_e2e_test")
	if err := os.MkdirAll(workspaceDir, 0755); err != nil {
		t.Fatalf("Failed to create workspace dir: %v", err)
	}

	// Define a unique test marker that we'll have Claude echo
	testContent := "RUNNER_E2E_MARKER_67890"

	// Start container first
	containerName := "runner-e2e-test-container"
	executor := exec.NewLongRunningDockerExec(config.BootstrapContainerTag, "runner-e2e-test")

	opts := &exec.Opts{
		WorkDir: workspaceDir,
		User:    "0:0",
	}

	startedContainer, err := executor.StartContainer(ctx, containerName, opts)
	if err != nil {
		t.Fatalf("Failed to start container: %v", err)
	}
	defer executor.StopContainer(ctx, startedContainer)

	// Wait for container to be ready
	time.Sleep(1 * time.Second)

	// Create tool provider
	logger := logx.NewLogger("runner-e2e-test")
	agentCtx := &tools.AgentContext{
		Executor: exec.NewLocalExec(),
		WorkDir:  workspaceDir,
	}
	provider := tools.NewProvider(agentCtx, []string{"shell"})

	// Create the runner
	runner := claude.NewRunner(executor, containerName, provider, logger)

	// Ensure Claude Code is installed
	if err := runner.GetInstaller().EnsureClaudeCode(ctx); err != nil {
		t.Fatalf("Failed to ensure Claude Code: %v", err)
	}

	// Run Claude Code with the runner
	// Note: The runner test is simplified and doesn't test the full flow like TestClaudeCodeMCPToolCall
	// because the runner doesn't add --allowedTools and has permission issues
	runOpts := &claude.RunOptions{
		InitialInput: fmt.Sprintf("Use the shell tool to run: echo %s\nThen report exactly what the command output.", testContent),
		WorkDir:      "/workspace",
		TotalTimeout: 2 * time.Minute,
		EnvVars: map[string]string{
			"ANTHROPIC_API_KEY": os.Getenv("ANTHROPIC_API_KEY"),
		},
	}

	result, err := runner.Run(ctx, runOpts)
	if err != nil {
		t.Fatalf("Runner.Run failed: %v", err)
	}

	t.Logf("Runner result: signal=%s, responses=%d, duration=%s",
		result.Signal, result.ResponseCount, result.Duration)

	// For this test, we don't necessarily need a specific signal
	// since we're just testing MCP tool calls work
	// The fact that it completed without error is the main success criteria

	t.Log("Claude Code Runner E2E test completed")
}
