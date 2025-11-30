package tools

import (
	"context"
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"orchestrator/pkg/demo"
)

// ComposeRemoveServiceTool removes a service from the compose.yml file.
type ComposeRemoveServiceTool struct {
	workspaceDir string
}

// NewComposeRemoveServiceTool creates a new compose_remove_service tool.
func NewComposeRemoveServiceTool(workspaceDir string) *ComposeRemoveServiceTool {
	return &ComposeRemoveServiceTool{workspaceDir: workspaceDir}
}

// Name returns the tool name.
func (t *ComposeRemoveServiceTool) Name() string {
	return "compose_remove_service"
}

// Definition returns the tool definition.
func (t *ComposeRemoveServiceTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_remove_service",
		Description: "Remove a service from the Docker Compose file.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"name": {
					Type:        "string",
					Description: "The name of the service to remove",
				},
			},
			Required: []string{"name"},
		},
	}
}

// PromptDocumentation returns documentation for prompt injection.
func (t *ComposeRemoveServiceTool) PromptDocumentation() string {
	return `- **compose_remove_service** - Remove a service from Docker Compose file
  - Parameters:
    - name (required): service name to remove
  - Removes the specified service from compose.yml
  - Does nothing if service doesn't exist
  - Does not affect running containers (use compose_down first)`
}

// Exec removes a service from the compose.yml file.
func (t *ComposeRemoveServiceTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	name, ok := args["name"].(string)
	if !ok || name == "" {
		return &ExecResult{
			Content: "Error: service name is required",
		}, nil
	}

	composePath := demo.ComposeFilePath(t.workspaceDir)

	if !demo.ComposeFileExists(t.workspaceDir) {
		return &ExecResult{
			Content: "No compose.yml file found in workspace.",
		}, nil
	}

	// Load compose file
	content, err := os.ReadFile(composePath)
	if err != nil {
		return &ExecResult{
			Content: fmt.Sprintf("Error reading compose file: %v", err),
		}, nil
	}

	compose := make(map[string]any)
	if unmarshalErr := yaml.Unmarshal(content, &compose); unmarshalErr != nil {
		return &ExecResult{
			Content: fmt.Sprintf("Error parsing compose file: %v", unmarshalErr),
		}, nil
	}

	// Get services section
	services, ok := compose["services"].(map[string]any)
	if !ok {
		return &ExecResult{
			Content: "No services defined in compose file.",
		}, nil
	}

	// Check if service exists
	if _, exists := services[name]; !exists {
		return &ExecResult{
			Content: fmt.Sprintf("Service '%s' not found in compose file.", name),
		}, nil
	}

	// Remove the service
	delete(services, name)

	// Write back to file
	newContent, err := yaml.Marshal(compose)
	if err != nil {
		return &ExecResult{
			Content: fmt.Sprintf("Error marshaling compose file: %v", err),
		}, nil
	}

	if err := os.WriteFile(composePath, newContent, 0644); err != nil {
		return &ExecResult{
			Content: fmt.Sprintf("Error writing compose file: %v", err),
		}, nil
	}

	return &ExecResult{
		Content: fmt.Sprintf("Successfully removed service '%s'", name),
	}, nil
}
