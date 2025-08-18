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
	"orchestrator/pkg/utils"
)

const (
	// DefaultDockerfile is the standard Dockerfile name.
	DefaultDockerfile = "Dockerfile"
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
	executor exec.Executor
}

// ContainerListTool lists available containers and their registry status.
type ContainerListTool struct {
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
	return &ContainerTestTool{executor: executor}
}

// NewContainerListTool creates a new container list tool instance.
func NewContainerListTool(executor exec.Executor) *ContainerListTool {
	return &ContainerListTool{executor: executor}
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
			cwd = "/workspace"
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
		return nil, fmt.Errorf("failed to build container: %w", err)
	}

	// Test the container
	if err := c.testContainer(ctx, containerName); err != nil {
		return nil, fmt.Errorf("container build succeeded but failed testing: %w", err)
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
		Description: "Update project configuration with new container settings",
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
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerUpdateTool) Name() string {
	return "container_update"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerUpdateTool) PromptDocumentation() string {
	return `- **container_update** - Update project configuration with container settings
  - Parameters:
    - container_name (required): name of container to register
    - cwd (optional): working directory containing config
  - Updates project configuration to use the specified container
  - Use after successfully building a container with container_build`
}

// Exec executes the container configuration update operation.
func (c *ContainerUpdateTool) Exec(_ context.Context, args map[string]any) (any, error) {
	// Extract container name using utils pattern
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Use default dockerfile path - container_build will validate existence during actual build
	dockerfilePath := utils.GetMapFieldOr(args, "dockerfile_path", DefaultDockerfile)

	log.Printf("DEBUG container_update: containerName=%s, dockerfilePath=%s (container_build will validate)", containerName, dockerfilePath)

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
	return `- **container_test** - Unified container testing tool
  - Parameters:
    - container_name (required): name of container to test
    - command (optional): command to execute in container (if not provided, does boot test)
    - ttl_seconds (optional): time-to-live for container (0=boot test, >0=persistent container, default: 0)
    - working_dir (optional): working directory inside container
    - env_vars (optional): environment variables to set
    - volumes (optional): volume mounts for host access
    - timeout_seconds (optional): max seconds to wait for command completion (default: 60, max: 300)
  - Modes:
    - Boot Test (no command, ttl_seconds=0): Tests container boots successfully
    - Command Execution (with command): Executes command and returns output
    - Persistent Container (ttl_seconds>0): Starts long-running container for further interaction
  - Automatically registers containers with registry for lifecycle management`
}

// Exec executes the unified container test tool.
func (c *ContainerTestTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract container name
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract optional command
	command := utils.GetMapFieldOr(args, "command", "")

	// Extract TTL (time-to-live) - 0 means boot test, >0 means persistent
	ttlSeconds := utils.GetMapFieldOr(args, "ttl_seconds", 0)

	// Determine mode based on parameters
	if command != "" {
		// Command execution mode
		return c.executeCommand(ctx, args, containerName, command)
	} else if ttlSeconds > 0 {
		// Persistent container mode
		return c.startPersistentContainer(ctx, args, containerName, ttlSeconds)
	} else {
		// Boot test mode (default)
		return c.performBootTest(ctx, args, containerName)
	}
}

// executeCommand runs a specific command in a container and returns the result.
func (c *ContainerTestTool) executeCommand(ctx context.Context, args map[string]any, containerName, command string) (any, error) {
	// Build docker run command
	dockerArgs := c.buildDockerArgs(args, containerName, command, false)

	// Execute with appropriate timeout
	timeout := utils.GetMapFieldOr(args, "timeout_seconds", 60)
	if timeout > 300 {
		timeout = 300 // Enforce max limit
	}

	result, err := c.executor.Run(ctx, dockerArgs, &exec.Opts{
		Timeout: time.Duration(timeout) * time.Second,
	})
	if err != nil {
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"command":        command,
			"error":          fmt.Sprintf("Command execution failed: %v", err),
			"stdout":         result.Stdout,
			"stderr":         result.Stderr,
		}, nil
	}

	return map[string]any{
		"success":        result.ExitCode == 0,
		"container_name": containerName,
		"command":        command,
		"exit_code":      result.ExitCode,
		"stdout":         result.Stdout,
		"stderr":         result.Stderr,
		"message":        fmt.Sprintf("Command executed in container '%s' with exit code %d", containerName, result.ExitCode),
	}, nil
}

// performBootTest tests that a container boots successfully.
func (c *ContainerTestTool) performBootTest(ctx context.Context, args map[string]any, containerName string) (any, error) {
	// Build docker run command for boot test (no specific command)
	dockerArgs := []string{"docker", "run", "--rm", containerName}

	// Get timeout for boot test
	timeout := utils.GetMapFieldOr(args, "timeout_seconds", 30)
	if timeout > 60 {
		timeout = 60 // Enforce max limit for boot test
	}

	result, err := c.executor.Run(ctx, dockerArgs, &exec.Opts{
		Timeout: time.Duration(timeout) * time.Second,
	})

	// For boot test, timeout is expected (container should be killed after waiting)
	if err != nil {
		errorMsg := err.Error()
		if strings.Contains(errorMsg, "killed") || strings.Contains(errorMsg, "timeout") || strings.Contains(errorMsg, "context deadline exceeded") {
			// Container was killed by timeout - success for boot test
			return map[string]any{
				"success":        true,
				"container_name": containerName,
				"timeout":        timeout,
				"mode":           "boot_test",
				"message":        fmt.Sprintf("Container '%s' booted successfully and ran for %d seconds", containerName, timeout),
			}, nil
		}
	}

	// Container exited early or had other error
	return map[string]any{
		"success":        false,
		"container_name": containerName,
		"exit_code":      result.ExitCode,
		"stdout":         result.Stdout,
		"stderr":         result.Stderr,
		"mode":           "boot_test",
		"message":        fmt.Sprintf("Container '%s' failed boot test - exited early with code %d", containerName, result.ExitCode),
	}, nil
}

// startPersistentContainer starts a container with TTL and registers it with the container registry.
func (c *ContainerTestTool) startPersistentContainer(ctx context.Context, args map[string]any, containerName string, ttlSeconds int) (any, error) {
	// Build docker run command for persistent container (detached mode)
	dockerArgs := c.buildDockerArgs(args, containerName, "", true)

	// Execute docker run in detached mode
	result, err := c.executor.Run(ctx, dockerArgs, &exec.Opts{
		Timeout: 30 * time.Second, // Just for starting the container
	})
	if err != nil {
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"error":          fmt.Sprintf("Failed to start persistent container: %v", err),
			"stdout":         result.Stdout,
			"stderr":         result.Stderr,
		}, nil
	}

	// Extract container ID from output (docker run -d returns container ID)
	containerID := strings.TrimSpace(result.Stdout)

	// Register with container registry
	registry := exec.GetGlobalRegistry()
	if registry != nil {
		// Use the container ID as agent ID for now, container name as container name, and "persistent" as purpose
		registry.Register(containerID, containerName, "persistent")
	}

	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"container_id":   containerID,
		"ttl_seconds":    ttlSeconds,
		"mode":           "persistent",
		"message":        fmt.Sprintf("Persistent container '%s' started with TTL %d seconds", containerName, ttlSeconds),
	}, nil
}

// buildDockerArgs builds docker run command arguments based on parameters.
func (c *ContainerTestTool) buildDockerArgs(args map[string]any, containerName, command string, detached bool) []string {
	dockerArgs := []string{"docker", "run"}

	if detached {
		dockerArgs = append(dockerArgs, "-d") // Detached mode for persistent containers
	} else {
		dockerArgs = append(dockerArgs, "--rm") // Remove after execution
	}

	// Add working directory if specified
	workingDir := utils.GetMapFieldOr(args, "working_dir", "")
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

	// Add volume mounts
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
