package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
)

// ReadFileTool allows reading file contents from coder workspaces.
type ReadFileTool struct {
	executor     execpkg.Executor
	maxSizeBytes int64
}

// NewReadFileTool creates a new read_file tool.
func NewReadFileTool(executor execpkg.Executor, maxSizeBytes int64) *ReadFileTool {
	if maxSizeBytes <= 0 {
		maxSizeBytes = 1048576 // Default: 1MB
	}
	return &ReadFileTool{
		executor:     executor,
		maxSizeBytes: maxSizeBytes,
	}
}

// Name returns the tool name.
func (t *ReadFileTool) Name() string {
	return ToolReadFile
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *ReadFileTool) PromptDocumentation() string {
	return `- **read_file** - Read contents of a file from a coder workspace
  - Parameters: coder_id (string, REQUIRED), path (string, REQUIRED)
  - Use to inspect code that coders have written`
}

// Definition returns the tool definition for LLM.
func (t *ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolReadFile,
		Description: "Read contents of a file from a coder workspace. Use this to inspect code that coders have written.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"coder_id": {
					Type:        "string",
					Description: "Coder ID (e.g., 'coder-001', 'coder-002')",
				},
				"path": {
					Type:        "string",
					Description: "Relative path to file within coder workspace",
				},
			},
			Required: []string{"coder_id", "path"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract arguments
	coderID, ok := args["coder_id"].(string)
	if !ok || coderID == "" {
		return nil, fmt.Errorf("coder_id is required and must be a string")
	}

	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required and must be a string")
	}

	// Validate coder_id format (should be coder-NNN)
	if !strings.HasPrefix(coderID, "coder-") {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("invalid coder_id format: %s (expected 'coder-001' format)", coderID),
		}, nil
	}

	// Clean path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return map[string]any{
			"success": false,
			"error":   "path cannot contain directory traversal (..) attempts",
		}, nil
	}

	// Construct full path in container
	const codersMountPath = "/mnt/coders"
	containerPath := filepath.Join(codersMountPath, coderID, cleanPath)

	// Use head to limit file size
	cmd := []string{"sh", "-c", fmt.Sprintf("head -c %d %s 2>&1", t.maxSizeBytes, containerPath)}

	result, err := t.executor.Run(ctx, cmd, nil)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("file not found or not readable: %s (error: %v)", path, err),
		}, nil
	}
	if result.ExitCode != 0 {
		return map[string]any{
			"success": false,
			"error":   fmt.Sprintf("file not found or not readable: %s (exit code: %d)", path, result.ExitCode),
		}, nil
	}

	// Check if truncated (output length equals max size)
	truncated := len(result.Stdout) >= int(t.maxSizeBytes)

	return map[string]any{
		"success":   true,
		"content":   result.Stdout,
		"path":      path,
		"coder_id":  coderID,
		"truncated": truncated,
	}, nil
}
