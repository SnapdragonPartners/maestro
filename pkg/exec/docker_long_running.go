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

// LongRunningDockerExec implements the Executor interface using long-running Docker containers
// Each container persists for the duration of a story, allowing state to be preserved between commands
type LongRunningDockerExec struct {
	logger           *logx.Logger
	image            string
	dockerCmd        string
	containerPrefix  string
	mu               sync.RWMutex
	activeContainers map[string]*ContainerInfo // key: container ID, value: container info
}

// ContainerInfo holds information about a running container
type ContainerInfo struct {
	ID        string
	Name      string
	WorkDir   string
	CreatedAt time.Time
	LastUsed  time.Time
}

// NewLongRunningDockerExec creates a new long-running Docker executor
func NewLongRunningDockerExec(image string) *LongRunningDockerExec {
	logger := logx.NewLogger("docker-longrunning")

	// Auto-detect Docker command
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	return &LongRunningDockerExec{
		logger:           logger,
		image:            image,
		dockerCmd:        dockerCmd,
		containerPrefix:  "maestro-story-",
		activeContainers: make(map[string]*ContainerInfo),
	}
}

// Name returns the executor type name
func (d *LongRunningDockerExec) Name() ExecutorType {
	return ExecutorTypeDocker
}

// Available checks if Docker is available and the daemon is running
func (d *LongRunningDockerExec) Available() bool {
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

// StartContainer creates and starts a new long-running container for a story
func (d *LongRunningDockerExec) StartContainer(ctx context.Context, storyID string, opts ExecOpts) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// Generate container name
	containerName := fmt.Sprintf("%s%s", d.containerPrefix, storyID)

	// Check if container already exists in our tracking
	if info, exists := d.activeContainers[containerName]; exists {
		d.logger.Info("Container %s already exists, reusing", containerName)
		info.LastUsed = time.Now()
		return containerName, nil
	}

	// Remove any existing container with the same name (from previous runs)
	// This handles cases where the container exists in Docker but not in our tracking
	d.logger.Debug("Removing any existing container with name %s", containerName)
	rmCmd := exec.CommandContext(ctx, d.dockerCmd, "rm", "-f", containerName)
	if err := rmCmd.Run(); err != nil {
		// Log but don't fail - the container might not exist
		d.logger.Debug("Failed to remove existing container %s (this is normal if it doesn't exist): %v", containerName, err)
	}

	// Build docker run command for long-running container
	args := []string{"run", "-d", "--name", containerName}

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
			return "", fmt.Errorf("failed to resolve working directory: %w", err)
		}

		// Mount working directory - mode determined by opts.ReadOnly
		mountMode := "rw"
		if opts.ReadOnly {
			mountMode = "ro"
		}

		// Handle cross-platform path mapping
		hostPath := d.normalizePath(absWorkDir)

		// Resolve any symlinks that might cause Docker Desktop issues
		if resolvedPath, err := filepath.EvalSymlinks(hostPath); err == nil {
			if resolvedPath != hostPath {
				d.logger.Info("Resolved symlink: %s -> %s", hostPath, resolvedPath)
				hostPath = resolvedPath
			}
		} else {
			d.logger.Warn("Failed to resolve symlinks for %s: %v", hostPath, err)
		}

		d.logger.Debug("Checking workspace directory: %s", hostPath)

		// Ensure the workspace directory exists before mounting (critical for Docker Desktop)
		// Docker Desktop will fail with "/host_mnt/" errors if the directory doesn't exist
		if stat, err := os.Stat(hostPath); os.IsNotExist(err) {
			d.logger.Info("Directory does not exist, creating: %s", hostPath)
			if err := os.MkdirAll(hostPath, 0755); err != nil {
				return "", fmt.Errorf("failed to create workspace directory %s: %w", hostPath, err)
			}
			d.logger.Info("Created workspace directory: %s", hostPath)
		} else if err != nil {
			return "", fmt.Errorf("failed to stat workspace directory %s: %w", hostPath, err)
		} else {
			d.logger.Debug("Directory exists: %s (mode: %v)", hostPath, stat.Mode())
		}

		// Wait for Docker Desktop's gRPC-FUSE layer to see the directory
		// This fixes timing issues where newly created directories aren't immediately
		// visible to Docker Desktop's VM, causing "/host_mnt/" mount failures
		if err := d.waitUntilDockerCanMount(hostPath, 5*time.Second); err != nil {
			return "", fmt.Errorf("directory not accessible to Docker Desktop: %w", err)
		}

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

	// Add image and command (sleep to keep container running)
	args = append(args, d.image, "sleep", "infinity")

	// Create and start container
	cmd := exec.CommandContext(ctx, d.dockerCmd, args...)

	// Ensure Docker has access to necessary environment variables
	if cmd.Env == nil {
		cmd.Env = os.Environ()
	}

	d.logger.Info("Starting long-running container: %s", strings.Join(cmd.Args, " "))

	// Debug: Log working directory and environment
	workDir := cmd.Dir
	if workDir == "" {
		workDir = "<empty>"
	}
	d.logger.Debug("Docker command working directory: %s", workDir)
	d.logger.Debug("Docker command environment: %v", cmd.Env)

	output, err := cmd.CombinedOutput()
	if err != nil {
		cmdString := strings.Join(cmd.Args, " ")
		if cmdString == "" {
			cmdString = "<empty command args>"
		}
		d.logger.Error("Docker command failed: %s", cmdString)
		d.logger.Error("Docker command error: %v", err)
		d.logger.Error("Docker command output: %s", string(output))

		// Try to see if the container was created but failed
		checkCmd := exec.Command(d.dockerCmd, "ps", "-a", "--filter", "name="+containerName, "--format", "{{.Status}}")
		if checkOutput, checkErr := checkCmd.CombinedOutput(); checkErr == nil {
			d.logger.Info("Container status after failure: %s", string(checkOutput))
		}

		return "", fmt.Errorf("failed to start container %s: %w\nOutput: %s", containerName, err, string(output))
	}

	containerID := strings.TrimSpace(string(output))
	d.logger.Info("Started container %s with ID: %s", containerName, containerID)

	// Store container info
	d.activeContainers[containerName] = &ContainerInfo{
		ID:        containerID,
		Name:      containerName,
		WorkDir:   opts.WorkDir,
		CreatedAt: time.Now(),
		LastUsed:  time.Now(),
	}

	return containerName, nil
}

// StopContainer stops and removes a long-running container
func (d *LongRunningDockerExec) StopContainer(ctx context.Context, containerName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	info, exists := d.activeContainers[containerName]
	if !exists {
		d.logger.Warn("Container %s not found in active containers", containerName)
		return nil
	}

	d.logger.Info("Stopping container %s", containerName)

	// Stop the container
	stopCmd := exec.CommandContext(ctx, d.dockerCmd, "stop", containerName)
	if err := stopCmd.Run(); err != nil {
		d.logger.Error("Failed to stop container %s: %v", containerName, err)
	}

	// Remove the container
	rmCmd := exec.CommandContext(ctx, d.dockerCmd, "rm", "-f", containerName)
	if err := rmCmd.Run(); err != nil {
		d.logger.Error("Failed to remove container %s: %v", containerName, err)
	}

	// Remove from active containers
	delete(d.activeContainers, containerName)

	d.logger.Info("Container %s stopped and removed (was active for %v)",
		containerName, time.Since(info.CreatedAt))

	return nil
}

// Run executes a command in an existing long-running container
func (d *LongRunningDockerExec) Run(ctx context.Context, cmd []string, opts ExecOpts) (ExecResult, error) {
	start := time.Now()

	if len(cmd) == 0 {
		return ExecResult{}, fmt.Errorf("command cannot be empty")
	}

	// For long-running containers, we need a story ID to identify the container
	// Try to get it from context first, then from active containers
	storyID := d.getStoryIDFromContext(ctx)
	var containerName string

	if storyID != "" {
		containerName = fmt.Sprintf("%s%s", d.containerPrefix, storyID)
	} else {
		// If no story ID in context, use the first available container
		d.mu.RLock()
		for name := range d.activeContainers {
			containerName = name
			break
		}
		d.mu.RUnlock()

		if containerName == "" {
			return ExecResult{}, fmt.Errorf("no active containers found and no story ID in context")
		}
	}

	// Check if container exists
	d.mu.RLock()
	info, exists := d.activeContainers[containerName]
	d.mu.RUnlock()

	if !exists {
		return ExecResult{}, fmt.Errorf("container %s not found - call StartContainer first", containerName)
	}

	// Update last used time
	d.mu.Lock()
	info.LastUsed = time.Now()
	d.mu.Unlock()

	// Set up context with timeout
	execCtx := ctx
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	// Build docker exec command
	execArgs := []string{"exec", "-i"}

	// Set working directory if specified
	if opts.WorkDir != "" {
		execArgs = append(execArgs, "--workdir", "/workspace")
	}

	// Add container name and command
	execArgs = append(execArgs, containerName)
	execArgs = append(execArgs, cmd...)

	// Execute command
	dockerCmd := exec.CommandContext(execCtx, d.dockerCmd, execArgs...)
	stdout, stderr, err := d.executeCommand(dockerCmd)

	duration := time.Since(start)
	result := ExecResult{
		Stdout:       stdout,
		Stderr:       stderr,
		Duration:     duration,
		ExecutorUsed: string(d.Name()),
	}

	if err != nil {
		// Extract exit code from error
		if exitErr, ok := err.(*exec.ExitError); ok {
			result.ExitCode = exitErr.ExitCode()
		} else {
			result.ExitCode = 1
		}
		return result, fmt.Errorf("docker exec failed: %w", err)
	}

	result.ExitCode = 0
	return result, nil
}

// executeCommand runs the docker command and captures output
func (d *LongRunningDockerExec) executeCommand(cmd *exec.Cmd) (string, string, error) {
	var stdout, stderr strings.Builder

	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	d.logger.Debug("Executing docker command: %s", strings.Join(cmd.Args, " "))

	err := cmd.Run()

	// Log stderr if command failed
	if err != nil {
		cmdString := strings.Join(cmd.Args, " ")
		if cmdString == "" {
			cmdString = "<empty exec command args>"
		}
		d.logger.Warn("Docker exec command failed: %s", cmdString)
		d.logger.Warn("Docker exec error: %v", err)
		d.logger.Warn("Docker exec stderr: %s", stderr.String())
	}

	return stdout.String(), stderr.String(), err
}

// normalizePath handles cross-platform path normalization for Docker
func (d *LongRunningDockerExec) normalizePath(path string) string {
	// On Windows, convert path for Docker Desktop
	if runtime.GOOS == "windows" {
		// Convert C:\path\to\dir to /c/path/to/dir
		if len(path) > 2 && path[1] == ':' {
			drive := strings.ToLower(string(path[0]))
			rest := strings.ReplaceAll(path[2:], "\\", "/")
			return "/" + drive + rest
		}
	}

	// On macOS with Docker Desktop, ensure path is absolute and clean
	// Docker Desktop on macOS automatically maps /Users, /Volumes, /tmp, and /var/folders
	if runtime.GOOS == "darwin" {
		// Clean the path to avoid Docker Desktop path resolution issues
		cleanPath := filepath.Clean(path)

		// Ensure path starts with one of the shared directories Docker Desktop supports
		allowedPrefixes := []string{"/Users", "/Volumes", "/tmp", "/var/folders", "/private/tmp"}
		for _, prefix := range allowedPrefixes {
			if strings.HasPrefix(cleanPath, prefix) {
				return cleanPath
			}
		}

		// If path doesn't start with a shared directory, Docker Desktop won't be able to mount it
		d.logger.Warn("Path %s may not be accessible to Docker Desktop on macOS. Ensure it's under /Users, /Volumes, /tmp, or /var/folders", cleanPath)
		return cleanPath
	}

	return path
}

// waitUntilDockerCanMount waits until Docker Desktop's gRPC-FUSE layer can see the directory
// This prevents "/host_mnt/" errors when mounting newly created directories
func (d *LongRunningDockerExec) waitUntilDockerCanMount(hostPath string, timeout time.Duration) error {
	d.logger.Debug("Waiting for Docker Desktop to see directory: %s", hostPath)
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		// Test if Docker can mount the directory by doing a quick test mount
		testCmd := exec.Command(d.dockerCmd, "run", "--rm", "-v", hostPath+":/test:ro", "alpine", "true")

		// Ensure test command has environment variables
		if testCmd.Env == nil {
			testCmd.Env = os.Environ()
		}

		if err := testCmd.Run(); err == nil {
			d.logger.Debug("Docker Desktop can now mount directory: %s", hostPath)
			return nil // Docker can see the directory
		} else {
			d.logger.Debug("Docker mount test failed (expected while waiting): %s", strings.Join(testCmd.Args, " "))
		}

		// Also try touching parent directory to trigger gRPC-FUSE rescan
		if parentDir := filepath.Dir(hostPath); parentDir != hostPath {
			os.Chtimes(parentDir, time.Now(), time.Now())
		}

		time.Sleep(200 * time.Millisecond)
	}

	return fmt.Errorf("directory %s did not become mountable within %v", hostPath, timeout)
}

// Context key type for story ID
type contextKey string

const (
	contextKeyStoryID contextKey = "story_id"
)

// getStoryIDFromContext extracts story ID from context
func (d *LongRunningDockerExec) getStoryIDFromContext(ctx context.Context) string {
	if storyID := ctx.Value(contextKeyStoryID); storyID != nil {
		if id, ok := storyID.(string); ok {
			return id
		}
	}
	return ""
}

// Shutdown gracefully stops all running containers
func (d *LongRunningDockerExec) Shutdown(ctx context.Context) error {
	d.mu.RLock()
	containerNames := make([]string, 0, len(d.activeContainers))
	for name := range d.activeContainers {
		containerNames = append(containerNames, name)
	}
	d.mu.RUnlock()

	d.logger.Info("Shutting down %d active containers", len(containerNames))

	var wg sync.WaitGroup
	for _, containerName := range containerNames {
		wg.Add(1)
		go func(name string) {
			defer wg.Done()
			if err := d.StopContainer(ctx, name); err != nil {
				d.logger.Error("Failed to stop container %s during shutdown: %v", name, err)
			}
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
		d.logger.Info("All containers stopped successfully")
		return nil
	case <-ctx.Done():
		d.logger.Error("Container shutdown timed out")
		return ctx.Err()
	}
}

// GetImage returns the Docker image being used
func (d *LongRunningDockerExec) GetImage() string {
	return d.image
}

// SetImage updates the Docker image
func (d *LongRunningDockerExec) SetImage(image string) {
	d.image = image
}

// GetActiveContainers returns information about currently active containers
func (d *LongRunningDockerExec) GetActiveContainers() map[string]*ContainerInfo {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]*ContainerInfo)
	for name, info := range d.activeContainers {
		result[name] = info
	}
	return result
}
