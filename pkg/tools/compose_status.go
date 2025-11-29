package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/demo"
)

// ComposeStatusTool provides MCP interface for checking Docker Compose service status.
type ComposeStatusTool struct {
	workDir string // Agent workspace directory
}

// NewComposeStatusTool creates a new compose status tool instance.
func NewComposeStatusTool(workDir string) *ComposeStatusTool {
	return &ComposeStatusTool{workDir: workDir}
}

// Definition returns the tool's definition in Claude API format.
func (c *ComposeStatusTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_status",
		Description: "Check the status of Docker Compose services. Shows running state and health status for each service.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// Name returns the tool identifier.
func (c *ComposeStatusTool) Name() string {
	return "compose_status"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ComposeStatusTool) PromptDocumentation() string {
	return `- **compose_status** - Check status of Docker Compose services
  - No parameters required
  - Shows running state and health status for each service
  - Use to verify services are running before running tests`
}

// Exec executes the compose status operation.
func (c *ComposeStatusTool) Exec(ctx context.Context, _ map[string]any) (*ExecResult, error) {
	// Check if compose file exists
	if !demo.ComposeFileExists(c.workDir) {
		return &ExecResult{
			Content: fmt.Sprintf("No compose file found at %s. No services defined.", demo.ComposeFilePath(c.workDir)),
		}, nil
	}

	// Create stack
	composePath := demo.ComposeFilePath(c.workDir)
	stack := demo.NewStack("dev", composePath, "")

	// Get service status
	services, err := stack.PS(ctx)
	if err != nil {
		return nil, fmt.Errorf("compose status failed: %w", err)
	}

	if len(services) == 0 {
		return &ExecResult{
			Content: "No services are currently running. Use compose_up to start services.",
		}, nil
	}

	// Build status message
	statusMsg := "Docker Compose Service Status:\n\n"
	for i := range services {
		runningIcon := "❌"
		if services[i].Running {
			runningIcon = "✅"
		}

		healthStatus := ""
		if services[i].Health != "" {
			healthIcon := "⚠️"
			if services[i].Health == "healthy" {
				healthIcon = "💚"
			} else if services[i].Health == "unhealthy" {
				healthIcon = "🔴"
			}
			healthStatus = fmt.Sprintf(" %s %s", healthIcon, services[i].Health)
		}

		portInfo := ""
		if len(services[i].Ports) > 0 {
			portInfo = "\n    Ports:"
			for j := range services[i].Ports {
				if services[i].Ports[j].PublishedPort > 0 {
					portInfo += fmt.Sprintf(" %d->%d/%s", services[i].Ports[j].PublishedPort, services[i].Ports[j].TargetPort, services[i].Ports[j].Protocol)
				}
			}
		}

		statusMsg += fmt.Sprintf("%s **%s**: %s%s%s\n", runningIcon, services[i].Name, services[i].Status, healthStatus, portInfo)
	}

	return &ExecResult{
		Content: statusMsg,
	}, nil
}
