package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
)

// FileEditTool performs targeted string replacements in files.
type FileEditTool struct {
	executor      execpkg.Executor
	workspaceRoot string
}

// NewFileEditTool creates a new file_edit tool.
func NewFileEditTool(executor execpkg.Executor, workspaceRoot string) *FileEditTool {
	if workspaceRoot == "" {
		workspaceRoot = DefaultWorkspaceDir
	}
	return &FileEditTool{
		executor:      executor,
		workspaceRoot: workspaceRoot,
	}
}

// Name returns the tool name.
func (t *FileEditTool) Name() string {
	return ToolFileEdit
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *FileEditTool) PromptDocumentation() string {
	return `- **file_edit** - Replace a specific string in a file with new content
  - Parameters: path (string, REQUIRED), old_string (string, REQUIRED), new_string (string, REQUIRED)
  - old_string must match exactly one location in the file
  - Use to make targeted edits without rewriting the entire file`
}

// Definition returns the tool definition for LLM.
func (t *FileEditTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolFileEdit,
		Description: "Replace an exact string match in a file with new content. The old_string must appear exactly once in the file. Use this for targeted edits instead of rewriting entire files.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"path": {
					Type:        "string",
					Description: "Relative path to file within workspace",
				},
				"old_string": {
					Type:        "string",
					Description: "The exact string to find in the file. Must match exactly one location.",
				},
				"new_string": {
					Type:        "string",
					Description: "The replacement string. Use empty string to delete the matched text.",
				},
			},
			Required: []string{"path", "old_string", "new_string"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *FileEditTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	path, ok := utils.SafeAssert[string](args["path"])
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required and must be a string")
	}

	oldString, ok := utils.SafeAssert[string](args["old_string"])
	if !ok || oldString == "" {
		return nil, fmt.Errorf("old_string is required and must be a non-empty string")
	}

	// new_string can be empty (deletion), so just check type
	newString, ok := utils.SafeAssert[string](args["new_string"])
	if !ok {
		return nil, fmt.Errorf("new_string is required and must be a string")
	}

	// Clean path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return t.errorResult("path cannot contain directory traversal (..) attempts"), nil
	}

	containerPath := filepath.Join(t.workspaceRoot, cleanPath)

	// Read the file using argv form to avoid shell metacharacter issues
	readCmd := []string{"cat", containerPath}
	result, err := t.executor.Run(ctx, readCmd, &execpkg.Opts{})
	if err != nil {
		return t.errorResult(fmt.Sprintf("file not found or not readable: %s (error: %v)", path, err)), nil //nolint:nilerr // returning structured error to LLM
	}
	if result.ExitCode != 0 {
		return t.errorResult(fmt.Sprintf("file not found or not readable: %s", path)), nil
	}

	content := result.Stdout

	// Count occurrences
	count := strings.Count(content, oldString)
	if count == 0 {
		return t.errorResult("old_string not found in file. Make sure it matches the file content exactly, including whitespace and indentation."), nil
	}
	if count > 1 {
		return t.errorResult(fmt.Sprintf("old_string matches %d locations in the file. It must match exactly once. Include more surrounding context to make it unique.", count)), nil
	}

	// Perform the replacement
	newContent := strings.Replace(content, oldString, newString, 1)

	// Write back using base64 encoding to safely pass arbitrary content through the shell.
	// Path is single-quoted to prevent shell metacharacter interpretation.
	encoded := base64.StdEncoding.EncodeToString([]byte(newContent))
	writeCmd := []string{"sh", "-c", fmt.Sprintf("echo '%s' | base64 -d > '%s'", encoded, strings.ReplaceAll(containerPath, "'", "'\"'\"'"))}
	writeResult, err := t.executor.Run(ctx, writeCmd, &execpkg.Opts{})
	if err != nil || writeResult.ExitCode != 0 {
		errMsg := "failed to write file"
		if err != nil {
			errMsg = fmt.Sprintf("failed to write file: %v", err)
		} else if writeResult.Stderr != "" {
			errMsg = fmt.Sprintf("failed to write file: %s", writeResult.Stderr)
		}
		return t.errorResult(errMsg), nil
	}

	response := map[string]any{
		"success": true,
		"path":    path,
		"message": "Edit applied successfully",
	}
	respJSON, _ := json.Marshal(response)
	return &ExecResult{Content: string(respJSON)}, nil
}

func (t *FileEditTool) errorResult(msg string) *ExecResult {
	response := map[string]any{
		"success": false,
		"error":   msg,
	}
	content, _ := json.Marshal(response)
	return &ExecResult{Content: string(content)}
}
