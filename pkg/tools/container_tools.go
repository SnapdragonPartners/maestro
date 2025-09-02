package tools

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// IMPORTANT: Tool Error Handling Pattern
// All MCP tools should return structured responses with success/error details instead of (nil, error).
// When commands fail, return map[string]any with:
//   - "success": false
//   - "error": error message
//   - "stdout": command output (when available)
//   - "stderr": command errors (when available)
// This gives LLMs full context to understand failures and avoid repeating the same mistakes.
// Only return (nil, error) for parameter validation errors, not execution failures.

const (
	// DefaultDockerfile is the standard Dockerfile name.
	DefaultDockerfile = "Dockerfile"
	// DefaultWorkspaceDir is the standard workspace directory inside containers.
	DefaultWorkspaceDir = "/workspace"
)

// ContainerBuildTool provides MCP interface for building Docker containers from Dockerfile.
type ContainerBuildTool struct {
	executor exec.Executor
}

// ContainerUpdateTool provides MCP interface for updating container configuration.
type ContainerUpdateTool struct {
	executor exec.Executor
}

// ContainerTestTool provides unified MCP interface for container testing with optional TTL and command execution.
type ContainerTestTool struct {
	executor   exec.Executor
	hostRunner *HostRunner // For host execution strategy
	agent      Agent       // Optional agent reference for state access
	workDir    string      // Agent working directory for host mounts
}

// ContainerListTool lists available containers and their registry status.
type ContainerListTool struct {
	executor exec.Executor
}

// ContainerSwitchTool switches the coder agent execution environment to a different container.
type ContainerSwitchTool struct {
	executor exec.Executor
}

// NewContainerBuildTool creates a new container build tool instance.
func NewContainerBuildTool(executor exec.Executor) *ContainerBuildTool {
	return &ContainerBuildTool{executor: executor}
}

// NewContainerUpdateTool creates a new container update tool instance.
func NewContainerUpdateTool(executor exec.Executor) *ContainerUpdateTool {
	return &ContainerUpdateTool{executor: executor}
}

// NewContainerTestTool creates a new container test tool instance.
func NewContainerTestTool(executor exec.Executor) *ContainerTestTool {
	return &ContainerTestTool{
		executor:   executor,
		hostRunner: NewHostRunner(),
	}
}

// NewContainerTestToolWithAgent creates a new container test tool instance with agent state access.
func NewContainerTestToolWithAgent(executor exec.Executor, agent Agent) *ContainerTestTool {
	return &ContainerTestTool{
		executor:   executor,
		agent:      agent,
		workDir:    ".",
		hostRunner: NewHostRunner(),
	}
}

// NewContainerTestToolWithContext creates a new container test tool instance with full context.
func NewContainerTestToolWithContext(executor exec.Executor, agent Agent, workDir string) *ContainerTestTool {
	return &ContainerTestTool{
		executor:   executor,
		agent:      agent,
		workDir:    workDir,
		hostRunner: NewHostRunner(),
	}
}

// NewContainerListTool creates a new container list tool instance.
func NewContainerListTool(executor exec.Executor) *ContainerListTool {
	return &ContainerListTool{executor: executor}
}

// NewContainerSwitchTool creates a new container switch tool instance.
func NewContainerSwitchTool(executor exec.Executor) *ContainerSwitchTool {
	return &ContainerSwitchTool{executor: executor}
}

// Definition returns the tool's definition in Claude API format.
func (c *ContainerBuildTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_build",
		Description: "Build Docker container from Dockerfile using buildx with proper validation and testing",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory containing Dockerfile (defaults to current directory)",
				},
				"container_name": {
					Type:        "string",
					Description: "Name to tag the built container (e.g., 'maestro-hello-dev')",
				},
				"dockerfile_path": {
					Type:        "string",
					Description: "Path to dockerfile relative to cwd (defaults to 'Dockerfile')",
				},
				"platform": {
					Type:        "string",
					Description: "Target platform for multi-arch builds (e.g., 'linux/amd64', 'linux/arm64')",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerBuildTool) Name() string {
	return "container_build"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerBuildTool) PromptDocumentation() string {
	return `- **container_build** - Build Docker container from Dockerfile using buildx
  - Parameters:
    - container_name (required): name to tag the built container
    - cwd (optional): working directory containing dockerfile
    - dockerfile_path (optional): path to dockerfile (defaults to 'Dockerfile')
    - platform (optional): target platform for multi-arch builds
  - Builds container using Docker buildx with validation and testing
  - Use for DevOps stories that need to build platform-specific containers
  - Avoids legacy docker build deprecation warnings`
}

// extractWorkingDirectory extracts and validates the working directory from args.
func extractWorkingDirectory(args map[string]any) string {
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	if cwd == "" {
		// Default to configured workspace path - all agent operations run inside containers
		workspacePath, err := config.GetContainerWorkspacePath()
		if err != nil {
			// Fallback to standard workspace path if config not available
			cwd = DefaultWorkspaceDir
		} else {
			cwd = workspacePath
		}
	}

	return cwd
}

// Exec executes the container build operation.
//
//nolint:cyclop // Temporary debugging code increases complexity
func (c *ContainerBuildTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := extractWorkingDirectory(args)

	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract dockerfile path
	dockerfilePath := DefaultDockerfile
	if path, ok := args["dockerfile_path"].(string); ok && path != "" {
		dockerfilePath = path
	}

	// Extract platform
	platform := ""
	if p, ok := args["platform"].(string); ok && p != "" {
		platform = p
	}

	log.Printf("DEBUG container_build: cwd=%s, dockerfilePath=%s, containerName=%s", cwd, dockerfilePath, containerName)

	// Skip dockerfile existence check - docker build will validate and provide clear error messages
	log.Printf("DEBUG container_build: skipping existence check, docker will validate dockerfile: %s", dockerfilePath)

	// Calculate the dockerfile path relative to cwd for docker command
	relDockerfilePath := dockerfilePath
	if filepath.IsAbs(dockerfilePath) {
		var err error
		relDockerfilePath, err = filepath.Rel(cwd, dockerfilePath)
		if err != nil {
			// If we can't make it relative, use absolute path
			relDockerfilePath = dockerfilePath
		}
	}

	return c.buildAndTestContainer(ctx, cwd, containerName, relDockerfilePath, platform)
}

// buildAndTestContainer builds and tests a container, returning the result map.
func (c *ContainerBuildTool) buildAndTestContainer(ctx context.Context, cwd, containerName, dockerfilePath, platform string) (any, error) {
	// Build the container
	if err := c.buildContainer(ctx, cwd, containerName, dockerfilePath, platform); err != nil {
		// Return structured response with build failure details (error already includes stdout/stderr)
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"platform":       platform,
			"error":          fmt.Sprintf("Failed to build container: %v", err),
			"stage":          "build",
		}, nil
	}

	// Test the container
	if err := c.testContainer(ctx, containerName); err != nil {
		// Return structured response with test failure details (error already includes stdout/stderr)
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"platform":       platform,
			"error":          fmt.Sprintf("Container built successfully but failed testing: %v", err),
			"stage":          "test",
		}, nil
	}

	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"dockerfile":     dockerfilePath,
		"platform":       platform,
		"message":        fmt.Sprintf("Successfully built container '%s'", containerName),
	}, nil
}

// buildContainer builds the Docker container from the specified dockerfile using buildx or docker build as fallback.
func (c *ContainerBuildTool) buildContainer(ctx context.Context, cwd, containerName, dockerfilePath, platform string) error {
	// Get config to check buildx availability
	cfg, err := config.GetConfig()
	if err != nil {
		log.Printf("WARNING: Failed to get config, defaulting to docker build: %v", err)
		return c.buildWithDockerBuild(ctx, cwd, containerName, dockerfilePath, platform)
	}

	// Check if multi-platform build is requested but buildx not available
	if platform != "" && (cfg.Container == nil || !cfg.Container.BuildxAvailable) {
		return fmt.Errorf("multi-platform builds require buildx, but buildx is not available on this host")
	}

	// Use buildx if available, otherwise fall back to docker build
	if cfg.Container != nil && cfg.Container.BuildxAvailable {
		return c.buildWithBuildx(ctx, cwd, containerName, dockerfilePath, platform)
	} else {
		log.Printf("INFO: Using docker build (buildx not available)")
		return c.buildWithDockerBuild(ctx, cwd, containerName, dockerfilePath, platform)
	}
}

// buildWithBuildx builds using docker buildx.
func (c *ContainerBuildTool) buildWithBuildx(ctx context.Context, cwd, containerName, dockerfilePath, platform string) error {
	args := []string{"docker", "buildx", "build", "-t", containerName, "-f", dockerfilePath}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, "--load", ".")

	opts := &exec.Opts{
		WorkDir: cwd,
		Timeout: 5 * time.Minute,
		Env:     []string{"DOCKER_CONFIG=/tmp/docker"}, // Use writable location
	}

	result, err := c.executor.Run(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("docker buildx build failed: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("docker buildx build failed with exit code %d (stdout: %s, stderr: %s)", result.ExitCode, result.Stdout, result.Stderr)
	}
	return nil
}

// buildWithDockerBuild builds using legacy docker build with BuildKit enabled.
func (c *ContainerBuildTool) buildWithDockerBuild(ctx context.Context, cwd, containerName, dockerfilePath, _ string) error {
	args := []string{"docker", "build", "-t", containerName, "-f", dockerfilePath}
	// Note: --platform not supported in legacy docker build (parameter ignored)
	args = append(args, ".")

	opts := &exec.Opts{
		WorkDir: cwd,
		Timeout: 5 * time.Minute,
		Env:     []string{"DOCKER_BUILDKIT=1"}, // Enable BuildKit for legacy build
	}

	result, err := c.executor.Run(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("docker build failed: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("docker build failed with exit code %d (stdout: %s, stderr: %s)", result.ExitCode, result.Stdout, result.Stderr)
	}
	return nil
}

// testContainer performs basic validation that the container works.
func (c *ContainerBuildTool) testContainer(ctx context.Context, containerName string) error {
	// Test 1: Basic container startup test
	result, err := c.executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "echo", "test"}, &exec.Opts{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("container failed basic startup test: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}

	// Test 2: Check if common tools are available (depending on what we need)
	// This is a basic smoke test - more specific tests could be added based on platform
	result, err = c.executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "sh", "-c", "command -v sh"}, &exec.Opts{})
	if err != nil || result.ExitCode != 0 {
		return fmt.Errorf("container missing basic shell: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}

	return nil
}

// ContainerUpdateTool Implementation

// Definition returns the tool's definition in Claude API format.
func (c *ContainerUpdateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_update",
		Description: "Update project configuration with container settings or atomically set pinned target image ID",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory containing .maestro/config.json (defaults to current directory)",
				},
				"container_name": {
					Type:        "string",
					Description: "Name of the container to register in configuration",
				},
				"dockerfile_path": {
					Type:        "string",
					Description: "Path to dockerfile relative to workspace root (defaults to 'Dockerfile')",
				},
				"pinned_image_id": {
					Type:        "string",
					Description: "Docker image ID (sha256:...) to pin as target image",
				},
				"image_tag": {
					Type:        "string",
					Description: "Optional human-readable tag for the image",
				},
				"reason": {
					Type:        "string",
					Description: "Reason for the pinned image change (for audit)",
				},
				"dry_run": {
					Type:        "boolean",
					Description: "Preview changes without applying them (default: false)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerUpdateTool) Name() string {
	return "container_update"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerUpdateTool) PromptDocumentation() string {
	return `- **container_update** - Update container settings or atomically set pinned target image ID
  - Container Config Mode:
    - container_name (required): name of container to register
    - dockerfile_path (optional): path to dockerfile
    - cwd (optional): working directory containing config
  - Pinned Image Mode:
    - pinned_image_id (required): Docker image ID (sha256:...) to pin as target
    - image_tag (optional): human-readable tag for audit
    - reason (optional): reason for the change
    - dry_run (optional): preview changes without applying
  - Validates image exists locally; writes new pin ID; returns "updated" or "noop"
  - Does NOT change active container; use container_switch to activate`
}

// Exec executes the container configuration update operation.
func (c *ContainerUpdateTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Determine mode based on parameters
	pinnedImageID := utils.GetMapFieldOr(args, "pinned_image_id", "")
	containerName := utils.GetMapFieldOr(args, "container_name", "")

	if pinnedImageID != "" {
		// Pinned Image Mode
		return c.updatePinnedImage(ctx, args)
	} else if containerName != "" {
		// Container Config Mode
		return c.updateContainerConfig(args)
	} else {
		return nil, fmt.Errorf("either pinned_image_id or container_name is required")
	}
}

// updatePinnedImage implements Story 5 - atomically set pinned target image ID.
func (c *ContainerUpdateTool) updatePinnedImage(_ context.Context, args map[string]any) (any, error) {
	pinnedImageID := utils.GetMapFieldOr(args, "pinned_image_id", "")
	imageTag := utils.GetMapFieldOr(args, "image_tag", "")
	reason := utils.GetMapFieldOr(args, "reason", "")
	dryRun := utils.GetMapFieldOr(args, "dry_run", false)

	// TODO: Validate image exists locally (requires Docker image inspection)
	// For now, basic validation that it looks like an image ID
	if !strings.HasPrefix(pinnedImageID, "sha256:") {
		return map[string]any{
			"success": false,
			"error":   "pinned_image_id must be a Docker image ID (sha256:...)",
		}, nil
	}

	// Get current pinned image ID
	oldPinnedID := config.GetPinnedImageID()

	// Check for no-op
	if oldPinnedID == pinnedImageID {
		return map[string]any{
			"success":           true,
			"status":            "noop",
			"previous_image_id": oldPinnedID,
			"new_image_id":      pinnedImageID,
			"message":           fmt.Sprintf("Image already pinned to %s", pinnedImageID),
		}, nil
	}

	// Handle dry run
	if dryRun {
		return map[string]any{
			"success":           true,
			"status":            "would_update",
			"previous_image_id": oldPinnedID,
			"new_image_id":      pinnedImageID,
			"reason":            reason,
			"tag":               imageTag,
			"message":           fmt.Sprintf("Would update pinned image: %s → %s", oldPinnedID, pinnedImageID),
		}, nil
	}

	// Atomically update pinned image ID
	if err := config.SetPinnedImageID(pinnedImageID); err != nil {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to set pinned image ID: %v", err),
		}, nil
	}

	// Log the change for audit
	log.Printf("INFO container_update: pinned image updated %s → %s (tag: %s, reason: %s)",
		oldPinnedID, pinnedImageID, imageTag, reason)

	return map[string]any{
		"success":           true,
		"status":            "updated",
		"previous_image_id": oldPinnedID,
		"new_image_id":      pinnedImageID,
		"reason":            reason,
		"tag":               imageTag,
		"message":           fmt.Sprintf("Successfully pinned image: %s → %s", oldPinnedID, pinnedImageID),
	}, nil
}

// updateContainerConfig updates the container name and dockerfile configuration.
func (c *ContainerUpdateTool) updateContainerConfig(args map[string]any) (any, error) {
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	dockerfilePath := utils.GetMapFieldOr(args, "dockerfile_path", DefaultDockerfile)

	log.Printf("DEBUG container_update: containerName=%s, dockerfilePath=%s", containerName, dockerfilePath)

	// Update project configuration with container name and dockerfile path
	if err := c.updateProjectConfig(containerName, dockerfilePath); err != nil {
		return nil, fmt.Errorf("failed to update project config: %w", err)
	}

	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"dockerfile":     dockerfilePath,
		"message":        fmt.Sprintf("Successfully updated configuration to use container '%s' with dockerfile '%s'", containerName, dockerfilePath),
	}, nil
}

// updateProjectConfig updates the project configuration with the new container name and dockerfile path.
func (c *ContainerUpdateTool) updateProjectConfig(containerName, dockerfilePath string) error {
	// Get current config
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Update container configuration
	if cfg.Container == nil {
		cfg.Container = &config.ContainerConfig{}
	}

	updatedContainer := &config.ContainerConfig{
		Name:          containerName,
		Dockerfile:    dockerfilePath,              // Use repo-relative dockerfile path
		WorkspacePath: cfg.Container.WorkspacePath, // Preserve existing workspace path
		// Runtime settings preserved from existing config
		Network:   cfg.Container.Network,
		TmpfsSize: cfg.Container.TmpfsSize,
		CPUs:      cfg.Container.CPUs,
		Memory:    cfg.Container.Memory,
		PIDs:      cfg.Container.PIDs,
	}

	// Use atomic update function (no path parameter needed now)
	if err := config.UpdateContainer(updatedContainer); err != nil {
		return fmt.Errorf("failed to update container config: %w", err)
	}
	return nil
}

// ContainerTestTool Implementation - Unified container testing with optional TTL and command execution

// Definition returns the tool's definition in Claude API format.
func (c *ContainerTestTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_test",
		Description: "Unified container testing tool - boot test, command execution, or long-running containers with TTL",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"container_name": {
					Type:        "string",
					Description: "Name of container to test",
				},
				"command": {
					Type:        "string",
					Description: "Command to execute in container (optional, if not provided does boot test)",
				},
				"ttl_seconds": {
					Type:        "integer",
					Description: "Time-to-live for container in seconds (0=boot test, >0=persistent container, default: 0)",
				},
				"working_dir": {
					Type:        "string",
					Description: "Working directory inside container",
				},
				"env_vars": {
					Type:        "object",
					Description: "Environment variables to set in container",
				},
				"volumes": {
					Type:        "array",
					Description: "Volume mounts in format 'host_path:container_path'",
				},
				"timeout_seconds": {
					Type:        "integer",
					Description: "Maximum seconds to wait for command completion (default: 60, max: 300)",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerTestTool) Name() string {
	return "container_test"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerTestTool) PromptDocumentation() string {
	return `- **container_test** - Run target (or safe) image in throwaway container to validate environment and/or run tests
  - Purpose: Test container functionality without modifying active container
  - Parameters:
    - container_name (required): name of container to test
    - command (optional): command to execute in container (if not provided, does boot test)
    - ttl_seconds (optional): time-to-live for container (0=boot test, >0=persistent container, default: 0)
    - working_dir (optional): working directory inside container (default: /workspace)
    - env_vars (optional): environment variables to set
    - volumes (optional): additional volume mounts (workspace auto-mounted)
    - timeout_seconds (optional): max seconds to wait for command completion (default: 60, max: 300)
  - Mount Policy:
    - CODING: /workspace mounted READ-WRITE; /tmp writable
    - All other states: /workspace mounted READ-ONLY; /tmp writable
    - Polyglot: Don't assume language; tests may cache under /tmp
  - Behavior: May compile/run tests but must not modify sources except in CODING mode
  - Returns: { "status": "pass" | "fail", "details": { "image_id", "role": "target|safe", ... } }
  - Note: This tool NEVER changes the active container`
}

// Exec executes the unified container test tool using host execution strategy.
func (c *ContainerTestTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract container name
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Add host workspace path to args for host execution
	if c.agent != nil {
		hostWorkspacePath := c.agent.GetHostWorkspacePath()
		if hostWorkspacePath != "" {
			args["host_workspace_path"] = hostWorkspacePath
			fmt.Printf("🗂️  ContainerTestTool: Got host workspace path from agent: %s\n", hostWorkspacePath)
		} else {
			fmt.Printf("🗂️  ContainerTestTool: Agent returned empty host workspace path\n")
		}

		// Add mount permissions based on agent state
		currentState := c.agent.GetCurrentState()
		if currentState == proto.State("CODING") {
			args["mount_permissions"] = "rw"
		} else {
			args["mount_permissions"] = "ro"
		}
		fmt.Printf("🗂️  ContainerTestTool: Agent state=%s, mount_permissions=%s\n", currentState, args["mount_permissions"])
	} else {
		// Fallback to workDir and read-only if no agent
		fmt.Printf("🗂️  ContainerTestTool: No agent available, using fallback workDir: %s\n", c.workDir)
		if c.workDir != "" {
			args["host_workspace_path"] = c.workDir
		}
		args["mount_permissions"] = "ro"
	}

	// Use host execution strategy for all container_test operations
	return c.hostRunner.RunContainerTest(ctx, args)
}

// buildDockerArgs builds docker run command arguments based on parameters.
// Automatically mounts workspace with appropriate permissions based on agent state.
func (c *ContainerTestTool) buildDockerArgs(args map[string]any, containerName, command string, detached bool) []string {
	dockerArgs := []string{"docker", "run"}

	if detached {
		dockerArgs = append(dockerArgs, "-d") // Detached mode for persistent containers
	} else {
		dockerArgs = append(dockerArgs, "--rm") // Remove after execution
	}

	// Add tmpfs for writable /tmp (always writable)
	dockerArgs = append(dockerArgs, "--tmpfs", "/tmp:rw,noexec,nosuid,size=100m")

	// Automatically mount workspace based on agent state
	workspaceMount := c.getWorkspaceMount()
	if workspaceMount != "" {
		dockerArgs = append(dockerArgs, "-v", workspaceMount)
	}

	// Set default working directory to /workspace
	defaultWorkingDir := "/workspace"
	workingDir := utils.GetMapFieldOr(args, "working_dir", defaultWorkingDir)
	if workingDir != "" {
		dockerArgs = append(dockerArgs, "-w", workingDir)
	}

	// Add environment variables
	if envVarsRaw, exists := args["env_vars"]; exists {
		if envVars, ok := envVarsRaw.(map[string]any); ok {
			for key, value := range envVars {
				if strValue, ok := value.(string); ok {
					dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", key, strValue))
				}
			}
		}
	}

	// Add additional volume mounts (in addition to automatic workspace mount)
	if volumesRaw, exists := args["volumes"]; exists {
		if volumes, ok := volumesRaw.([]any); ok {
			for _, volume := range volumes {
				if volumeStr, ok := volume.(string); ok {
					dockerArgs = append(dockerArgs, "-v", volumeStr)
				}
			}
		}
	}

	// Add container name
	dockerArgs = append(dockerArgs, containerName)

	// Add command if specified
	if command != "" {
		dockerArgs = append(dockerArgs, "sh", "-c", command)
	}

	return dockerArgs
}

// getWorkspaceMount returns the appropriate workspace mount string based on agent state.
// Implements mount policy: PLANNING=RO, CODING=RW, /tmp always writable.
func (c *ContainerTestTool) getWorkspaceMount() string {
	// Get the host workspace path from the agent
	var hostWorkspacePath string
	if c.agent != nil {
		hostWorkspacePath = c.agent.GetHostWorkspacePath()
	}

	// Fallback to workDir if agent doesn't provide host path
	if hostWorkspacePath == "" {
		hostWorkspacePath = c.workDir
	}

	// Final fallback to current directory
	if hostWorkspacePath == "" {
		hostWorkspacePath = "."
	}

	// Determine mount permissions based on agent state
	permissions := c.getWorkspacePermissions()

	// Return workspace mount: host_path:/workspace:permissions
	return fmt.Sprintf("%s:/workspace:%s", hostWorkspacePath, permissions)
}

// getWorkspacePermissions determines workspace mount permissions based on agent state.
// Returns "rw" only for CODING state, "ro" for all other states (safer default).
func (c *ContainerTestTool) getWorkspacePermissions() string {
	// Try to get current agent state
	// This would need to be enhanced to access the actual agent context
	currentState := c.getCurrentAgentState()

	// Only CODING state gets read-write access, all others are read-only for security
	if currentState == proto.State("CODING") {
		return "rw"
	}

	// All other states (PLANNING, TESTING, SETUP, CODE_REVIEW, etc.) get read-only
	return "ro"
}

// getCurrentAgentState gets the current agent state if available.
func (c *ContainerTestTool) getCurrentAgentState() proto.State {
	if c.agent != nil {
		return c.agent.GetCurrentState()
	}

	// If no agent reference is available, default to empty state (read-only)
	return proto.State("")
}

// Name returns the tool identifier.
func (c *ContainerListTool) Name() string {
	return "container_list"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerListTool) PromptDocumentation() string {
	return `- **container_list** - List all available Docker containers with registry status
  - Parameters:
    - show_all (optional): show all containers including stopped ones (default: false)
  - Lists Docker containers with their names, images, status, and ports
  - Includes registry information when available
  - Use to see what containers are available for use`
}

// Definition returns the container_list tool definition.
func (c *ContainerListTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_list",
		Description: "List all available Docker containers with their registry status",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"show_all": {
					Type:        "boolean",
					Description: "Show all containers including stopped ones (default: false)",
				},
			},
			Required: []string{},
		},
	}
}

// Exec executes the container_list tool.
func (c *ContainerListTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	showAll := false
	if showAllRaw, exists := args["show_all"]; exists {
		if showAllBool, ok := showAllRaw.(bool); ok {
			showAll = showAllBool
		}
	}

	// Get registry information
	registry := exec.GetGlobalRegistry()
	var registryContainers map[string]exec.RegistryContainerInfo
	if registry != nil {
		registryContainers = registry.GetActiveContainers()
	}

	// Build docker ps command
	dockerArgs := []string{"docker", "ps"}
	if showAll {
		dockerArgs = append(dockerArgs, "-a")
	}
	dockerArgs = append(dockerArgs, "--format", "table {{.Names}}\\t{{.Image}}\\t{{.Status}}\\t{{.Ports}}")

	// Execute docker ps
	result, err := c.executor.Run(ctx, dockerArgs, &exec.Opts{Timeout: 30 * time.Second})
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("Failed to list containers: %v", err),
		}, nil
	}

	response := map[string]any{
		"success": true,
		"output":  result.Stdout,
	}

	// Add registry information if available
	if registryContainers != nil {
		response["registry_status"] = registryContainers
		response["registry_count"] = len(registryContainers)
	}

	return response, nil
}

// ContainerSwitchTool Implementation

// Definition returns the tool's definition in Claude API format.
func (c *ContainerSwitchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_switch",
		Description: "Switch coder agent execution environment to a different container, with fallback to bootstrap container on failure",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"container_name": {
					Type:        "string",
					Description: "Name of container to switch to (e.g., 'maestro-hello', 'maestro-bootstrap')",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerSwitchTool) Name() string {
	return "container_switch"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerSwitchTool) PromptDocumentation() string {
	return `- **container_switch** - Switch coder execution environment to different container
  - Parameters:
    - container_name (required): name of container to switch to
  - Switches the current agent execution environment to specified container
  - Falls back to bootstrap container (maestro-bootstrap) if switch fails
  - Use 'maestro-bootstrap' to return to safe bootstrap environment
  - Updates agent context so it knows its new execution environment
  - Essential for DevOps stories that need to test target containers`
}

// Exec executes the container switch operation.
func (c *ContainerSwitchTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract container name
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Test that the target container is available and working
	testResult, err := c.testContainerAvailability(ctx, containerName)
	if err != nil {
		// Container not available - fall back to bootstrap
		return c.fallbackToBootstrap(ctx, containerName, err.Error())
	}

	// Update the agent's container configuration to use the new container
	if err := c.updateAgentContainer(containerName); err != nil {
		return map[string]any{
			"success":             false,
			"requested_container": containerName,
			"current_container":   c.getCurrentContainer(),
			"error":               fmt.Sprintf("Failed to update agent configuration: %v", err),
			"fallback_available":  true,
		}, nil
	}

	return map[string]any{
		"success":            true,
		"previous_container": c.getCurrentContainer(),
		"current_container":  containerName,
		"test_result":        testResult,
		"message":            fmt.Sprintf("Successfully switched to container '%s'", containerName),
	}, nil
}

// testContainerAvailability tests that the target container is available and working.
func (c *ContainerSwitchTool) testContainerAvailability(ctx context.Context, containerName string) (map[string]any, error) {
	// Try to run a simple test command in the target container
	result, err := c.executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "echo", "container_test"}, &exec.Opts{
		Timeout: 30 * time.Second,
	})

	if err != nil {
		return nil, fmt.Errorf("container '%s' failed availability test: %w (stdout: %s, stderr: %s)",
			containerName, err, result.Stdout, result.Stderr)
	}

	if result.ExitCode != 0 {
		return nil, fmt.Errorf("container '%s' failed availability test with exit code %d (stdout: %s, stderr: %s)",
			containerName, result.ExitCode, result.Stdout, result.Stderr)
	}

	return map[string]any{
		"test_passed": true,
		"stdout":      result.Stdout,
		"stderr":      result.Stderr,
	}, nil
}

// fallbackToBootstrap falls back to the bootstrap container when target container fails.
func (c *ContainerSwitchTool) fallbackToBootstrap(ctx context.Context, requestedContainer, errorMsg string) (any, error) {
	bootstrapContainer := "maestro-bootstrap"

	// Test bootstrap container availability
	_, testErr := c.testContainerAvailability(ctx, bootstrapContainer)
	if testErr != nil {
		return map[string]any{
			"success":             false,
			"requested_container": requestedContainer,
			"current_container":   c.getCurrentContainer(),
			"error":               fmt.Sprintf("Target container failed: %s. Bootstrap container also failed: %v", errorMsg, testErr),
			"fallback_available":  false,
		}, nil
	}

	// Update to bootstrap container
	if err := c.updateAgentContainer(bootstrapContainer); err != nil {
		return map[string]any{
			"success":             false,
			"requested_container": requestedContainer,
			"current_container":   c.getCurrentContainer(),
			"error":               fmt.Sprintf("Target container failed: %s. Failed to switch to bootstrap: %v", errorMsg, err),
			"fallback_available":  false,
		}, nil
	}

	return map[string]any{
		"success":             false,
		"requested_container": requestedContainer,
		"current_container":   bootstrapContainer,
		"fallback_used":       true,
		"original_error":      errorMsg,
		"message":             fmt.Sprintf("Container '%s' failed, fell back to '%s'", requestedContainer, bootstrapContainer),
	}, nil
}

// updateAgentContainer updates the agent's container configuration.
func (c *ContainerSwitchTool) updateAgentContainer(containerName string) error {
	// Get current config
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Update container configuration while preserving other settings
	if cfg.Container == nil {
		cfg.Container = &config.ContainerConfig{}
	}

	updatedContainer := &config.ContainerConfig{
		Name:          containerName,
		Dockerfile:    cfg.Container.Dockerfile,    // Preserve dockerfile path
		WorkspacePath: cfg.Container.WorkspacePath, // Preserve workspace path
		// Preserve runtime settings
		Network:   cfg.Container.Network,
		TmpfsSize: cfg.Container.TmpfsSize,
		CPUs:      cfg.Container.CPUs,
		Memory:    cfg.Container.Memory,
		PIDs:      cfg.Container.PIDs,
	}

	// Use atomic update function
	if err := config.UpdateContainer(updatedContainer); err != nil {
		return fmt.Errorf("failed to update container config: %w", err)
	}

	return nil
}

// getCurrentContainer returns the currently configured container name.
func (c *ContainerSwitchTool) getCurrentContainer() string {
	cfg, err := config.GetConfig()
	if err != nil || cfg.Container == nil {
		return "unknown"
	}
	return cfg.Container.Name
}
