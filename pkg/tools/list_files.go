package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
)

// ListFilesTool allows listing files in coder workspaces.
type ListFilesTool struct {
	executor   execpkg.Executor
	maxResults int
}

// NewListFilesTool creates a new list_files tool.
func NewListFilesTool(executor execpkg.Executor, maxResults int) *ListFilesTool {
	if maxResults <= 0 {
		maxResults = 1000 // Default: 1000 files
	}
	return &ListFilesTool{
		executor:   executor,
		maxResults: maxResults,
	}
}

// Name returns the tool name.
func (t *ListFilesTool) Name() string {
	return ToolListFiles
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *ListFilesTool) PromptDocumentation() string {
	return `- **list_files** - List files in a coder workspace matching a pattern
  - Parameters: coder_id (string, REQUIRED), pattern (string, optional glob pattern)
  - Use to explore what files exist in a workspace`
}

// Definition returns the tool definition for LLM.
func (t *ListFilesTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolListFiles,
		Description: "List files in a coder workspace matching a pattern. Use this to explore what files exist.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"coder_id": {
					Type:        "string",
					Description: "Coder ID (e.g., 'coder-001', 'coder-002')",
				},
				"pattern": {
					Type:        "string",
					Description: "File pattern to match (shell glob, e.g., '*.go', 'src/**/*.js'). Defaults to '*' (all files).",
				},
			},
			Required: []string{"coder_id"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *ListFilesTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract arguments
	coderID, ok := args["coder_id"].(string)
	if !ok || coderID == "" {
		return nil, fmt.Errorf("coder_id is required and must be a string")
	}

	pattern := "*"
	if p, ok := args["pattern"].(string); ok && p != "" {
		pattern = p
	}

	// Validate coder_id format
	if !strings.HasPrefix(coderID, "coder-") {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("invalid coder_id format: %s (expected 'coder-001' format)", coderID),
		}, nil
	}

	// Construct base path in container
	const codersMountPath = "/mnt/coders"
	basePath := filepath.Join(codersMountPath, coderID)

	// Use find with pattern matching, limit results
	// Use -path instead of -name to support **/ patterns
	cmd := []string{"sh", "-c", fmt.Sprintf(
		"cd %s && find . -type f -path './%s' 2>/dev/null | head -n %d",
		basePath, pattern, t.maxResults,
	)}

	result, err := t.executor.Run(ctx, cmd, nil)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to list files in %s: %v", coderID, err),
		}, nil
	}
	if result.ExitCode != 0 {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("failed to list files in %s: %s", coderID, result.Stderr),
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
		"coder_id":  coderID,
		"pattern":   pattern,
		"truncated": truncated,
	}, nil
}
