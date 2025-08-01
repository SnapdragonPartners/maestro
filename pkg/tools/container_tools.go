package tools

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"orchestrator/pkg/config"
)

// UpdateContainerTool provides MCP interface for building and updating container configuration.
type UpdateContainerTool struct{}

// NewUpdateContainerTool creates a new update container tool instance.
func NewUpdateContainerTool() *UpdateContainerTool {
	return &UpdateContainerTool{}
}

// Definition returns the tool's definition in Claude API format.
func (u *UpdateContainerTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "update_container",
		Description: "Build container from dockerfile and update project configuration with the new container name",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory containing .maestro/Dockerfile and .maestro/config.json (defaults to current directory)",
				},
				"container_name": {
					Type:        "string",
					Description: "Name to tag the built container (e.g., 'maestro-hello-dev')",
				},
				"dockerfile_path": {
					Type:        "string",
					Description: "Path to dockerfile relative to cwd (defaults to '.maestro/Dockerfile')",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (u *UpdateContainerTool) Name() string {
	return "update_container"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (u *UpdateContainerTool) PromptDocumentation() string {
	return `- **update_container** - Build container from dockerfile and update project configuration
  - Parameters:
    - container_name (required): name to tag the built container
    - cwd (optional): working directory containing dockerfile and config
    - dockerfile_path (optional): path to dockerfile (defaults to '.maestro/Dockerfile')
  - Builds container using Docker, tests it works, then updates project config
  - Use after copying dockerfile to .maestro/Dockerfile and making any necessary edits`
}

// extractWorkingDirectory extracts and validates the working directory from args.
func extractWorkingDirectory(args map[string]any) (string, error) {
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	if cwd == "" {
		var err error
		cwd, err = filepath.Abs(".")
		if err != nil {
			return "", fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	return cwd, nil
}

// Exec executes the container build and update operation.
func (u *UpdateContainerTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd, err := extractWorkingDirectory(args)
	if err != nil {
		return nil, err
	}

	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract dockerfile path
	dockerfilePath := ".maestro/Dockerfile"
	if path, ok := args["dockerfile_path"].(string); ok && path != "" {
		dockerfilePath = path
	}

	// Make dockerfile path absolute
	absDockerfilePath := filepath.Join(cwd, dockerfilePath)
	if _, err := os.Stat(absDockerfilePath); err != nil {
		return nil, fmt.Errorf("dockerfile not found at %s: %w", absDockerfilePath, err)
	}

	// Build the container
	if err := u.buildContainer(ctx, cwd, containerName, dockerfilePath); err != nil {
		return nil, fmt.Errorf("failed to build container: %w", err)
	}

	// Test the container
	if err := u.testContainer(ctx, containerName); err != nil {
		return nil, fmt.Errorf("container build succeeded but failed testing: %w", err)
	}

	// Update project configuration with new container name
	if err := u.updateProjectConfig(cwd, containerName); err != nil {
		return nil, fmt.Errorf("failed to update project config: %w", err)
	}

	return map[string]any{
		"success":        true,
		"container_name": containerName,
		"dockerfile":     dockerfilePath,
		"message":        fmt.Sprintf("Successfully built and configured container '%s'", containerName),
	}, nil
}

// buildContainer builds the Docker container from the specified dockerfile.
func (u *UpdateContainerTool) buildContainer(ctx context.Context, cwd, containerName, dockerfilePath string) error {
	// Build command: docker build -t {containerName} -f {dockerfilePath} .
	cmd := exec.CommandContext(ctx, "docker", "build", "-t", containerName, "-f", dockerfilePath, ".")
	cmd.Dir = cwd

	// Capture output for debugging
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker build failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// testContainer performs basic validation that the container works.
func (u *UpdateContainerTool) testContainer(ctx context.Context, containerName string) error {
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

// updateProjectConfig updates the project configuration with the new container name.
func (u *UpdateContainerTool) updateProjectConfig(cwd, containerName string) error {
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
