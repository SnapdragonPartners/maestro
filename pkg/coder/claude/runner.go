package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/google/uuid"

	"orchestrator/pkg/coder/claude/mcpserver"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// MCPProxyPath is the path to the MCP proxy binary inside containers.
// We use /tmp because container filesystems may be read-only, but /tmp is always writable.
const MCPProxyPath = "/tmp/maestro-mcp-proxy"

// MCPConfigPath is the path where the MCP config file is written inside containers.
const MCPConfigPath = "/tmp/maestro-mcp-config.json"

// DockerHostFromContainer is the hostname used by containers to reach the host.
// On Docker Desktop (macOS/Windows), this is "host.docker.internal".
// On Linux with --add-host=host.docker.internal:host-gateway, this also works.
const DockerHostFromContainer = "host.docker.internal"

// Runner executes Claude Code and manages the lifecycle of a session.
// It starts an MCP server on the host and configures Claude Code to use it
// via a stdio proxy inside the container.
type Runner struct {
	executor      exec.Executor
	containerName string
	installer     *Installer
	logger        *logx.Logger
	toolProvider  *tools.ToolProvider
	mcpServer     *mcpserver.Server
}

// NewRunner creates a new Runner for executing Claude Code.
func NewRunner(executor exec.Executor, containerName string, toolProvider *tools.ToolProvider, logger *logx.Logger) *Runner {
	if logger == nil {
		logger = logx.NewLogger("claude-runner")
	}

	return &Runner{
		executor:      executor,
		containerName: containerName,
		installer:     NewInstaller(executor, containerName, logger),
		toolProvider:  toolProvider,
		logger:        logger,
	}
}

// Run executes Claude Code with the given options and returns the result.
// This is the main entry point for running Claude Code sessions.
// If tm is non-nil, streaming output is used for real-time activity tracking.
func (r *Runner) Run(ctx context.Context, opts *RunOptions, tm *TimeoutManager) (Result, error) {
	startTime := time.Now()

	// Determine session ID: use provided one for resume, or generate new one
	sessionID := opts.SessionID
	if !opts.Resume && sessionID == "" {
		// Generate a new session ID for fresh sessions
		sessionID = uuid.New().String()
		opts.SessionID = sessionID
	}

	// Ensure Claude Code is installed
	if err := r.installer.EnsureClaudeCode(ctx); err != nil {
		return Result{
			Signal:    SignalError,
			Error:     err,
			Duration:  time.Since(startTime),
			SessionID: sessionID,
		}, err
	}

	// Ensure coder user (UID 1000) exists for non-root execution
	// Claude Code refuses --dangerously-skip-permissions when running as root
	if err := r.installer.EnsureCoderUser(ctx); err != nil {
		return Result{
			Signal:    SignalError,
			Error:     fmt.Errorf("failed to ensure coder user: %w", err),
			Duration:  time.Since(startTime),
			SessionID: sessionID,
		}, err
	}

	// Ensure MCP proxy is installed (copies embedded binary to container if needed)
	if err := r.installer.EnsureMCPProxy(ctx); err != nil {
		return Result{
			Signal:    SignalError,
			Error:     fmt.Errorf("failed to install MCP proxy: %w", err),
			Duration:  time.Since(startTime),
			SessionID: sessionID,
		}, err
	}

	// Start the MCP server on the host
	if err := r.startMCPServer(ctx); err != nil {
		return Result{
			Signal:    SignalError,
			Error:     fmt.Errorf("failed to start MCP server: %w", err),
			Duration:  time.Since(startTime),
			SessionID: sessionID,
		}, err
	}
	defer r.stopMCPServer()

	// Write MCP config to file in container (Claude Code expects file path, not inline JSON)
	if err := r.writeMCPConfig(ctx); err != nil {
		return Result{
			Signal:    SignalError,
			Error:     fmt.Errorf("failed to write MCP config: %w", err),
			Duration:  time.Since(startTime),
			SessionID: sessionID,
		}, err
	}

	// Verify MCP connectivity before launching Claude Code.
	// This catches networking/auth issues early instead of silently hanging.
	if err := r.verifyMCPConnectivity(ctx); err != nil {
		return Result{
			Signal:    SignalError,
			Error:     fmt.Errorf("MCP connectivity check failed: %w", err),
			Duration:  time.Since(startTime),
			SessionID: sessionID,
		}, err
	}

	// Build the command
	cmd := r.buildCommand(opts)

	// Build execution options (includes socket mount)
	execOpts := r.buildExecOpts(opts)

	if opts.Resume {
		r.logger.Info("Resuming Claude Code session: session=%s mode=%s model=%s timeout=%s port=%d",
			sessionID, opts.Mode, opts.Model, opts.TotalTimeout, r.mcpServer.Port())
	} else {
		r.logger.Info("Starting Claude Code: session=%s mode=%s model=%s timeout=%s port=%d",
			sessionID, opts.Mode, opts.Model, opts.TotalTimeout, r.mcpServer.Port())
	}

	// Execute Claude Code - prefer streaming for real-time activity tracking.
	execResult, err := r.executeWithStreaming(ctx, cmd, execOpts, tm)
	duration := time.Since(startTime)

	if err != nil {
		// Log any captured output for diagnostics (especially important when process is killed).
		if execResult.Stderr != "" {
			r.logger.Warn("Claude Code stderr output:\n%s", execResult.Stderr)
		}
		if execResult.Stdout == "" {
			r.logger.Warn("Claude Code produced no stdout output before exit")
		} else {
			// Log first 500 chars of stdout for context.
			preview := execResult.Stdout
			if len(preview) > 500 {
				preview = preview[:500] + "..."
			}
			r.logger.Info("Claude Code stdout (first 500 chars): %s", preview)
		}

		// Check if it was a context cancellation (timeout or inactivity)
		if ctx.Err() != nil {
			signal := SignalTimeout
			if tm != nil && tm.IsInactivityExpired() {
				signal = SignalInactivity
			}
			return Result{
				Signal:    signal,
				Error:     err,
				Duration:  duration,
				SessionID: sessionID,
			}, nil
		}
		return Result{
			Signal:    SignalError,
			Error:     fmt.Errorf("claude code execution failed: %w", err),
			Duration:  duration,
			SessionID: sessionID,
		}, fmt.Errorf("claude code execution failed: %w", err)
	}

	// Parse the output from stdout
	result := r.parseOutput(execResult.Stdout, execResult.Stderr)
	result.Duration = duration
	result.SessionID = sessionID
	result.ContainerUpgradeNeeded = r.installer.UpgradedInPlace()

	r.logger.Info("Claude Code completed: session=%s signal=%s responses=%d duration=%s",
		sessionID, result.Signal, result.ResponseCount, duration)

	return result, nil
}

// executeWithStreaming tries to use streaming execution for real-time output.
// Falls back to buffered execution if the executor doesn't support streaming.
func (r *Runner) executeWithStreaming(ctx context.Context, cmd []string, execOpts *exec.Opts, tm *TimeoutManager) (exec.Result, error) {
	// Try streaming executor for real-time activity tracking.
	if streamExec, ok := r.executor.(exec.StreamingExecutor); ok && tm != nil {
		r.logger.Debug("Using streaming executor for real-time output")

		onStdout := func(line string) {
			tm.RecordActivity()
			r.logger.Debug("Claude Code: %s", line)
		}
		onStderr := func(line string) {
			// Stderr also counts as activity (Node.js/proxy logs).
			tm.RecordActivity()
			r.logger.Debug("Claude Code stderr: %s", line)
		}

		result, streamErr := streamExec.RunStreaming(ctx, cmd, execOpts, onStdout, onStderr)
		if streamErr != nil {
			return result, fmt.Errorf("streaming execution failed: %w", streamErr)
		}
		return result, nil
	}

	// Fallback: buffered execution (inactivity timeout less reliable).
	if tm != nil {
		r.logger.Warn("Executor does not support streaming; inactivity timeout may be less reliable")
	}
	result, runErr := r.executor.Run(ctx, cmd, execOpts)
	if runErr != nil {
		return result, fmt.Errorf("execution failed: %w", runErr)
	}
	return result, nil
}

// startMCPServer starts the MCP server on the host listening on a TCP port.
func (r *Runner) startMCPServer(ctx context.Context) error {
	if r.toolProvider == nil {
		return fmt.Errorf("tool provider is required for MCP server")
	}

	// Create and start the MCP server (binds to dynamic port)
	r.mcpServer = mcpserver.NewServer(r.toolProvider, r.logger)

	// Start server in background goroutine
	go func() {
		if err := r.mcpServer.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			r.logger.Error("MCP server error: %v", err)
		}
	}()

	// Give the server a moment to start and bind to port
	time.Sleep(100 * time.Millisecond)

	// Verify the server actually bound to a port (catches rare bind failures)
	if r.mcpServer.Port() == 0 {
		return fmt.Errorf("MCP server failed to bind to port")
	}

	r.logger.Debug("MCP server started on port %d", r.mcpServer.Port())
	return nil
}

// stopMCPServer stops the MCP server.
func (r *Runner) stopMCPServer() {
	if r.mcpServer != nil {
		if err := r.mcpServer.Stop(); err != nil {
			r.logger.Error("Failed to stop MCP server: %v", err)
		}
		r.mcpServer = nil
	}
}

// writeMCPConfig writes the MCP configuration to a file inside the container.
// Claude Code's --mcp-config flag expects a file path, not inline JSON.
func (r *Runner) writeMCPConfig(ctx context.Context) error {
	configJSON := BuildMCPConfigJSON(r.mcpServer.Port())

	// Write the config file using a heredoc to handle JSON escaping
	cmd := []string{"sh", "-c", fmt.Sprintf("cat > %s << 'MCPEOF'\n%s\nMCPEOF", MCPConfigPath, configJSON)}

	result, err := r.executor.Run(ctx, cmd, &exec.Opts{})
	if err != nil {
		return fmt.Errorf("failed to write MCP config: %w", err)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("failed to write MCP config: exit code %d, stderr: %s", result.ExitCode, result.Stderr)
	}

	r.logger.Debug("Wrote MCP config to %s", MCPConfigPath)
	return nil
}

// verifyMCPConnectivity checks that the MCP server is reachable and
// accepting authenticated connections before launching Claude Code.
// This catches networking/auth issues early instead of silently hanging.
func (r *Runner) verifyMCPConnectivity(ctx context.Context) error {
	if r.mcpServer == nil {
		return fmt.Errorf("MCP server not started")
	}

	port := r.mcpServer.Port()
	token := r.mcpServer.Token()

	// Phase 1: Host-side check - verify TCP server is accepting connections.
	addr := fmt.Sprintf("127.0.0.1:%d", port)
	conn, err := net.DialTimeout("tcp", addr, 5*time.Second)
	if err != nil {
		return fmt.Errorf("cannot connect to MCP server at %s: %w", addr, err)
	}

	// Send auth message and verify response.
	authMsg := fmt.Sprintf("{\"auth\":%q}\n", token)
	if _, writeErr := conn.Write([]byte(authMsg)); writeErr != nil {
		_ = conn.Close()
		return fmt.Errorf("failed to send auth to MCP server: %w", writeErr)
	}

	// Read auth response.
	reader := bufio.NewReader(conn)
	line, readErr := reader.ReadString('\n')
	_ = conn.Close()

	if readErr != nil {
		return fmt.Errorf("failed to read MCP server auth response: %w", readErr)
	}

	var authResp struct {
		Authenticated bool   `json:"authenticated"`
		Error         string `json:"error,omitempty"`
	}
	if jsonErr := json.Unmarshal([]byte(line), &authResp); jsonErr != nil {
		return fmt.Errorf("invalid MCP server auth response: %w", jsonErr)
	}
	if !authResp.Authenticated {
		return fmt.Errorf("MCP server rejected authentication: %s", authResp.Error)
	}

	// Phase 2: Container-side check - verify proxy can reach host from inside container.
	proxyCheckCmd := []string{MCPProxyPath, "--check", fmt.Sprintf("%s:%d", DockerHostFromContainer, port)}
	proxyOpts := &exec.Opts{
		Timeout: 10 * time.Second,
		Env:     []string{"MCP_AUTH_TOKEN=" + token},
	}
	proxyResult, proxyErr := r.executor.Run(ctx, proxyCheckCmd, proxyOpts)
	if proxyErr != nil || proxyResult.ExitCode != 0 {
		errDetail := ""
		if proxyErr != nil {
			errDetail = proxyErr.Error()
		} else {
			errDetail = proxyResult.Stderr
		}
		return fmt.Errorf("MCP proxy cannot reach host from container (host.docker.internal:%d): %s", port, errDetail)
	}

	r.logger.Info("✅ MCP connectivity verified (host-side + container-side)")
	return nil
}

// buildCommand constructs the Claude Code command line.
func (r *Runner) buildCommand(opts *RunOptions) []string {
	cmd := []string{
		"claude",
		"--print",
		"--output-format", "stream-json",
		"--verbose",
	}

	// Add model if specified
	if opts.Model != "" {
		cmd = append(cmd, "--model", opts.Model)
	}

	// Bypass permission checks since we're in a sandboxed container.
	// This prevents Claude Code from asking for interactive permission approval.
	// Security is enforced by the container sandbox (non-root user, read-only filesystem).
	// Also add MCP config file path (config written by writeMCPConfig).
	cmd = append(cmd, "--dangerously-skip-permissions", "--mcp-config", MCPConfigPath)

	// Add system prompt if specified (only for new sessions, not for resume)
	if opts.SystemPrompt != "" && !opts.Resume {
		cmd = append(cmd, "--append-system-prompt", opts.SystemPrompt)
	}

	// Handle session resume vs new session
	// Note: In print mode (--print), --resume REQUIRES a session ID as its argument.
	// Syntax: --resume <session-id> (NOT --session-id <id> --resume, which is invalid)
	// In interactive mode, --resume without a session ID opens a picker, but that doesn't work with --print.
	if opts.Resume {
		// Resume an existing session - session ID is REQUIRED in print mode
		if opts.SessionID != "" {
			cmd = append(cmd, "--resume", opts.SessionID)
		} else {
			// Fallback: if no session ID provided, try without (will fail in print mode)
			cmd = append(cmd, "--resume")
		}
		// Use ResumeInput as the prompt (contains feedback from testing/review/merge)
		if opts.ResumeInput != "" {
			cmd = append(cmd, "--", opts.ResumeInput)
		}
	} else {
		// New session - optionally use explicit session ID
		if opts.SessionID != "" {
			cmd = append(cmd, "--session-id", opts.SessionID)
		}
		// Add -- separator before positional argument to avoid parsing issues
		// with prompts that might start with - or contain special characters
		cmd = append(cmd, "--", opts.InitialInput)
	}

	return cmd
}

// BuildMCPConfigJSON creates the MCP config JSON string for a given port.
// This is useful for testing and custom integrations.
func BuildMCPConfigJSON(port int) string {
	// TCP address from container perspective using host.docker.internal
	tcpAddr := fmt.Sprintf("%s:%d", DockerHostFromContainer, port)

	config := map[string]interface{}{
		"mcpServers": map[string]interface{}{
			"maestro": map[string]interface{}{
				"command": MCPProxyPath,
				"args":    []string{tcpAddr},
			},
		},
	}
	data, _ := json.Marshal(config)
	return string(data)
}

// ClaudeCodeUser is the user to run Claude Code as.
// Claude Code refuses --dangerously-skip-permissions when running as root for security.
// We use UID 1000 which is the typical first non-root user on Linux systems.
// The container's Dockerfile should create this user with appropriate permissions.
const ClaudeCodeUser = "1000:1000"

// buildExecOpts creates execution options for the executor.
func (r *Runner) buildExecOpts(opts *RunOptions) *exec.Opts {
	execOpts := exec.DefaultExecOpts()

	// Set timeout
	if opts.TotalTimeout > 0 {
		execOpts.Timeout = opts.TotalTimeout
	}

	// Set working directory
	if opts.WorkDir != "" {
		execOpts.WorkDir = opts.WorkDir
	}

	// Run as non-root user for Claude Code
	// Claude Code refuses --dangerously-skip-permissions when running as root
	execOpts.User = ClaudeCodeUser

	// Build environment variables
	env := make([]string, 0, len(opts.EnvVars)+1)
	for k, v := range opts.EnvVars {
		env = append(env, k+"="+v)
	}

	// Add MCP auth token for the proxy to authenticate with the host server
	if r.mcpServer != nil {
		env = append(env, "MCP_AUTH_TOKEN="+r.mcpServer.Token())
	}
	execOpts.Env = env

	// Note: No extra mounts needed - TCP communication uses host.docker.internal
	// which is available by default in Docker Desktop and can be added to Linux
	// containers with --add-host=host.docker.internal:host-gateway

	return &execOpts
}

// parseOutput parses Claude Code's stdout and extracts the result.
func (r *Runner) parseOutput(stdout, stderr string) Result {
	var events []StreamEvent

	// Create parser that collects events and logs tool calls
	parser := NewStreamParser(func(event StreamEvent) {
		events = append(events, event)
		// Log tool calls at debug level for visibility into Claude Code activity
		if event.Type == eventTypeToolUse && event.ToolUse != nil {
			r.logger.Debug("Claude Code tool call: %s", event.ToolUse.Name)
		}
		if event.Type == "tool_result" && event.ToolResult != nil {
			status := "success"
			if event.ToolResult.IsError {
				status = "error"
			}
			r.logger.Debug("Claude Code tool result: %s", status)
		}
	}, func(err error) {
		r.logger.Debug("Stream parse error: %v", err)
	})

	// Parse stdout line by line
	for _, line := range strings.Split(stdout, "\n") {
		parser.ParseLine(line)
	}

	// Log summary of tool activity at Info level
	toolNames := r.getToolCallNames(events)
	if len(toolNames) > 0 {
		r.logger.Info("Claude Code tools called: %v", toolNames)
	}

	// Use signal detector to find maestro tool calls
	detector := NewSignalDetector()
	detector.AddEvents(events)

	signal, input := detector.DetectSignal()
	if signal == "" {
		// No signal detected - check if there was an error in the stream
		if hasErr, errMsg := HasError(events); hasErr {
			return Result{
				Signal:        SignalError,
				Error:         &streamError{message: errMsg},
				ResponseCount: CountResponses(events),
			}
		}

		// No signal and no error - unexpected completion
		r.logger.Warn("Claude Code completed without signal: lines=%d stderr=%s",
			parser.LineCount(), stderr)

		return Result{
			Signal:        SignalError,
			Error:         &streamError{message: "Claude Code completed without calling a maestro signal tool"},
			ResponseCount: CountResponses(events),
		}
	}

	// Build result from detected signal
	return BuildResult(signal, input, events)
}

// RunWithInactivityTimeout executes Claude Code with inactivity detection.
// This wraps Run() with streaming output and active inactivity cancellation.
// When no output is received for InactivityTimeout, the context is cancelled
// and the docker exec process is killed.
func (r *Runner) RunWithInactivityTimeout(ctx context.Context, opts *RunOptions) (Result, error) {
	// Create a timeout manager
	tm := NewTimeoutManager(opts.TotalTimeout, opts.InactivityTimeout)

	// Create a context with the total timeout, then wrap with cancel for inactivity.
	ctx, totalCancel := context.WithTimeout(ctx, opts.TotalTimeout)
	defer totalCancel()

	ctx, inactivityCancel := context.WithCancel(ctx)
	defer inactivityCancel()

	// Wire inactivity detection to context cancellation.
	tm.SetCancelFunc(inactivityCancel)

	// Start the timeout manager
	tm.Start()
	defer tm.Stop()

	// Run Claude Code with streaming (tm enables streaming callbacks).
	result, err := r.Run(ctx, opts, tm)

	// If inactivity triggered the cancellation, ensure the signal is set.
	if tm.IsInactivityExpired() {
		result.Signal = SignalInactivity
		if result.Error == nil {
			result.Error = &streamError{message: "Claude Code session stalled - no output for " + opts.InactivityTimeout.String()}
		}
		r.logger.Warn("⏰ Claude Code killed due to inactivity (no output for %s)", opts.InactivityTimeout)
	}

	return result, err
}

// GetInstaller returns the installer for manual installation operations.
func (r *Runner) GetInstaller() *Installer {
	return r.installer
}

// GetPort returns the TCP port the MCP server is listening on.
// Returns 0 if the server is not running.
func (r *Runner) GetPort() int {
	if r.mcpServer == nil {
		return 0
	}
	return r.mcpServer.Port()
}

// getToolCallNames returns the list of tool names called in order.
func (r *Runner) getToolCallNames(events []StreamEvent) []string {
	var names []string
	for i := range events {
		if events[i].Type == eventTypeToolUse && events[i].ToolUse != nil {
			names = append(names, events[i].ToolUse.Name)
		}
	}
	return names
}
