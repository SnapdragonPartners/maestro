package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"

	"orchestrator/pkg/demo"
)

// ComposeValidateTool validates the compose.yml file syntax.
type ComposeValidateTool struct {
	workspaceDir string
}

// NewComposeValidateTool creates a new compose_validate tool.
func NewComposeValidateTool(workspaceDir string) *ComposeValidateTool {
	return &ComposeValidateTool{workspaceDir: workspaceDir}
}

// Name returns the tool name.
func (t *ComposeValidateTool) Name() string {
	return "compose_validate"
}

// Definition returns the tool definition.
func (t *ComposeValidateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_validate",
		Description: "Validate the Docker Compose file (compose.yml) syntax using `docker compose config`. Returns validation errors if any.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// PromptDocumentation returns documentation for prompt injection.
func (t *ComposeValidateTool) PromptDocumentation() string {
	return `- **compose_validate** - Validate Docker Compose file syntax
  - Uses 'docker compose config' for validation
  - Returns success if valid, error message if invalid
  - Use after compose_write to verify changes`
}

// Exec validates the compose.yml file.
func (t *ComposeValidateTool) Exec(ctx context.Context, _ map[string]any) (*ExecResult, error) {
	composePath := demo.ComposeFilePath(t.workspaceDir)

	if !demo.ComposeFileExists(t.workspaceDir) {
		return &ExecResult{
			Content: "No compose.yml file found in workspace to validate.",
		}, nil
	}

	// Use docker compose config to validate
	cmd := exec.CommandContext(ctx, "docker", "compose", "-f", composePath, "config", "--quiet")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		// Validation failed - report error in Content, not as Go error
		//nolint:nilerr // Validation failures are returned in Content, not as Go error
		return &ExecResult{
			Content: fmt.Sprintf("Validation failed: %s", stderr.String()),
		}, nil
	}

	return &ExecResult{
		Content: "Compose file is valid.",
	}, nil
}
