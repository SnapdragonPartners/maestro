// Package tools provides execution strategy framework for host vs container tool execution.
package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/config"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
)

// ExecStrategy represents where a tool should execute.
type ExecStrategy string

const (
	// ExecContainer executes tool inside the agent's container (default).
	ExecContainer ExecStrategy = "container"
	// ExecHost executes tool directly on the host (for privileged operations).
	ExecHost ExecStrategy = "host"
)

// HostRunner executes tools directly on the host with access to host Docker daemon.
type HostRunner struct {
	logger *logx.Logger
}

// NewHostRunner creates a new host execution runner.
func NewHostRunner() *HostRunner {
	return &HostRunner{
		logger: logx.NewLogger("host-runner"),
	}
}

// RunContainerTest executes container_test tool on the host with full Docker access.
func (r *HostRunner) RunContainerTest(ctx context.Context, args map[string]any) (*ExecResult, error) {
	r.logger.Info("🖥️  Executing container_test on host (Strategy A)")

	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract command (optional)
	command, _ := args["command"].(string)

	// Extract TTL for persistent containers
	ttlSeconds, _ := args["ttl_seconds"].(float64)

	// Extract timeout
	timeoutSeconds, _ := args["timeout_seconds"].(float64)
	if timeoutSeconds == 0 {
		timeoutSeconds = 60 // Default timeout
	}

	// Extract working directory (default to /workspace)
	workingDir, _ := args["working_dir"].(string)
	if workingDir == "" {
		workingDir = DefaultWorkspaceDir
	}

	// Extract workspace mount from context (this should be the host path)
	hostWorkspace, _ := args["host_workspace_path"].(string)
	if hostWorkspace == "" {
		hostWorkspace = "." // Fallback to current directory
	}

	// Extract mount permissions (ro/rw based on agent state)
	mountPermissions, _ := args["mount_permissions"].(string)
	if mountPermissions == "" {
		mountPermissions = "ro" // Default to read-only for safety
	}

	// Determine mode and execute
	if command != "" {
		// Command execution mode
		return r.executeCommand(ctx, containerName, command, workingDir, hostWorkspace, mountPermissions, int(timeoutSeconds))
	} else if ttlSeconds > 0 {
		// Persistent container mode
		return r.startPersistentContainer(ctx, containerName, workingDir, hostWorkspace, mountPermissions, int(ttlSeconds))
	} else {
		// Boot test mode
		return r.performBootTest(ctx, containerName, workingDir, hostWorkspace, mountPermissions, int(timeoutSeconds))
	}
}

// executeCommand runs a command in a container and returns results.
func (r *HostRunner) executeCommand(ctx context.Context, containerName, command, workingDir, hostWorkspace, mountPermissions string, timeoutSec int) (*ExecResult, error) {
	r.logger.Info("🧪 container_test command: container='%s', command='%s', workDir='%s', hostWorkspace='%s', permissions='%s', timeout=%ds",
		containerName, command, workingDir, hostWorkspace, mountPermissions, timeoutSec)
	timeout := time.Duration(timeoutSec) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build docker run command
	args := []string{
		"docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/workspace:%s", hostWorkspace, mountPermissions),
		"-w", workingDir,
		"--tmpfs", fmt.Sprintf("/tmp:rw,noexec,nosuid,size=%s", config.GetContainerTmpfsSize()),
		containerName,
		"sh", "-c", command,
	}

	r.logger.Info("🐳 Executing: %s", strings.Join(args, " "))
	r.logger.Info("📂 Host workspace: %s, Mount permissions: %s", hostWorkspace, mountPermissions)

	cmd := exec.CommandContext(execCtx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode = exitError.ExitCode()
		} else {
			response := map[string]any{
				"success":        false,
				"container_name": containerName,
				"command":        command,
				"working_dir":    workingDir,
				"host_workspace": hostWorkspace,
				"permissions":    mountPermissions,
				"timeout":        timeoutSec,
				"error":          fmt.Sprintf("Command execution failed: %v", err),
			}
			content, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
			}
			return &ExecResult{Content: string(content)}, nil
		}
	}

	result := map[string]any{
		"success":        exitCode == 0,
		"container_name": containerName,
		"command":        command,
		"working_dir":    workingDir,
		"host_workspace": hostWorkspace,
		"permissions":    mountPermissions,
		"exit_code":      exitCode,
		"timeout":        timeoutSec,
		"stdout":         stdout.String(),
		"stderr":         stderr.String(),
		"host_executed":  true,
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// startPersistentContainer starts a long-running container and returns network information.
func (r *HostRunner) startPersistentContainer(ctx context.Context, containerName, workingDir, hostWorkspace, mountPermissions string, ttlSeconds int) (*ExecResult, error) {
	// Generate unique container name to avoid collisions
	uniqueName := r.generateUniqueContainerName(containerName)

	// Build docker run command for detached container
	args := []string{
		"docker", "run", "-d", "--name", uniqueName,
		"-v", fmt.Sprintf("%s:/workspace:%s", hostWorkspace, mountPermissions),
		"-w", workingDir,
		"--tmpfs", fmt.Sprintf("/tmp:rw,noexec,nosuid,size=%s", config.GetContainerTmpfsSize()),
		containerName,
		"sleep", fmt.Sprintf("%d", ttlSeconds),
	}

	r.logger.Info("🐳 Starting persistent container: %s", strings.Join(args, " "))
	r.logger.Info("📂 Host workspace: %s, Mount permissions: %s", hostWorkspace, mountPermissions)

	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		response := map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to start container: %v", err),
			"stdout":  stdout.String(),
			"stderr":  stderr.String(),
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Get container ID from output
	containerID := strings.TrimSpace(stdout.String())

	// Register with container registry for cleanup
	registry := execpkg.GetGlobalRegistry()
	if registry != nil {
		registry.Register(containerID, uniqueName, "container_test_persistent")
		r.logger.Info("📝 Registered container %s (%s) with registry for cleanup", uniqueName, containerID)
	}

	// Get network information
	networkInfo, err := r.getContainerNetworkInfo(ctx, containerID)
	if err != nil {
		r.logger.Warn("⚠️  Failed to get network info for container %s: %v", containerID, err)
		networkInfo = map[string]any{"error": err.Error()}
	}

	result := map[string]any{
		"success":        true,
		"container_name": containerName,
		"container_id":   containerID,
		"ttl_seconds":    ttlSeconds,
		"mode":           "persistent",
		"host_executed":  true,
		"network_info":   networkInfo,
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// performBootTest tests that a container starts successfully and has required capabilities.
func (r *HostRunner) performBootTest(ctx context.Context, containerName, workingDir, hostWorkspace, mountPermissions string, timeoutSec int) (*ExecResult, error) {
	r.logger.Info("🧪 container_test boot test: container='%s', workDir='%s', hostWorkspace='%s', permissions='%s', timeout=%ds",
		containerName, workingDir, hostWorkspace, mountPermissions, timeoutSec)
	// First validate container capabilities before doing boot test
	// Use local executor for validation (runs docker commands on host)
	hostExecutor := execpkg.NewLocalExec()
	validationResult := ValidateContainerCapabilities(ctx, hostExecutor, containerName)

	// If validation fails, return immediately with detailed error
	if !validationResult.Success {
		r.logger.Error("❌ Container '%s' validation failed: %s", containerName, validationResult.Message)
		for tool, details := range validationResult.ErrorDetails {
			r.logger.Error("   - %s: %s", tool, details)
		}
		response := map[string]any{
			"success":        false,
			"container_name": containerName,
			"working_dir":    workingDir,
			"host_workspace": hostWorkspace,
			"permissions":    mountPermissions,
			"timeout":        timeoutSec,
			"mode":           "boot_test_with_validation",
			"validation":     validationResult,
			"host_executed":  true,
			"message":        fmt.Sprintf("Container '%s' failed capability validation: %s", containerName, validationResult.Message),
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}
	r.logger.Info("✅ Container '%s' validation passed: git available, GitHub CLI available, GitHub API accessible", containerName)
	timeout := time.Duration(timeoutSec) * time.Second
	execCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Build docker run command for boot test
	args := []string{
		"docker", "run", "--rm",
		"-v", fmt.Sprintf("%s:/workspace:%s", hostWorkspace, mountPermissions),
		"-w", workingDir,
		"--tmpfs", fmt.Sprintf("/tmp:rw,noexec,nosuid,size=%s", config.GetContainerTmpfsSize()),
		containerName,
	}

	r.logger.Info("🐳 Boot testing: %s", strings.Join(args, " "))
	r.logger.Info("📂 Host workspace: %s, Mount permissions: %s", hostWorkspace, mountPermissions)

	cmd := exec.CommandContext(execCtx, args[0], args[1:]...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// For boot test, timeout is expected (container should run until killed)
	if err != nil {
		if execCtx.Err() == context.DeadlineExceeded {
			// Container ran successfully until timeout - this is success for boot test
			result := map[string]any{
				"success":        true,
				"container_name": containerName,
				"working_dir":    workingDir,
				"host_workspace": hostWorkspace,
				"permissions":    mountPermissions,
				"mode":           "boot_test_with_validation",
				"timeout":        timeoutSec,
				"host_executed":  true,
				"validation":     validationResult,
				"message":        fmt.Sprintf("Container '%s' booted successfully and passed validation", containerName),
			}
			content, marshalErr := json.Marshal(result)
			if marshalErr != nil {
				return nil, fmt.Errorf("failed to marshal result: %w", marshalErr)
			}
			return &ExecResult{Content: string(content)}, nil
		}
	}

	// Container exited early or had error
	exitCode := 0
	var exitError *exec.ExitError
	if errors.As(err, &exitError) {
		exitCode = exitError.ExitCode()
	}

	response := map[string]any{
		"success":        false,
		"container_name": containerName,
		"working_dir":    workingDir,
		"host_workspace": hostWorkspace,
		"permissions":    mountPermissions,
		"mode":           "boot_test",
		"exit_code":      exitCode,
		"timeout":        timeoutSec,
		"stdout":         stdout.String(),
		"stderr":         stderr.String(),
		"host_executed":  true,
		"message":        fmt.Sprintf("Container '%s' failed boot test", containerName),
	}

	content, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal error response: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// getContainerNetworkInfo inspects a running container and returns network details.
func (r *HostRunner) getContainerNetworkInfo(ctx context.Context, containerID string) (map[string]any, error) {
	// Get container inspection data
	cmd := exec.CommandContext(ctx, "docker", "inspect", containerID)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("docker inspect failed: %w", err)
	}

	// Parse JSON output
	var inspectData []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &inspectData); err != nil {
		return nil, fmt.Errorf("failed to parse inspect output: %w", err)
	}

	if len(inspectData) == 0 {
		return nil, fmt.Errorf("no container data returned")
	}

	container := inspectData[0]

	// Extract network settings
	networkSettings, ok := container["NetworkSettings"].(map[string]any)
	if !ok {
		return map[string]any{"error": "NetworkSettings not found"}, nil
	}

	// Get IP address
	ipAddress, _ := networkSettings["IPAddress"].(string)

	// Get ports
	ports, _ := networkSettings["Ports"].(map[string]any)

	// Get networks
	networks, _ := networkSettings["Networks"].(map[string]any)

	return map[string]any{
		"ip_address":      ipAddress,
		"exposed_ports":   ports,
		"networks":        networks,
		"container_id":    containerID,
		"inspection_time": time.Now().Format(time.RFC3339),
	}, nil
}

// generateUniqueContainerName creates a unique container name to avoid collisions.
func (r *HostRunner) generateUniqueContainerName(baseName string) string {
	// Generate random suffix (6 hex chars = 24 bits of randomness)
	randomBytes := make([]byte, 3)
	if _, err := rand.Read(randomBytes); err != nil {
		// Fallback to timestamp if crypto/rand fails
		return fmt.Sprintf("%s-%d", baseName, time.Now().UnixNano())
	}

	randomSuffix := hex.EncodeToString(randomBytes)
	return fmt.Sprintf("%s-%s", baseName, randomSuffix)
}
