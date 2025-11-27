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
	executor     execpkg.Executor
	maxDiffLines int
}

// NewGetDiffTool creates a new get_diff tool.
func NewGetDiffTool(executor execpkg.Executor, maxDiffLines int) *GetDiffTool {
	if maxDiffLines <= 0 {
		maxDiffLines = 10000 // Default: 10000 lines
	}
	return &GetDiffTool{
		executor:     executor,
		maxDiffLines: maxDiffLines,
	}
}

// Name returns the tool name.
func (t *GetDiffTool) Name() string {
	return ToolGetDiff
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *GetDiffTool) PromptDocumentation() string {
	return `- **get_diff** - Get git diff between coder workspace and main branch
  - Parameters: coder_id (string, REQUIRED), path (string, optional specific file)
  - Use to see what changes the coder made`
}

// Definition returns the tool definition for LLM.
func (t *GetDiffTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolGetDiff,
		Description: "Get git diff between coder workspace and main branch. Use this to see what changes the coder made.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"coder_id": {
					Type:        "string",
					Description: "Coder ID (e.g., 'coder-001', 'coder-002')",
				},
				"path": {
					Type:        "string",
					Description: "Optional: specific file path to diff. If omitted, shows diff for all files.",
				},
			},
			Required: []string{"coder_id"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *GetDiffTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract arguments
	coderID, ok := args["coder_id"].(string)
	if !ok || coderID == "" {
		return nil, fmt.Errorf("coder_id is required and must be a string")
	}

	path := ""
	if p, ok := args["path"].(string); ok {
		path = p
	}

	// Validate coder_id format
	if !strings.HasPrefix(coderID, "coder-") {
		response := map[string]any{
			"success": false,
			"error":   fmt.Sprintf("invalid coder_id format: %s (expected 'coder-001' format)", coderID),
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Construct workspace path in container
	const codersMountPath = "/mnt/coders"
	workspacePath := filepath.Join(codersMountPath, coderID)

	// Build git diff command
	var diffCmd string
	if path != "" {
		// Clean path to prevent directory traversal
		cleanPath := filepath.Clean(path)
		if strings.HasPrefix(cleanPath, "..") {
			response := map[string]any{
				"success": false,
				"error":   "path cannot contain directory traversal (..) attempts",
			}
			content, marshalErr := json.Marshal(response)
			if marshalErr != nil {
				return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
			}
			return &ExecResult{Content: string(content)}, nil
		}
		diffCmd = fmt.Sprintf(
			"cd %s && git diff --no-color --no-ext-diff origin/main -- %s 2>&1 | head -n %d",
			workspacePath, cleanPath, t.maxDiffLines,
		)
	} else {
		diffCmd = fmt.Sprintf(
			"cd %s && git diff --no-color --no-ext-diff origin/main 2>&1 | head -n %d",
			workspacePath, t.maxDiffLines,
		)
	}

	cmd := []string{"sh", "-c", diffCmd}

	result, err := t.executor.Run(ctx, cmd, nil)
	// Note: git diff returns 0 even if there are differences
	// Only fail if the command itself failed (e.g., not a git repo)
	if err != nil && result.ExitCode != 0 {
		var response map[string]any
		// Check if error is because workspace doesn't exist or isn't a git repo
		if strings.Contains(result.Stdout, "not a git repository") {
			response = map[string]any{
				"success": false,
				"error":   fmt.Sprintf("workspace %s is not a git repository", coderID),
			}
		} else {
			response = map[string]any{
				"success": false,
				"error":   fmt.Sprintf("git diff failed: %s", result.Stdout),
			}
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Count lines to detect truncation
	diffLines := 0
	if result.Stdout != "" {
		diffLines = len(strings.Split(result.Stdout, "\n"))
	}
	truncated := diffLines >= t.maxDiffLines

	resultMap := map[string]any{
		"success":   true,
		"diff":      result.Stdout,
		"coder_id":  coderID,
		"path":      path,
		"truncated": truncated,
		"lines":     diffLines,
	}

	content, err := json.Marshal(resultMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}
