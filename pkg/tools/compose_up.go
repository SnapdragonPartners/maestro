package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/demo"
)

// ComposeUpTool provides MCP interface for starting Docker Compose stacks.
type ComposeUpTool struct {
	workDir string // Agent workspace directory
}

// NewComposeUpTool creates a new compose up tool instance.
func NewComposeUpTool(workDir string) *ComposeUpTool {
	return &ComposeUpTool{workDir: workDir}
}

// Definition returns the tool's definition in Claude API format.
func (c *ComposeUpTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_up",
		Description: "Start Docker Compose services defined in .maestro/compose.yml. Idempotent - compose handles diffing and only recreates changed services.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"service": {
					Type:        "string",
					Description: "Specific service to start (optional - starts all services if not specified)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (c *ComposeUpTool) Name() string {
	return "compose_up"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ComposeUpTool) PromptDocumentation() string {
	return `- **compose_up** - Start Docker Compose services from .maestro/compose.yml
  - Parameters:
    - service (optional): specific service to start (starts all if not specified)
  - Idempotent: compose handles diffing and only recreates changed services
  - Use when tests need databases, caches, or other backend services
  - The compose file should be at .maestro/compose.yml in the workspace`
}

// Exec executes the compose up operation.
func (c *ComposeUpTool) Exec(ctx context.Context, _ map[string]any) (*ExecResult, error) {
	// Check if compose file exists
	if !demo.ComposeFileExists(c.workDir) {
		return &ExecResult{
			Content: fmt.Sprintf("No compose file found at %s. Create .maestro/compose.yml to define services.", demo.ComposeFilePath(c.workDir)),
		}, nil
	}

	// Create stack with a generic project name (will be overridden by coder context)
	composePath := demo.ComposeFilePath(c.workDir)
	stack := demo.NewStack("dev", composePath, "")

	// Run docker compose up
	if err := stack.Up(ctx); err != nil {
		return nil, fmt.Errorf("compose up failed: %w", err)
	}

	// Get service status after starting
	services, err := stack.PS(ctx)
	if err != nil {
		// The stack started successfully, we just couldn't get status - this is not a failure
		//nolint:nilerr // Intentionally returning nil error - the compose up operation succeeded
		return &ExecResult{
			Content: "Compose stack started successfully, but failed to get service status: " + err.Error(),
		}, nil
	}

	// Build status message
	statusMsg := "Compose stack started successfully.\n\nService status:\n"
	for i := range services {
		healthStatus := ""
		if services[i].Health != "" {
			healthStatus = fmt.Sprintf(" (health: %s)", services[i].Health)
		}
		statusMsg += fmt.Sprintf("- %s: %s%s\n", services[i].Name, services[i].Status, healthStatus)
	}

	return &ExecResult{
		Content: statusMsg,
	}, nil
}
