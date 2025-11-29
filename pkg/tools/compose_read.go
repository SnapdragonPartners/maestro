package tools

import (
	"context"
	"fmt"
	"os"

	"orchestrator/pkg/demo"
)

// ComposeReadTool reads the compose.yml file contents.
type ComposeReadTool struct {
	workspaceDir string
}

// NewComposeReadTool creates a new compose_read tool.
func NewComposeReadTool(workspaceDir string) *ComposeReadTool {
	return &ComposeReadTool{workspaceDir: workspaceDir}
}

// Name returns the tool name.
func (t *ComposeReadTool) Name() string {
	return "compose_read"
}

// Definition returns the tool definition.
func (t *ComposeReadTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_read",
		Description: "Read the current Docker Compose file (compose.yml) contents from the workspace. Returns the full YAML content of the compose file.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// PromptDocumentation returns documentation for prompt injection.
func (t *ComposeReadTool) PromptDocumentation() string {
	return `- **compose_read** - Read the Docker Compose file contents
  - Returns the full YAML content of compose.yml
  - Returns empty string if no compose file exists
  - Use before modifying to understand current configuration`
}

// Exec reads the compose.yml file.
func (t *ComposeReadTool) Exec(_ context.Context, _ map[string]any) (*ExecResult, error) {
	composePath := demo.ComposeFilePath(t.workspaceDir)

	if !demo.ComposeFileExists(t.workspaceDir) {
		return &ExecResult{
			Content: "No compose.yml file found in workspace.",
		}, nil
	}

	content, err := os.ReadFile(composePath)
	if err != nil {
		return &ExecResult{
			Content: fmt.Sprintf("Error reading compose file: %v", err),
		}, nil
	}

	return &ExecResult{
		Content: string(content),
	}, nil
}
