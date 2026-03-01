package tools

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
)

// ContainerSwitchTool switches the coder agent execution environment to a different container.
// Performs a live container switch via the Agent interface (lifecycle + config staging),
// with fallback to bootstrap container on failure.
type ContainerSwitchTool struct {
	executor exec.Executor // For validation + imageID resolution (host docker commands)
	agent    Agent         // For live switch + staging
}

// NewContainerSwitchTool creates a new container switch tool instance.
// Uses local executor for docker commands on the host.
// Agent is required for performing the actual container switch.
func NewContainerSwitchTool(agent Agent) *ContainerSwitchTool {
	return &ContainerSwitchTool{
		executor: exec.NewLocalExec(),
		agent:    agent,
	}
}

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
					Description: "Name of container image to switch to (e.g., 'maestro-myproject-dockerfile:latest', 'maestro-bootstrap:latest')",
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
    - container_name (required): name of container image to switch to
  - Performs a LIVE container switch: starts the new container, stops the old one
  - Automatically stages configuration for post-merge persistence
  - Falls back to bootstrap container (maestro-bootstrap) if switch fails
  - Use 'maestro-bootstrap' to return to safe bootstrap environment
  - Essential for DevOps stories that need to test target containers`
}

// Exec executes the container switch operation.
//
//nolint:cyclop // Container switch has inherent complexity with validation, staging, and fallback paths
func (c *ContainerSwitchTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract container name
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Validate container capabilities before attempting switch
	validationResult := ValidateContainerCapabilities(ctx, c.executor, containerName)
	if !validationResult.Success {
		// Target container failed validation — attempt bootstrap fallback
		return c.fallbackToBootstrap(ctx, containerName,
			fmt.Sprintf("container '%s' validation failed: %s. Missing tools: %v",
				containerName, validationResult.Message, validationResult.MissingTools))
	}

	// Resolve staging data for non-reserved containers
	stagingImageID, stagingDockerfile, stagingHash := "", "", ""
	if !IsReservedContainerName(containerName) {
		// Resolve image ID for staging
		imageID, err := GetContainerImageID(ctx, c.executor, containerName)
		if err != nil {
			log.Printf("WARN container_switch: Failed to resolve image ID for %s: %v (skipping staging)", containerName, err)
		} else {
			stagingImageID = imageID
			stagingDockerfile = config.GetDockerfilePath()

			// Compute Dockerfile hash if agent has host workspace path
			if c.agent != nil {
				if hostWorkspace := c.agent.GetHostWorkspacePath(); hostWorkspace != "" {
					fullDockerfilePath := filepath.Join(hostWorkspace, stagingDockerfile)
					if dockerfileContent, readErr := os.ReadFile(fullDockerfilePath); readErr == nil {
						hashBytes := sha256.Sum256(dockerfileContent)
						stagingHash = hex.EncodeToString(hashBytes[:])
					}
				}
			}
		}
	}

	// Perform the actual switch via agent interface
	if c.agent == nil {
		return nil, fmt.Errorf("no agent reference available for container switch")
	}

	previousContainer := ""
	if c.agent != nil {
		previousContainer = c.agent.GetContainerName()
	}

	newContainerName, err := c.agent.SwitchContainer(ctx, containerName, stagingImageID, stagingDockerfile, stagingHash)
	if err != nil {
		// SwitchContainer includes bootstrap fallback internally, so this is a hard failure
		response := map[string]any{
			"success":             false,
			"requested_container": containerName,
			"previous_container":  previousContainer,
			"error":               fmt.Sprintf("Container switch failed: %v", err),
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	liveSwitched := newContainerName != previousContainer
	message := fmt.Sprintf("Successfully switched to container '%s'", newContainerName)
	if !liveSwitched {
		message = fmt.Sprintf("Staged container config for '%s' (lifecycle deferred to signal handler)", containerName)
	}

	result := map[string]any{
		"success":            true,
		"previous_container": previousContainer,
		"current_container":  newContainerName,
		"requested_image":    containerName,
		"staged":             stagingImageID != "",
		"live_switched":      liveSwitched,
		"message":            message,
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// fallbackToBootstrap falls back to the bootstrap container when target container fails validation.
func (c *ContainerSwitchTool) fallbackToBootstrap(ctx context.Context, requestedContainer, errorMsg string) (*ExecResult, error) {
	bootstrapContainer := config.BootstrapContainerTag

	// Test bootstrap container availability
	bootstrapValidation := ValidateContainerCapabilities(ctx, c.executor, bootstrapContainer)
	if !bootstrapValidation.Success {
		response := map[string]any{
			"success":             false,
			"requested_container": requestedContainer,
			"error":               fmt.Sprintf("Target container failed: %s. Bootstrap container also failed: %s", errorMsg, bootstrapValidation.Message),
			"fallback_available":  false,
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Attempt bootstrap switch via agent (no staging for reserved containers)
	if c.agent == nil {
		response := map[string]any{
			"success":             false,
			"requested_container": requestedContainer,
			"error":               fmt.Sprintf("Target container failed: %s. No agent reference for bootstrap fallback.", errorMsg),
			"fallback_available":  false,
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	newContainerName, switchErr := c.agent.SwitchContainer(ctx, bootstrapContainer, "", "", "")
	if switchErr != nil {
		response := map[string]any{
			"success":             false,
			"requested_container": requestedContainer,
			"error":               fmt.Sprintf("Target container failed: %s. Bootstrap switch also failed: %v", errorMsg, switchErr),
			"fallback_available":  false,
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	result := map[string]any{
		"success":             false,
		"requested_container": requestedContainer,
		"current_container":   newContainerName,
		"fallback_used":       true,
		"original_error":      errorMsg,
		"message":             fmt.Sprintf("Container '%s' failed, fell back to '%s'", requestedContainer, newContainerName),
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}
