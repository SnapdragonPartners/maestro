package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
)

// ReadFileTool allows reading file contents from workspaces.
type ReadFileTool struct {
	executor      execpkg.Executor
	workspaceRoot string // Base path for file operations (e.g., "/mnt/architect" or "/mnt/coders/coder-001")
	maxSizeBytes  int64
}

// NewReadFileTool creates a new read_file tool.
func NewReadFileTool(executor execpkg.Executor, workspaceRoot string, maxSizeBytes int64) *ReadFileTool {
	if maxSizeBytes <= 0 {
		maxSizeBytes = 1048576 // Default: 1MB
	}
	if workspaceRoot == "" {
		workspaceRoot = "/workspace" // Default workspace path
	}
	return &ReadFileTool{
		executor:      executor,
		workspaceRoot: workspaceRoot,
		maxSizeBytes:  maxSizeBytes,
	}
}

// Name returns the tool name.
func (t *ReadFileTool) Name() string {
	return ToolReadFile
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *ReadFileTool) PromptDocumentation() string {
	return `- **read_file** - Read contents of a file from the workspace
  - Parameters: path (string, REQUIRED)
  - Use to inspect code files and understand the codebase`
}

// Definition returns the tool definition for LLM.
func (t *ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolReadFile,
		Description: "Read contents of a file from the workspace. Use this to inspect code files and understand the codebase.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"path": {
					Type:        "string",
					Description: "Relative path to file within workspace",
				},
			},
			Required: []string{"path"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract path argument
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required and must be a string")
	}

	// Clean path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return map[string]any{
			"success": false,
			"error":   "path cannot contain directory traversal (..) attempts",
		}, nil
	}

	// Construct full path using workspace root
	containerPath := filepath.Join(t.workspaceRoot, cleanPath)

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
		"truncated": truncated,
	}, nil
}
