package exec

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"orchestrator/pkg/logx"
)

// DockerExec implements the Executor interface using Docker containers
type DockerExec struct {
	logger            *logx.Logger
	image             string
	dockerCmd         string
	containerPrefix   string
	mu                sync.RWMutex
	runningContainers map[string]*exec.Cmd
}

// NewDockerExec creates a new Docker executor
func NewDockerExec(image string) *DockerExec {
	logger := logx.NewLogger("docker-exec")

	// Auto-detect Docker command
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	return &DockerExec{
		logger:            logger,
		image:             image,
		dockerCmd:         dockerCmd,
		containerPrefix:   "maestro-exec-",
		runningContainers: make(map[string]*exec.Cmd),
	}
}

// Name returns the executor type name
func (d *DockerExec) Name() string {
	return "docker"
}

// Available checks if Docker is available and the daemon is running
func (d *DockerExec) Available() bool {
	// Check if docker/podman command exists
	if _, err := exec.LookPath(d.dockerCmd); err != nil {
		d.logger.Debug("Docker command not found: %v", err)
		return false
	}

	// Check if daemon is running by trying to list containers
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, d.dockerCmd, "ps", "-q")
	if err := cmd.Run(); err != nil {
		d.logger.Debug("Docker daemon not available: %v", err)
		return false
	}

	return true
}

// Run executes a command in a Docker container
func (d *DockerExec) Run(ctx context.Context, cmd []string, opts ExecOpts) (ExecResult, error) {
	start := time.Now()

	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("command cannot be empty")
	}

	// Generate unique container name
	containerName := d.generateContainerName()

	// Build docker run command
	dockerArgs, err := d.buildDockerArgs(containerName, cmd, opts)
	if err != nil {
		return ExecResult{}, fmt.Errorf("failed to build docker args: %w", err)
	}

	// Set up context with timeout
	execCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Create docker command
	dockerCmd := exec.CommandContext(execCtx, d.dockerCmd, dockerArgs...)

	// Track running container
	d.mu.Lock()
	d.runningContainers[containerName] = dockerCmd
	d.mu.Unlock()

	// Ensure cleanup
	defer func() {
		d.mu.Lock()
		delete(d.runningContainers, containerName)
		d.mu.Unlock()
		d.cleanupContainer(containerName)
	}()

	// Execute command and capture output
	stdout, stderr, err := d.executeCommand(dockerCmd)

	duration := time.Since(start)
	result := ExecResult{
		Stdout:       stdout,
		Stderr:       stderr,
		Duration:     duration,
		ExecutorUsed: d.Name(),
	}

	if err != nil {
		// Extract exit code from error
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		return result, fmt.Errorf("docker command failed: %w", err)
	}

	result.ExitCode = 0
	return result, nil
}

// generateContainerName creates a unique container name
func (d *DockerExec) generateContainerName() string {
	timestamp := time.Now().UnixNano()
	return fmt.Sprintf("%s%d", d.containerPrefix, timestamp)
}

// buildDockerArgs constructs the docker run command arguments
func (d *DockerExec) buildDockerArgs(containerName string, cmd []string, opts ExecOpts) ([]string, error) {
	args := []string{"run", "--rm", "--name", containerName}

	// Security hardening
	args = append(args, "--security-opt", "no-new-privileges")

	// Read-only root filesystem for additional security
	if opts.ReadOnly {
		args = append(args, "--read-only")
	}

	// Network configuration
	if opts.NetworkDisabled {
		args = append(args, "--network", "none")
	}

	// Resource limits
	if opts.ResourceLimits != nil {
		if opts.ResourceLimits.CPUs != "" {
			args = append(args, "--cpus", opts.ResourceLimits.CPUs)
		}
		if opts.ResourceLimits.Memory != "" {
			args = append(args, "--memory", opts.ResourceLimits.Memory)
		}
		if opts.ResourceLimits.PIDs > 0 {
			args = append(args, "--pids-limit", strconv.FormatInt(opts.ResourceLimits.PIDs, 10))
		}
	}

	// User configuration for rootless execution
	if opts.User != "" {
		args = append(args, "--user", opts.User)
	} else {
		// Use current user UID:GID for rootless execution
		uid := os.Getuid()
		gid := os.Getgid()
		args = append(args, "--user", fmt.Sprintf("%d:%d", uid, gid))
	}

	// Working directory setup
	workspaceDir := "/workspace"
	if opts.WorkDir != "" {
		// Convert to absolute path if relative
		absWorkDir, err := filepath.Abs(opts.WorkDir)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve working directory: %w", err)
		}

		// Mount working directory as read-write for agent workspaces
		// The opts.ReadOnly flag applies to the container's root filesystem, not the workspace
		mountMode := "rw"

		// Handle cross-platform path mapping
		hostPath := d.normalizePath(absWorkDir)
		args = append(args, "--volume", fmt.Sprintf("%s:%s:%s", hostPath, workspaceDir, mountMode))

		// Set working directory inside container
		args = append(args, "--workdir", workspaceDir)
	}

	// Add writable tmpfs directories
	args = append(args, "--tmpfs", "/tmp:exec,nodev,nosuid,size=100m")
	args = append(args, "--tmpfs", "/home:exec,nodev,nosuid,size=100m")
	args = append(args, "--tmpfs", "/.cache:exec,nodev,nosuid,size=100m")

	// Environment variables
	for _, env := range opts.Env {
		args = append(args, "--env", env)
	}

	// Add image
	args = append(args, d.image)

	// Add command
	args = append(args, cmd...)

	return args, nil
}

// normalizePath handles cross-platform path normalization for Docker
func (d *DockerExec) normalizePath(path string) string {
	// On Windows, convert path for Docker Desktop
	if runtime.GOOS == "windows" {
		// Convert C:\path\to\dir to /c/path/to/dir
		if len(path) > 2 && path[1] == ':' {
			drive := strings.ToLower(string(path[0]))
			rest := strings.ReplaceAll(path[2:], "\\", "/")
			return "/" + drive + rest
		}
	}
	return path
}

// executeCommand runs the docker command and captures output
func (d *DockerExec) executeCommand(cmd *exec.Cmd) (string, string, error) {
	var stdout, stderr strings.Builder

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	d.logger.Debug("Executing docker command: %s", strings.Join(cmd.Args, " "))

	err := cmd.Run()

	// Log stderr if command failed
	if err != nil {
		d.logger.Error("Docker command failed: %s", stderr.String())
	}

	return stdout.String(), stderr.String(), err
}

// cleanupContainer removes the container if it's still running
func (d *DockerExec) cleanupContainer(containerName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Try to stop the container
	stopCmd := exec.CommandContext(ctx, d.dockerCmd, "stop", containerName)
	if err := stopCmd.Run(); err != nil {
		d.logger.Debug("Failed to stop container %s: %v", containerName, err)
	}

	// Try to remove the container
	rmCmd := exec.CommandContext(ctx, d.dockerCmd, "rm", "-f", containerName)
	if err := rmCmd.Run(); err != nil {
		d.logger.Debug("Failed to remove container %s: %v", containerName, err)
	}
}

// Shutdown gracefully stops all running containers
func (d *DockerExec) Shutdown(ctx context.Context) error {
	d.mu.RLock()
	containers := make([]string, 0, len(d.runningContainers))
	for name := range d.runningContainers {
		containers = append(containers, name)
	}
	d.mu.RUnlock()

	var wg sync.WaitGroup
	for _, containerName := range containers {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			d.cleanupContainer(name)
		}(containerName)
	}

	// Wait for cleanup with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// GetImage returns the Docker image being used
func (d *DockerExec) GetImage() string {
	return d.image
}

// SetImage updates the Docker image
func (d *DockerExec) SetImage(image string) {
	d.image = image
}
