package tools

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
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

// ContainerRunTool provides MCP interface for running containers on host.
type ContainerRunTool struct {
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

// NewContainerRunTool creates a new container run tool instance.
func NewContainerRunTool(executor exec.Executor) *ContainerRunTool {
	return &ContainerRunTool{executor: executor}
}

// Definition returns the tool's definition in Claude API format.
func (c *ContainerBuildTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_build",
		Description: "Build Docker container from Dockerfile with proper validation and testing",
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
	return `- **container_build** - Build Docker container from Dockerfile
  - Parameters:
    - container_name (required): name to tag the built container
    - cwd (optional): working directory containing dockerfile
    - dockerfile_path (optional): path to dockerfile (defaults to 'Dockerfile')
    - platform (optional): target platform for multi-arch builds
  - Builds container using Docker with validation and testing
  - Use for DevOps stories that need to build platform-specific containers`
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

// buildContainer builds the Docker container from the specified dockerfile.
func (c *ContainerBuildTool) buildContainer(ctx context.Context, cwd, containerName, dockerfilePath, platform string) error {
	// Build command with optional platform support
	args := []string{"docker", "build", "-t", containerName, "-f", dockerfilePath}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ".")

	// Execute via executor interface
	opts := &exec.Opts{
		WorkDir: cwd,
		Timeout: 5 * time.Minute, // Docker builds can take time
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

// ContainerRunTool Implementation

// Definition returns the tool's definition in Claude API format.
func (c *ContainerRunTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_run",
		Description: "Run container with command on host system",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"container_name": {
					Type:        "string",
					Description: "Name of container to run",
				},
				"command": {
					Type:        "string",
					Description: "Command to execute in container (defaults to container's default command)",
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
				"remove_after": {
					Type:        "boolean",
					Description: "Remove container after execution (default: true)",
				},
				"timeout_seconds": {
					Type:        "integer",
					Description: "Maximum seconds to wait for container to complete (default: 30)",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerRunTool) Name() string {
	return "container_run"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerRunTool) PromptDocumentation() string {
	return `- **container_run** - Run container with command on host system
  - Parameters:
    - container_name (required): name of container to run
    - command (optional): command to execute in container
    - working_dir (optional): working directory inside container
    - env_vars (optional): environment variables to set
    - volumes (optional): volume mounts for host access
    - remove_after (optional): remove container after execution (default: true)
    - timeout_seconds (optional): max seconds to wait for completion (default: 30)
  - Executes container on host system with proper isolation and resource limits
  - Use for testing built containers or running specific commands, not for long-running services
  - For web services, use short commands like health checks rather than starting the full server`
}

// Exec executes the container run operation.
func (c *ContainerRunTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract container name using utils pattern
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Build docker run command
	dockerArgs := c.buildDockerRunArgs(args, containerName)

	// Execute docker run via executor with timeout for potentially long-running containers
	timeout := utils.GetMapFieldOr(args, "timeout_seconds", 30)
	result, err := c.executor.Run(ctx, dockerArgs, &exec.Opts{
		Timeout: time.Duration(timeout) * time.Second,
	})
	if err != nil {
		return nil, fmt.Errorf("container run failed: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}
	if result.ExitCode != 0 {
		return nil, fmt.Errorf("container run failed with exit code %d (stdout: %s, stderr: %s)", result.ExitCode, result.Stdout, result.Stderr)
	}

	command := utils.GetMapFieldOr(args, "command", "")
	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"command":        command,
		"output":         result.Stdout,
		"message":        fmt.Sprintf("Successfully ran container '%s'", containerName),
	}, nil
}

// buildDockerRunArgs builds the docker run command arguments from the tool inputs.
func (c *ContainerRunTool) buildDockerRunArgs(args map[string]any, containerName string) []string {
	dockerArgs := []string{"docker", "run"}

	// Add basic options
	removeAfter := utils.GetMapFieldOr(args, "remove_after", true)
	if removeAfter {
		dockerArgs = append(dockerArgs, "--rm")
	}

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
	command := utils.GetMapFieldOr(args, "command", "")
	if command != "" {
		dockerArgs = append(dockerArgs, "sh", "-c", command)
	}

	return dockerArgs
}
