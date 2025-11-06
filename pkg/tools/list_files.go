package tools

import (
	"context"
	"fmt"
	"strings"

	execpkg "orchestrator/pkg/exec"
)

// ListFilesTool allows listing files in workspaces.
type ListFilesTool struct {
	executor      execpkg.Executor
	workspaceRoot string // Base path for file operations
	maxResults    int
}

// NewListFilesTool creates a new list_files tool.
func NewListFilesTool(executor execpkg.Executor, workspaceRoot string, maxResults int) *ListFilesTool {
	if maxResults <= 0 {
		maxResults = 1000 // Default: 1000 files
	}
	if workspaceRoot == "" {
		workspaceRoot = "/workspace" // Default workspace path
	}
	return &ListFilesTool{
		executor:      executor,
		workspaceRoot: workspaceRoot,
		maxResults:    maxResults,
	}
}

// Name returns the tool name.
func (t *ListFilesTool) Name() string {
	return ToolListFiles
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *ListFilesTool) PromptDocumentation() string {
	return `- **list_files** - List files in the workspace matching a pattern
  - Parameters: pattern (string, optional glob pattern)
  - Use to explore what files exist in the codebase`
}

// Definition returns the tool definition for LLM.
func (t *ListFilesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolListFiles,
		Description: "List files in the workspace matching a pattern. Use this to explore what files exist in the codebase.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"pattern": {
					Type:        "string",
					Description: "File pattern to match (shell glob, e.g., '*.go', 'src/**/*.js'). Defaults to '*' (all files).",
				},
			},
			Required: []string{},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *ListFilesTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract pattern (optional)
	pattern := "*"
	if p, ok := args["pattern"].(string); ok && p != "" {
		pattern = p
	}

	// Use find with pattern matching, limit results
	// Use -path instead of -name to support **/ patterns
	cmd := []string{"sh", "-c", fmt.Sprintf(
		"cd %s && find . -type f -path './%s' 2>/dev/null | head -n %d",
		t.workspaceRoot, pattern, t.maxResults,
	)}

	result, err := t.executor.Run(ctx, cmd, nil)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to list files: %v", err),
		}, nil
	}
	if result.ExitCode != 0 {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to list files: %s", result.Stderr),
		}, nil
	}

	// Parse output into file list
	output := strings.TrimSpace(result.Stdout)
	var files []string
	if output != "" {
		rawFiles := strings.Split(output, "\n")
		files = make([]string, 0, len(rawFiles))
		for _, f := range rawFiles {
			// Strip ./ prefix from find output
			clean := strings.TrimPrefix(f, "./")
			if clean != "" {
				files = append(files, clean)
			}
		}
	} else {
		files = []string{}
	}

	truncated := len(files) >= t.maxResults

	return map[string]any{
		"success":   true,
		"files":     files,
		"count":     len(files),
		"pattern":   pattern,
		"truncated": truncated,
	}, nil
}
