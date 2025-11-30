package tools

import (
	"context"
	"fmt"
	"io"

	"orchestrator/pkg/demo"
)

// ComposeLogsTool provides MCP interface for viewing Docker Compose service logs.
type ComposeLogsTool struct {
	workDir string // Agent workspace directory
}

// NewComposeLogsTool creates a new compose logs tool instance.
func NewComposeLogsTool(workDir string) *ComposeLogsTool {
	return &ComposeLogsTool{workDir: workDir}
}

// Definition returns the tool's definition in Claude API format.
func (c *ComposeLogsTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_logs",
		Description: "View logs from Docker Compose services. Useful for debugging service startup issues or application errors.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"service": {
					Type:        "string",
					Description: "Specific service to get logs from (optional - returns all service logs if not specified)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (c *ComposeLogsTool) Name() string {
	return "compose_logs"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ComposeLogsTool) PromptDocumentation() string {
	return `- **compose_logs** - View logs from Docker Compose services
  - Parameters:
    - service (optional): specific service to get logs from (returns all if not specified)
  - Returns the last 100 lines of logs
  - Use to debug service startup issues or application errors`
}

// Exec executes the compose logs operation.
func (c *ComposeLogsTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Check if compose file exists
	if !demo.ComposeFileExists(c.workDir) {
		return &ExecResult{
			Content: fmt.Sprintf("No compose file found at %s. No services to get logs from.", demo.ComposeFilePath(c.workDir)),
		}, nil
	}

	// Extract optional service name
	service := ""
	if svc, ok := args["service"].(string); ok {
		service = svc
	}

	// Create stack
	composePath := demo.ComposeFilePath(c.workDir)
	stack := demo.NewStack("dev", composePath, "")

	// Get logs
	reader, err := stack.Logs(ctx, service)
	if err != nil {
		return nil, fmt.Errorf("compose logs failed: %w", err)
	}

	// Read logs content
	logs, err := io.ReadAll(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read logs: %w", err)
	}

	if len(logs) == 0 {
		serviceDesc := "all services"
		if service != "" {
			serviceDesc = fmt.Sprintf("service '%s'", service)
		}
		return &ExecResult{
			Content: fmt.Sprintf("No logs available for %s. Services may not be running or haven't produced any output yet.", serviceDesc),
		}, nil
	}

	return &ExecResult{
		Content: string(logs),
	}, nil
}
