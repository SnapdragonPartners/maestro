package exec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// ArchitectExecutor implements the Executor interface for the architect agent.
// The architect runs in a single long-lived container with read-only mounts to all coder workspaces.
type ArchitectExecutor struct {
	logger        *logx.Logger
	containerName string
	containerID   string
	image         string
	dockerCmd     string
	projectDir    string
	maxCoders     int
	mu            sync.RWMutex
}

// NewArchitectExecutor creates a new architect executor.
// projectDir is the base directory containing coder workspaces (coder-001, coder-002, etc.).
// maxCoders is the number of coder workspace directories to mount.
func NewArchitectExecutor(image, projectDir string, maxCoders int) *ArchitectExecutor {
	logger := logx.NewLogger("architect-executor")

	// Auto-detect Docker command
	dockerCmd := dockerCommand
	if _, err := exec.LookPath(podmanCommand); err == nil {
		if _, err := exec.LookPath(dockerCommand); err != nil {
			dockerCmd = podmanCommand
		}
	}

	return &ArchitectExecutor{
		logger:        logger,
		image:         image,
		dockerCmd:     dockerCmd,
		containerName: "maestro-architect",
		projectDir:    projectDir,
		maxCoders:     maxCoders,
	}
}

// Name returns the executor type name.
func (a *ArchitectExecutor) Name() ExecutorType {
	return ExecutorTypeDocker
}

// Available checks if Docker is available and the daemon is running.
func (a *ArchitectExecutor) Available() bool {
	// Check if docker/podman command exists
	if _, err := exec.LookPath(a.dockerCmd); err != nil {
		a.logger.Debug("Docker command not found: %v", err)
		return false
	}

	// Check if daemon is running
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, a.dockerCmd, "ps", "-q")
	if err := cmd.Run(); err != nil {
		a.logger.Debug("Docker daemon not available: %v", err)
		return false
	}

	return true
}

// Start creates and starts the architect container with read-only mounts to all coder workspaces.
//
//nolint:cyclop // Complexity from mounting multiple workspaces is acceptable.
func (a *ArchitectExecutor) Start(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if already running
	if a.containerID != "" {
		a.logger.Info("Architect container already running: %s", a.containerID)
		return nil
	}

	// Remove any existing container with the same name
	a.logger.Debug("Removing any existing architect container")
	rmCmd := exec.CommandContext(ctx, a.dockerCmd, "rm", "-f", a.containerName)
	if err := rmCmd.Run(); err != nil {
		a.logger.Debug("Failed to remove existing container (this is normal if it doesn't exist): %v", err)
	}

	// Build docker run command
	args := []string{"run", "-d", "--name", a.containerName,
		// Security hardening
		"--security-opt", "no-new-privileges",
		// Read-only root filesystem
		"--read-only",
		// Resource limits (generous for architect)
		"--cpus", "2",
		"--memory", "2g",
		"--pids-limit", "256",
		// Run as non-privileged user (same as coders for consistency)
		"--user", "1000:1000",
	}

	// Network enabled (architect needs to communicate with LLM APIs)
	// Network is enabled by default, no --network flag needed

	// Mount architect workspace (read-only)
	architectDir := filepath.Join(a.projectDir, "architect-001")
	absArchitectDir, archErr := filepath.Abs(architectDir)
	if archErr != nil {
		return fmt.Errorf("failed to resolve architect workspace path: %w", archErr)
	}
	hostArchitectPath := normalizePath(absArchitectDir)

	// Verify architect workspace exists
	if stat, statErr := os.Stat(hostArchitectPath); os.IsNotExist(statErr) {
		return fmt.Errorf("architect workspace does not exist at %s - run workspace verification/setup first", hostArchitectPath)
	} else if statErr != nil {
		return fmt.Errorf("failed to stat architect workspace %s: %w", hostArchitectPath, statErr)
	} else {
		a.logger.Debug("Mounting architect workspace: %s (mode: %v)", hostArchitectPath, stat.Mode())
	}

	// Mount as read-only: host:container:ro
	args = append(args, "--volume", fmt.Sprintf("%s:/mnt/architect:ro", hostArchitectPath))

	// Mount all coder workspace directories (read-only)
	for i := 1; i <= a.maxCoders; i++ {
		coderDir := filepath.Join(a.projectDir, fmt.Sprintf("coder-%03d", i))

		// Convert to absolute path
		absCoderDir, coderErr := filepath.Abs(coderDir)
		if coderErr != nil {
			return fmt.Errorf("failed to resolve coder workspace path: %w", coderErr)
		}

		// Normalize path for cross-platform
		hostPath := normalizePath(absCoderDir)

		// Ensure directory exists (should have been created in Phase 1)
		if stat, err := os.Stat(hostPath); os.IsNotExist(err) {
			a.logger.Warn("Coder workspace %s does not exist, creating it", hostPath)
			if mkdirErr := os.MkdirAll(hostPath, 0755); mkdirErr != nil {
				return fmt.Errorf("failed to create workspace directory %s: %w", hostPath, mkdirErr)
			}
		} else if err != nil {
			return fmt.Errorf("failed to stat workspace directory %s: %w", hostPath, err)
		} else {
			a.logger.Debug("Mounting coder workspace: %s (mode: %v)", hostPath, stat.Mode())
		}

		// Mount as read-only: host:container:ro
		containerPath := fmt.Sprintf("/mnt/coders/coder-%03d", i)
		args = append(args, "--volume", fmt.Sprintf("%s:%s:ro", hostPath, containerPath))
	}

	// Mount mirror repository (read-only)
	mirrorPath := filepath.Join(a.projectDir, ".mirrors")
	absMirrorPath, err := filepath.Abs(mirrorPath)
	if err != nil {
		return fmt.Errorf("failed to resolve mirror path: %w", err)
	}
	hostMirrorPath := normalizePath(absMirrorPath)

	// Ensure mirror directory exists
	if _, statErr := os.Stat(hostMirrorPath); os.IsNotExist(statErr) {
		a.logger.Warn("Mirror directory %s does not exist", hostMirrorPath)
		// Don't fail - mirror might not be set up yet
	} else {
		args = append(args, "--volume", fmt.Sprintf("%s:/mnt/mirror:ro", hostMirrorPath))
		a.logger.Debug("Mounted mirror directory: %s", hostMirrorPath)
	}

	// Add writable tmpfs directories, environment variables, image and command
	args = append(args,
		"--tmpfs", fmt.Sprintf("/tmp:exec,nodev,nosuid,size=%s", config.GetContainerTmpfsSize()),
		"--tmpfs", "/home:exec,nodev,nosuid,size=100m",
		"--tmpfs", "/.cache:exec,nodev,nosuid,size=100m",
		"--env", "HOME=/tmp",
		a.image, "sleep", "infinity",
	)

	// Create and start container
	cmd := exec.CommandContext(ctx, a.dockerCmd, args...)
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	a.logger.Info("Starting architect container: %s", strings.Join(cmd.Args, " "))

	output, err := cmd.CombinedOutput()
	if err != nil {
		a.logger.Error("Docker command failed: %s", strings.Join(cmd.Args, " "))
		a.logger.Error("Docker command error: %v", err)
		a.logger.Error("Docker command output: %s", string(output))
		return fmt.Errorf("failed to start architect container: %w\nOutput: %s", err, string(output))
	}

	a.containerID = strings.TrimSpace(string(output))
	a.logger.Info("Started architect container %s with ID: %s", a.containerName, a.containerID)

	// Register with global registry
	if registry := GetGlobalRegistry(); registry != nil {
		registry.Register("architect", a.containerName, "architect")
	}

	return nil
}

// Stop stops and removes the architect container.
func (a *ArchitectExecutor) Stop(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.containerID == "" {
		a.logger.Debug("No architect container to stop")
		return nil
	}

	a.logger.Info("Stopping architect container %s", a.containerName)

	// Stop the container
	stopCmd := exec.CommandContext(ctx, a.dockerCmd, "stop", a.containerName)
	if err := stopCmd.Run(); err != nil {
		a.logger.Error("Failed to stop architect container: %v", err)
	}

	// Remove the container
	rmCmd := exec.CommandContext(ctx, a.dockerCmd, "rm", "-f", a.containerName)
	if err := rmCmd.Run(); err != nil {
		a.logger.Error("Failed to remove architect container: %v", err)
	}

	// Unregister from global registry
	if registry := GetGlobalRegistry(); registry != nil {
		registry.Unregister(a.containerName)
	}

	a.containerID = ""
	a.logger.Info("Architect container stopped and removed")

	return nil
}

// Run executes a command in the architect container.
func (a *ArchitectExecutor) Run(ctx context.Context, cmd []string, opts *Opts) (Result, error) {
	start := time.Now()

	if len(cmd) == 0 {
		return Result{}, fmt.Errorf("command cannot be empty")
	}

	a.mu.RLock()
	containerName := a.containerName
	a.mu.RUnlock()

	if containerName == "" {
		return Result{}, fmt.Errorf("architect container not started")
	}

	// Build docker exec command
	// Working directory (default to /tmp)
	workDir := "/tmp"
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
	execCmd := exec.CommandContext(ctx, a.dockerCmd, args...)
	if execCmd.Env == nil {
		execCmd.Env = os.Environ()
	}

	a.logger.Debug("Executing in architect container: %s", strings.Join(args, " "))

	output, err := execCmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = 1
		}
	}

	elapsed := time.Since(start)

	result := Result{
		Stdout:   string(output),
		Stderr:   "", // Combined output
		ExitCode: exitCode,
		Duration: elapsed,
	}

	if err != nil && exitCode != 0 {
		a.logger.Debug("Command failed with exit code %d: %s", exitCode, strings.Join(cmd, " "))
	}

	return result, nil
}

// Shutdown cleans up the architect container.
func (a *ArchitectExecutor) Shutdown(ctx context.Context) error {
	return a.Stop(ctx)
}

// normalizePath normalizes paths for cross-platform Docker compatibility.
// On Windows with Docker Desktop, converts C:\path to /host_mnt/c/path format.
func normalizePath(path string) string {
	// Handle Windows paths for Docker Desktop
	if len(path) >= 2 && path[1] == ':' {
		// Convert C:\path to /host_mnt/c/path
		drive := strings.ToLower(string(path[0]))
		rest := filepath.ToSlash(path[2:])
		return "/host_mnt/" + drive + rest
	}
	return filepath.ToSlash(path)
}
