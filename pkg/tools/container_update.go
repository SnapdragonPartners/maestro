package tools

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
)

// ContainerUpdateTool provides MCP interface for updating container configuration.
type ContainerUpdateTool struct {
	executor exec.Executor
}

// NewContainerUpdateTool creates a new container update tool instance.
func NewContainerUpdateTool(executor exec.Executor) *ContainerUpdateTool {
	return &ContainerUpdateTool{executor: executor}
}

// Definition returns the tool's definition in Claude API format.
func (c *ContainerUpdateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_update",
		Description: "Update project configuration with container settings - automatically gets image ID from Docker",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
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
	return `- **container_update** - Update container configuration atomically  
  - Parameters:
    - container_name (required): name of container to register
    - dockerfile_path (optional): path to dockerfile (defaults to 'Dockerfile')
  - Validates container capabilities before updating configuration
  - Automatically gets and pins the current image ID from Docker
  - Updates container name, dockerfile path, AND pinned image ID atomically
  - Does NOT change active container; use container_switch to activate`
}

// Exec executes the container configuration update operation.
func (c *ContainerUpdateTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract required parameters
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract optional parameters
	dockerfilePath := utils.GetMapFieldOr(args, "dockerfile_path", DefaultDockerfile)

	return c.updateContainerConfiguration(ctx, containerName, dockerfilePath)
}

// updateContainerConfiguration updates the complete container configuration atomically.
func (c *ContainerUpdateTool) updateContainerConfiguration(ctx context.Context, containerName, dockerfilePath string) (any, error) {
	log.Printf("DEBUG container_update: containerName=%s, dockerfilePath=%s", containerName, dockerfilePath)

	// Validate container capabilities before updating configuration
	hostExecutor := exec.NewLocalExec()
	validationResult := validateContainerCapabilities(ctx, hostExecutor, containerName)

	if !validationResult.Success {
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"validation":     validationResult,
			"error": fmt.Sprintf("Container validation failed: %s. Cannot update configuration to use container that lacks required tools: %v",
				validationResult.Message, validationResult.MissingTools),
		}, nil
	}

	// Get the current image ID for the container to pin it automatically
	imageID, err := c.getContainerImageID(ctx, containerName)
	if err != nil {
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"error":          fmt.Sprintf("Failed to get image ID for container '%s': %v", containerName, err),
		}, nil
	}

	// Update project configuration with container name and dockerfile path
	if err := c.updateProjectConfig(containerName, dockerfilePath); err != nil {
		return nil, fmt.Errorf("failed to update project config: %w", err)
	}

	// Pin the image ID for consistency
	if err := config.SetPinnedImageID(imageID); err != nil {
		return map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"error":          fmt.Sprintf("Failed to pin image ID %s: %v", imageID, err),
		}, nil
	}

	log.Printf("INFO container_update: Updated container config: %s, dockerfile: %s, pinned image: %s", containerName, dockerfilePath, imageID)

	return map[string]any{
		"success":         true,
		"container_name":  containerName,
		"dockerfile":      dockerfilePath,
		"pinned_image_id": imageID,
		"message":         fmt.Sprintf("Successfully updated container '%s' with dockerfile '%s' and pinned image ID '%s'", containerName, dockerfilePath, imageID),
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

// getContainerImageID gets the image ID for a given container/image name.
func (c *ContainerUpdateTool) getContainerImageID(ctx context.Context, containerName string) (string, error) {
	// Use docker inspect to get the image ID
	result, err := c.executor.Run(ctx, []string{"docker", "inspect", "--format={{.Id}}", containerName}, &exec.Opts{
		Timeout: 30 * time.Second,
	})

	if err != nil {
		return "", fmt.Errorf("failed to inspect container/image '%s': %w (stdout: %s, stderr: %s)",
			containerName, err, result.Stdout, result.Stderr)
	}

	if result.ExitCode != 0 {
		return "", fmt.Errorf("docker inspect failed for '%s' with exit code %d (stdout: %s, stderr: %s)",
			containerName, result.ExitCode, result.Stdout, result.Stderr)
	}

	imageID := strings.TrimSpace(result.Stdout)
	if imageID == "" {
		return "", fmt.Errorf("empty image ID returned for container/image '%s'", containerName)
	}

	log.Printf("DEBUG: Retrieved image ID %s for container %s", imageID, containerName)
	return imageID, nil
}
