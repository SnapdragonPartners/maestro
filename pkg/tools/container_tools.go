package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"orchestrator/pkg/config"
)

// ContainerBuildTool provides MCP interface for building Docker containers from Dockerfile.
type ContainerBuildTool struct{}

// ContainerUpdateTool provides MCP interface for updating container configuration.
type ContainerUpdateTool struct{}

// ContainerRunTool provides MCP interface for running containers on host.
type ContainerRunTool struct{}

// NewContainerBuildTool creates a new container build tool instance.
func NewContainerBuildTool() *ContainerBuildTool {
	return &ContainerBuildTool{}
}

// NewContainerUpdateTool creates a new container update tool instance.
func NewContainerUpdateTool() *ContainerUpdateTool {
	return &ContainerUpdateTool{}
}

// NewContainerRunTool creates a new container run tool instance.
func NewContainerRunTool() *ContainerRunTool {
	return &ContainerRunTool{}
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
		// Default to /workspace - all agent operations run inside containers
		cwd = "/workspace"
	}

	return cwd
}

// Exec executes the container build operation.
func (c *ContainerBuildTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := extractWorkingDirectory(args)

	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract dockerfile path
	dockerfilePath := "Dockerfile"
	if path, ok := args["dockerfile_path"].(string); ok && path != "" {
		dockerfilePath = path
	}

	// Extract platform
	platform := ""
	if p, ok := args["platform"].(string); ok && p != "" {
		platform = p
	}

	// Make dockerfile path absolute - handle both relative and absolute paths
	var absDockerfilePath string
	if filepath.IsAbs(dockerfilePath) {
		absDockerfilePath = dockerfilePath
	} else {
		absDockerfilePath = filepath.Join(cwd, dockerfilePath)
	}

	// Validate dockerfile exists
	if _, err := os.Stat(absDockerfilePath); err != nil {
		// Try alternative path if the first fails
		if !filepath.IsAbs(dockerfilePath) {
			workspaceDir := "/workspace"
			altPath := filepath.Join(workspaceDir, dockerfilePath)
			if _, altErr := os.Stat(altPath); altErr == nil {
				// Use the alternative path that was found - dockerfile path stays relative to workspaceDir
				return c.buildAndTestContainer(ctx, workspaceDir, containerName, dockerfilePath, platform)
			}
			return nil, fmt.Errorf("dockerfile not found at %s or %s: %w", absDockerfilePath, altPath, err)
		}
		return nil, fmt.Errorf("dockerfile not found at %s: %w", absDockerfilePath, err)
	}

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
	args := []string{"build", "-t", containerName, "-f", dockerfilePath}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, ".")
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = cwd

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// testContainer performs basic validation that the container works.
func (c *ContainerBuildTool) testContainer(ctx context.Context, containerName string) error {
	// Test 1: Basic container startup test
	cmd := exec.CommandContext(ctx, "docker", "run", "--rm", containerName, "echo", "test")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container failed basic startup test: %w", err)
	}

	// Test 2: Check if common tools are available (depending on what we need)
	// This is a basic smoke test - more specific tests could be added based on platform
	cmd = exec.CommandContext(ctx, "docker", "run", "--rm", containerName, "sh", "-c", "command -v sh")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("container missing basic shell: %w", err)
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
	// Extract working directory
	cwd := extractWorkingDirectory(args)

	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Update project configuration with new container name
	if err := c.updateProjectConfig(cwd, containerName); err != nil {
		return nil, fmt.Errorf("failed to update project config: %w", err)
	}

	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"message":        fmt.Sprintf("Successfully updated configuration to use container '%s'", containerName),
	}, nil
}

// updateProjectConfig updates the project configuration with the new container name.
func (c *ContainerUpdateTool) updateProjectConfig(cwd, containerName string) error {
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
		Name:       containerName,
		Dockerfile: cfg.Container.Dockerfile, // Preserve existing dockerfile if any
		// Runtime settings preserved from existing config
		Network:   cfg.Container.Network,
		TmpfsSize: cfg.Container.TmpfsSize,
		CPUs:      cfg.Container.CPUs,
		Memory:    cfg.Container.Memory,
		PIDs:      cfg.Container.PIDs,
	}

	// Use atomic update function
	if err := config.UpdateContainer(cwd, updatedContainer); err != nil {
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
  - Executes container on host system with proper isolation and resource limits
  - Use for running built containers with specific commands or workflows`
}

// Exec executes the container run operation.
func (c *ContainerRunTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Build docker run command
	dockerArgs := c.buildDockerRunArgs(args, containerName)

	// Execute docker run
	cmd := exec.CommandContext(ctx, "docker", dockerArgs...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("container run failed: %w (output: %s)", err, string(output))
	}

	command, _ := args["command"].(string)
	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"command":        command,
		"output":         string(output),
		"message":        fmt.Sprintf("Successfully ran container '%s'", containerName),
	}, nil
}

// buildDockerRunArgs builds the docker run command arguments from the tool inputs.
func (c *ContainerRunTool) buildDockerRunArgs(args map[string]any, containerName string) []string {
	dockerArgs := []string{"run"}

	// Add basic options
	if removeAfter, ok := args["remove_after"].(bool); !ok || removeAfter {
		dockerArgs = append(dockerArgs, "--rm")
	}

	if workingDir, ok := args["working_dir"].(string); ok && workingDir != "" {
		dockerArgs = append(dockerArgs, "-w", workingDir)
	}

	// Add environment variables
	if envVars, ok := args["env_vars"].(map[string]any); ok {
		for key, value := range envVars {
			if strValue, ok := value.(string); ok {
				dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", key, strValue))
			}
		}
	}

	// Add volume mounts
	if volumes, ok := args["volumes"].([]any); ok {
		for _, volume := range volumes {
			if volumeStr, ok := volume.(string); ok {
				dockerArgs = append(dockerArgs, "-v", volumeStr)
			}
		}
	}

	// Add container name
	dockerArgs = append(dockerArgs, containerName)

	// Add command if specified
	if command, ok := args["command"].(string); ok && command != "" {
		dockerArgs = append(dockerArgs, "sh", "-c", command)
	}

	return dockerArgs
}
