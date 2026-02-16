package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
)

const (
	defaultReadLines   = 2000 // Default number of lines to read
	maxLineLength      = 2000 // Truncate lines longer than this
	defaultStartOffset = 1    // 1-based line numbering
)

// ReadFileTool allows reading file contents from workspaces.
type ReadFileTool struct {
	executor      execpkg.Executor
	workspaceRoot string // Base path for file operations (e.g., "/mnt/architect" or "/mnt/coders/coder-001")
	maxSizeBytes  int64  // Safety cap on total output bytes
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
  - Parameters:
    - path (string, REQUIRED): relative path to file within workspace
    - offset (integer, optional): line number to start from (1-based, default: 1)
    - limit (integer, optional): number of lines to read (default: 2000)
  - Output uses numbered lines (cat -n format)
  - Lines longer than 2000 characters are truncated
  - For large files, use offset and limit to read specific sections`
}

// Definition returns the tool definition for LLM.
func (t *ReadFileTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolReadFile,
		Description: "Read contents of a file from the workspace. Output uses numbered lines. For large files, use offset and limit to read specific sections.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"path": {
					Type:        "string",
					Description: "Relative path to file within workspace",
				},
				"offset": {
					Type:        "integer",
					Description: "Line number to start reading from (1-based). Defaults to 1.",
				},
				"limit": {
					Type:        "integer",
					Description: "Number of lines to read. Defaults to 2000.",
				},
			},
			Required: []string{"path"},
		},
	}
}

// intArgOrDefault extracts an integer argument from the args map, returning defaultVal if missing or invalid.
// Handles float64 (from JSON unmarshal), int, and int64 value types.
func intArgOrDefault(args map[string]any, key string, defaultVal int) int {
	v, exists := args[key]
	if !exists {
		return defaultVal
	}
	var n int
	switch val := v.(type) {
	case float64:
		n = int(val)
	case int:
		n = val
	case int64:
		n = int(val)
	default:
		return defaultVal
	}
	if n < 1 {
		return defaultVal
	}
	return n
}

// Exec executes the tool with the given arguments.
func (t *ReadFileTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract path argument
	path, ok := args["path"].(string)
	if !ok || path == "" {
		return nil, fmt.Errorf("path is required and must be a string")
	}

	offset := intArgOrDefault(args, "offset", defaultStartOffset)
	limit := intArgOrDefault(args, "limit", defaultReadLines)

	// Clean path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return t.errorResult("path cannot contain directory traversal (..) attempts")
	}

	// Construct full path using workspace root
	containerPath := filepath.Join(t.workspaceRoot, cleanPath)

	// Build awk command that:
	// 1. Selects lines in range [offset, offset+limit-1]
	// 2. Prints with original line numbers (NR) and tab separator (cat -n format)
	// 3. Truncates lines longer than maxLineLength characters
	// 4. Counts total lines to detect truncation
	endLine := offset + limit - 1
	awkScript := fmt.Sprintf(
		`awk 'NR>=%d && NR<=%d { printf "%%6d\t%%s\n", NR, substr($0, 1, %d) } END { printf "\n__TOTAL_LINES__%%d\n", NR }' '%s'`,
		offset, endLine, maxLineLength, strings.ReplaceAll(containerPath, "'", "'\"'\"'"),
	)
	cmd := []string{"sh", "-c", awkScript}

	result, err := t.executor.Run(ctx, cmd, &execpkg.Opts{})
	if err != nil {
		return t.errorResult(fmt.Sprintf("file not found or not readable: %s (error: %v)", path, err))
	}
	if result.ExitCode != 0 {
		errDetail := result.Stderr
		if errDetail == "" {
			errDetail = result.Stdout
		}
		return t.errorResult(fmt.Sprintf("file not found or not readable: %s (exit code: %d, output: %s)", path, result.ExitCode, errDetail))
	}

	// Parse output to separate content from total line count
	output := result.Stdout
	totalLines := 0
	truncated := false

	if idx := strings.LastIndex(output, "\n__TOTAL_LINES__"); idx >= 0 {
		lineCountStr := strings.TrimSpace(output[idx+len("\n__TOTAL_LINES__"):])
		output = output[:idx]
		if _, scanErr := fmt.Sscanf(lineCountStr, "%d", &totalLines); scanErr == nil {
			truncated = totalLines > endLine
		}
	}

	// Safety cap: if output exceeds maxSizeBytes, truncate
	if int64(len(output)) > t.maxSizeBytes {
		output = output[:t.maxSizeBytes]
		truncated = true
	}

	resultMap := map[string]any{
		"success":     true,
		"content":     output,
		"path":        path,
		"truncated":   truncated,
		"offset":      offset,
		"limit":       limit,
		"total_lines": totalLines,
	}

	content, jsonErr := json.Marshal(resultMap)
	if jsonErr != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", jsonErr)
	}

	return &ExecResult{Content: string(content)}, nil
}

// errorResult creates a JSON error response.
func (t *ReadFileTool) errorResult(msg string) (*ExecResult, error) {
	response := map[string]any{
		"success": false,
		"error":   msg,
	}
	content, marshalErr := json.Marshal(response)
	if marshalErr != nil {
		return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
	}
	return &ExecResult{Content: string(content)}, nil
}
