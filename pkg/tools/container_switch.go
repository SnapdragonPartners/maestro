package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
)

// ContainerSwitchTool switches the coder agent execution environment to a different container.
type ContainerSwitchTool struct {
	executor exec.Executor
}

// NewContainerSwitchTool creates a new container switch tool instance.
func NewContainerSwitchTool(executor exec.Executor) *ContainerSwitchTool {
	return &ContainerSwitchTool{executor: executor}
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

// testContainerAvailability tests that the target container has all required capabilities.
func (c *ContainerSwitchTool) testContainerAvailability(ctx context.Context, containerName string) (map[string]any, error) {
	// Use centralized validation helper with comprehensive checks
	validationResult := ValidateContainerCapabilities(ctx, c.executor, containerName)

	if !validationResult.Success {
		return nil, fmt.Errorf("container '%s' validation failed: %s. Missing tools: %v. This container cannot be used for Maestro operations until these tools are installed: %v",
			containerName, validationResult.Message, validationResult.MissingTools, validationResult.ErrorDetails)
	}

	return map[string]any{
		"test_passed":      true,
		"validation":       validationResult,
		"git_available":    validationResult.GitAvailable,
		"gh_available":     validationResult.GHAvailable,
		"github_api_valid": validationResult.GitHubAPIValid,
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
