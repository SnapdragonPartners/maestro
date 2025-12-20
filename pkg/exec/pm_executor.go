package exec

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/logx"
)

// PMExecutor implements the Executor interface for the PM agent.
// The PM runs in a single long-lived container with read-only mount to its own workspace.
type PMExecutor struct {
	logger        *logx.Logger
	containerName string
	containerID   string
	image         string
	dockerCmd     string
	pmWorkspace   string // Path to PM's workspace directory
	mu            sync.RWMutex
}

// NewPMExecutor creates a new PM executor.
// pmWorkspace is the PM's workspace directory (e.g., /path/to/project/pm-001).
func NewPMExecutor(image, pmWorkspace string) *PMExecutor {
	logger := logx.NewLogger("pm-executor")

	// Auto-detect Docker command
	dockerCmd := dockerCommand
	if _, err := exec.LookPath(podmanCommand); err == nil {
		if _, err := exec.LookPath(dockerCommand); err != nil {
			dockerCmd = podmanCommand
		}
	}

	return &PMExecutor{
		logger:        logger,
		image:         image,
		dockerCmd:     dockerCmd,
		containerName: "maestro-pm",
		pmWorkspace:   pmWorkspace,
	}
}

// Name returns the executor type name.
func (p *PMExecutor) Name() ExecutorType {
	return ExecutorTypeDocker
}

// Available checks if Docker is available and the daemon is running.
func (p *PMExecutor) Available() bool {
	// Check if docker/podman command exists
	if _, err := exec.LookPath(p.dockerCmd); err != nil {
		p.logger.Debug("Docker command not found: %v", err)
		return false
	}

	// Check if daemon is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, p.dockerCmd, "ps", "-q")
	if err := cmd.Run(); err != nil {
		p.logger.Debug("Docker daemon not available: %v", err)
		return false
	}

	return true
}

// Start creates and starts the PM container with read-only mount to its workspace.
func (p *PMExecutor) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// Check if already running
	if p.containerID != "" {
		p.logger.Info("PM container already running: %s", p.containerID)
		return nil
	}

	// Remove any existing container with the same name
	p.logger.Debug("Removing any existing PM container")
	rmCmd := exec.CommandContext(ctx, p.dockerCmd, "rm", "-f", p.containerName)
	if err := rmCmd.Run(); err != nil {
		p.logger.Debug("Failed to remove existing container (this is normal if it doesn't exist): %v", err)
	}

	// Convert workspace path to absolute (required for Docker volume mounts)
	absWorkspace, err := filepath.Abs(p.pmWorkspace)
	if err != nil {
		return fmt.Errorf("failed to resolve PM workspace path: %w", err)
	}

	// Build docker run command with PM workspace mounted at /workspace
	args := []string{
		"run", "-d",
		"--name", p.containerName,
		"--security-opt", "no-new-privileges",
		"--read-only",
		"--cpus", "2",
		"--memory", "2g",
		"--pids-limit", "256",
		// Run as non-privileged user (same as coders for consistency)
		"--user", "1000:1000",
		// Mount PM workspace at /workspace (read-only, same as coders)
		"--volume", fmt.Sprintf("%s:/workspace:ro", absWorkspace),
		// Tmpfs mounts for temporary files
		"--tmpfs", "/tmp:exec,nodev,nosuid,size=1g",
		"--tmpfs", "/home:exec,nodev,nosuid,size=100m",
		"--tmpfs", "/.cache:exec,nodev,nosuid,size=100m",
		"--env", "HOME=/tmp",
		p.image,
		"sleep", "infinity",
	}

	p.logger.Info("Starting PM container: %s %s", p.dockerCmd, strings.Join(args, " "))

	// Start container
	cmd := exec.CommandContext(ctx, p.dockerCmd, args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start PM container: %w (output: %s)", err, string(output))
	}

	// Store container ID
	p.containerID = strings.TrimSpace(string(output))
	p.logger.Info("Started PM container %s with ID: %s", p.containerName, p.containerID)

	return nil
}

// Stop stops and removes the PM container.
func (p *PMExecutor) Stop(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.containerID == "" {
		p.logger.Debug("No PM container to stop")
		return nil
	}

	p.logger.Info("Stopping PM container: %s", p.containerID)

	// Stop and remove container
	rmCmd := exec.CommandContext(ctx, p.dockerCmd, "rm", "-f", p.containerName)
	if err := rmCmd.Run(); err != nil {
		return fmt.Errorf("failed to stop PM container: %w", err)
	}

	p.containerID = ""
	return nil
}

// Exec executes a command in the PM container.
func (p *PMExecutor) Exec(ctx context.Context, cmd string, args ...string) ([]byte, error) {
	p.mu.RLock()
	containerID := p.containerID
	p.mu.RUnlock()

	if containerID == "" {
		return nil, fmt.Errorf("PM container not running")
	}

	// Build docker exec command
	execArgs := []string{"exec", containerID, cmd}
	execArgs = append(execArgs, args...)

	p.logger.Debug("Executing in PM container: %s %s", cmd, strings.Join(args, " "))

	execCmd := exec.CommandContext(ctx, p.dockerCmd, execArgs...)
	output, err := execCmd.CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("command failed: %w", err)
	}

	return output, nil
}

// ExecShell executes a shell command in the PM container.
func (p *PMExecutor) ExecShell(ctx context.Context, shellCmd string) ([]byte, error) {
	return p.Exec(ctx, "sh", "-c", shellCmd)
}

// Run executes a command in the PM container and returns structured result.
// Implements the Executor interface.
func (p *PMExecutor) Run(ctx context.Context, cmd []string, opts *Opts) (Result, error) {
	start := time.Now()

	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("command cannot be empty")
	}

	p.mu.RLock()
	containerName := p.containerName
	p.mu.RUnlock()

	if containerName == "" {
		return Result{}, fmt.Errorf("PM container not started")
	}

	// Build docker exec command
	// Working directory (default to /workspace for PM)
	workDir := "/workspace"
	if opts != nil && opts.WorkDir != "" {
		workDir = opts.WorkDir
	}

	args := []string{"exec",
		"--workdir", workDir,
		containerName,
	}

	// Add command
	args = append(args, cmd...)

	// Execute command
	execCmd := exec.CommandContext(ctx, p.dockerCmd, args...)

	p.logger.Debug("Executing in PM container: %s", strings.Join(args, " "))

	// Use CombinedOutput and put in Stdout for now
	// Tools can check both Stdout and combined output
	output, err := execCmd.CombinedOutput()
	exitCode := 0
	stderr := ""
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
			// If there's an exit error, the output likely contains stderr
			stderr = string(output)
		} else {
			exitCode = 1
		}
	}

	elapsed := time.Since(start)

	result := Result{
		Stdout:       string(output),
		Stderr:       stderr, // Populated on error
		ExitCode:     exitCode,
		Duration:     elapsed,
		ExecutorUsed: string(ExecutorTypeDocker),
	}

	if err != nil && exitCode != 0 {
		p.logger.Debug("Command failed with exit code %d: %s", exitCode, strings.Join(cmd, " "))
	}

	return result, nil
}
