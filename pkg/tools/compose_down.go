package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/demo"
)

// ComposeDownTool provides MCP interface for stopping Docker Compose stacks.
type ComposeDownTool struct {
	workDir string // Agent workspace directory
}

// NewComposeDownTool creates a new compose down tool instance.
func NewComposeDownTool(workDir string) *ComposeDownTool {
	return &ComposeDownTool{workDir: workDir}
}

// Definition returns the tool's definition in Claude API format.
func (c *ComposeDownTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_down",
		Description: "Stop and remove Docker Compose services and volumes. Use when you need to clean up services or reset state.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// Name returns the tool identifier.
func (c *ComposeDownTool) Name() string {
	return "compose_down"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ComposeDownTool) PromptDocumentation() string {
	return `- **compose_down** - Stop and remove Docker Compose services
  - No parameters required
  - Removes containers and volumes for a clean state
  - Use when you need to reset database state or clean up after testing`
}

// Exec executes the compose down operation.
func (c *ComposeDownTool) Exec(ctx context.Context, _ map[string]any) (*ExecResult, error) {
	// Check if compose file exists
	if !demo.ComposeFileExists(c.workDir) {
		return &ExecResult{
			Content: fmt.Sprintf("No compose file found at %s. Nothing to stop.", demo.ComposeFilePath(c.workDir)),
		}, nil
	}

	// Create stack
	composePath := demo.ComposeFilePath(c.workDir)
	stack := demo.NewStack("dev", composePath, "")

	// Run docker compose down
	if err := stack.Down(ctx); err != nil {
		return nil, fmt.Errorf("compose down failed: %w", err)
	}

	return &ExecResult{
		Content: "Compose stack stopped and cleaned up successfully. All containers and volumes have been removed.",
	}, nil
}
