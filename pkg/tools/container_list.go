package tools

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/exec"
)

// ContainerListTool lists available containers and their registry status.
type ContainerListTool struct {
	executor exec.Executor
}

// NewContainerListTool creates a new container list tool instance.
func NewContainerListTool(executor exec.Executor) *ContainerListTool {
	return &ContainerListTool{executor: executor}
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
