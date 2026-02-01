package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"orchestrator/pkg/demo"
)

// ComposeUpTool provides MCP interface for starting Docker Compose stacks.
type ComposeUpTool struct {
	workDir string // Agent workspace directory
	agentID string // Agent ID for project name isolation
}

// NewComposeUpTool creates a new compose up tool instance.
// The agentID is used as the Docker Compose project name to isolate stacks per agent.
func NewComposeUpTool(workDir, agentID string) *ComposeUpTool {
	return &ComposeUpTool{
		workDir: workDir,
		agentID: agentID,
	}
}

// Definition returns the tool's definition in Claude API format.
func (c *ComposeUpTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "compose_up",
		Description: "Start Docker Compose services defined in .maestro/compose.yml. Idempotent - compose handles diffing and only recreates changed services. Always starts all services defined in the compose file.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
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
  - No parameters required - starts all services defined in the compose file
  - Idempotent: compose handles diffing and only recreates changed services
  - Use when tests need databases, caches, or other backend services
  - The compose file should be at .maestro/compose.yml in the workspace`
}

// validateWorkspacePath validates that the workspace path is safe to use.
// Returns an error if the path is invalid or potentially dangerous.
func (c *ComposeUpTool) validateWorkspacePath() error {
	// 1. Must be an absolute path
	if !filepath.IsAbs(c.workDir) {
		return fmt.Errorf("workspace path must be absolute, got: %s", c.workDir)
	}

	// 2. Clean the path to resolve any . or .. elements
	cleanedWorkDir := filepath.Clean(c.workDir)

	// 3. Build the expected compose path and clean it
	composePath := filepath.Join(cleanedWorkDir, ".maestro", "compose.yml")
	cleanedComposePath := filepath.Clean(composePath)

	// 4. Verify the cleaned compose path is still within the workspace
	// This catches traversal attempts like workDir="/workspace/../../../etc"
	if !strings.HasPrefix(cleanedComposePath, cleanedWorkDir+string(filepath.Separator)) {
		return fmt.Errorf("compose path escapes workspace boundary: %s", composePath)
	}

	// 5. Verify .maestro is in the path (not bypassed via traversal)
	maestroDir := filepath.Join(cleanedWorkDir, ".maestro")
	if !strings.HasPrefix(cleanedComposePath, maestroDir+string(filepath.Separator)) {
		return fmt.Errorf("compose file must be within .maestro directory")
	}

	return nil
}

// Exec executes the compose up operation.
func (c *ComposeUpTool) Exec(ctx context.Context, _ map[string]any) (*ExecResult, error) {
	// Validate workspace path before any file operations
	if err := c.validateWorkspacePath(); err != nil {
		return nil, fmt.Errorf("invalid workspace path: %w", err)
	}

	// Check if compose file exists
	if !demo.ComposeFileExists(c.workDir) {
		return &ExecResult{
			Content: fmt.Sprintf("No compose file found at %s. Create .maestro/compose.yml to define services.", demo.ComposeFilePath(c.workDir)),
		}, nil
	}

	// Create stack with project name derived from agent ID for isolation
	// This ensures each agent's compose stack is independent
	composePath := demo.ComposeFilePath(c.workDir)
	projectName := "maestro-" + c.agentID
	if c.agentID == "" {
		projectName = "maestro-dev" // Fallback if no agent ID
	}
	stack := demo.NewStack(projectName, composePath, "")

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
