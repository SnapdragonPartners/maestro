package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
)

// GetDiffTool allows getting git diff from coder workspaces.
type GetDiffTool struct {
	executor      execpkg.Executor
	workspaceRoot string // Base path for git operations (e.g., "/mnt/coders/coder-001")
	maxDiffLines  int
}

// NewGetDiffTool creates a new get_diff tool.
// workspaceRoot is the base path for git operations (e.g., "/mnt/coders/coder-001").
func NewGetDiffTool(executor execpkg.Executor, workspaceRoot string, maxDiffLines int) *GetDiffTool {
	if maxDiffLines <= 0 {
		maxDiffLines = 10000 // Default: 10000 lines
	}
	if workspaceRoot == "" {
		workspaceRoot = "/workspace" // Default workspace path
	}
	return &GetDiffTool{
		executor:      executor,
		workspaceRoot: workspaceRoot,
		maxDiffLines:  maxDiffLines,
	}
}

// Name returns the tool name.
func (t *GetDiffTool) Name() string {
	return ToolGetDiff
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *GetDiffTool) PromptDocumentation() string {
	return `- **get_diff** - Get git diff between workspace and main branch
  - Parameters: path (string, optional specific file)
  - Use to see what changes the coder made`
}

// Definition returns the tool definition for LLM.
func (t *GetDiffTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolGetDiff,
		Description: "Get git diff between the current workspace and main branch. Use this to see what changes the coder or hotfix agent made.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"path": {
					Type:        "string",
					Description: "Optional: specific file path to diff. If omitted, shows diff for all files.",
				},
			},
			Required: []string{}, // No required parameters - workspace is pre-configured
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *GetDiffTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract optional path argument
	path := ""
	if p, ok := args["path"].(string); ok {
		path = p
	}

	// Build git diff command using pre-configured workspace root
	diffCmd, err := t.buildDiffCommand(t.workspaceRoot, path)
	if err != nil {
		return t.buildErrorResult(err.Error())
	}

	cmd := []string{"sh", "-c", diffCmd}

	result, err := t.executor.Run(ctx, cmd, nil)
	// Note: git diff returns 0 even if there are differences
	// Only fail if the command itself failed (e.g., not a git repo)
	if err != nil && result.ExitCode != 0 {
		errMsg := fmt.Sprintf("git diff failed: %s", result.Stdout)
		if strings.Contains(result.Stdout, "not a git repository") {
			errMsg = fmt.Sprintf("workspace %s is not a git repository", t.workspaceRoot)
		}
		return t.buildErrorResult(errMsg)
	}

	// Count lines to detect truncation
	diffLines := 0
	if result.Stdout != "" {
		diffLines = len(strings.Split(result.Stdout, "\n"))
	}

	resultMap := map[string]any{
		"success":   true,
		"diff":      result.Stdout,
		"path":      path,
		"truncated": diffLines >= t.maxDiffLines,
		"lines":     diffLines,
	}

	content, err := json.Marshal(resultMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// buildErrorResult builds an error response as an ExecResult.
func (t *GetDiffTool) buildErrorResult(errMsg string) (*ExecResult, error) {
	response := map[string]any{
		"success": false,
		"error":   errMsg,
	}
	content, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal error response: %w", err)
	}
	return &ExecResult{Content: string(content)}, nil
}

// buildDiffCommand constructs the git diff command with proper path handling.
func (t *GetDiffTool) buildDiffCommand(workspacePath, path string) (string, error) {
	if path == "" {
		return fmt.Sprintf(
			"cd %s && git diff --no-color --no-ext-diff origin/main 2>&1 | head -n %d",
			workspacePath, t.maxDiffLines,
		), nil
	}

	// Clean path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return "", fmt.Errorf("path cannot contain directory traversal (..) attempts")
	}

	return fmt.Sprintf(
		"cd %s && git diff --no-color --no-ext-diff origin/main -- %s 2>&1 | head -n %d",
		workspacePath, cleanPath, t.maxDiffLines,
	), nil
}
