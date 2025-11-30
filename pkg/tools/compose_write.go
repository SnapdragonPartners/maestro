package tools

import (
	"context"
	"fmt"
	"os"

	"orchestrator/pkg/demo"
)

// ComposeWriteTool writes/updates the compose.yml file.
type ComposeWriteTool struct {
	workspaceDir string
}

// NewComposeWriteTool creates a new compose_write tool.
func NewComposeWriteTool(workspaceDir string) *ComposeWriteTool {
	return &ComposeWriteTool{workspaceDir: workspaceDir}
}

// Name returns the tool name.
func (t *ComposeWriteTool) Name() string {
	return "compose_write"
}

// Definition returns the tool definition.
func (t *ComposeWriteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_write",
		Description: "Write or update the Docker Compose file (compose.yml) in the workspace. Overwrites the entire file with the provided content.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"content": {
					Type:        "string",
					Description: "The complete YAML content to write to compose.yml",
				},
			},
			Required: []string{"content"},
		},
	}
}

// PromptDocumentation returns documentation for prompt injection.
func (t *ComposeWriteTool) PromptDocumentation() string {
	return `- **compose_write** - Write or update the Docker Compose file
  - Parameters:
    - content (required): complete YAML content to write
  - Overwrites the entire compose.yml file
  - Content must be valid YAML
  - Use compose_read first to understand current configuration
  - Use compose_validate to verify syntax after writing`
}

// Exec writes the compose.yml file.
func (t *ComposeWriteTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	content, ok := args["content"].(string)
	if !ok || content == "" {
		return &ExecResult{
			Content: "Error: content parameter is required",
		}, nil
	}

	composePath := demo.ComposeFilePath(t.workspaceDir)

	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		return &ExecResult{
			Content: fmt.Sprintf("Error writing compose file: %v", err),
		}, nil
	}

	return &ExecResult{
		Content: fmt.Sprintf("Successfully wrote compose.yml (%d bytes)", len(content)),
	}, nil
}
