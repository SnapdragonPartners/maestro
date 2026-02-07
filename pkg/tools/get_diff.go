package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
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
	return `- **get_diff** - Get git diff showing changes on the current branch
  - Parameters:
    - path (string, optional): specific file path to diff
    - base (string, optional): base ref to diff against (default: merge-base with origin/main)
  - By default shows only changes made on this branch (excludes changes made on main by others)
  - Use base="origin/main" for direct comparison against current main
  - Returns head_sha, base_sha, baseline_ref, and diff_mode for traceability`
}

// Definition returns the tool definition for LLM.
func (t *GetDiffTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: ToolGetDiff,
		Description: "Get git diff showing changes on the current branch. By default uses merge-base " +
			"with origin/main to show only changes made on this branch (excludes changes made on main " +
			"by others). Use the 'base' parameter to compare against a different ref.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"path": {
					Type:        "string",
					Description: "Optional: specific file path to diff. If omitted, shows diff for all files.",
				},
				"base": {
					Type: "string",
					Description: "Optional: base ref to diff against. Defaults to merge-base with origin/main " +
						"(shows only what this branch changed, excluding changes made on main by others). " +
						"Use 'origin/main' for direct comparison against current main. " +
						"Use any valid git ref (e.g., 'HEAD~3') for custom comparisons.",
				},
			},
			Required: []string{}, // No required parameters - workspace is pre-configured
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *GetDiffTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract optional path argument
	path, _ := utils.SafeAssert[string](args["path"])

	// Extract optional base argument
	base, _ := utils.SafeAssert[string](args["base"])

	// If a custom base ref is provided, validate it with git rev-parse
	if base != "" {
		if err := t.validateRef(ctx, base); err != nil {
			return t.buildErrorResult(fmt.Sprintf("invalid base ref %q: %s", base, err.Error()))
		}
	}

	// Resolve SHAs for metadata
	headSHA := t.resolveRef(ctx, "HEAD")
	var baseSHA, baselineRef, diffMode string

	if base == "" {
		// Default: merge-base with origin/main
		baseSHA = t.resolveMergeBase(ctx)
		baselineRef = "merge-base(origin/main, HEAD)"
		diffMode = "merge_base"
	} else {
		baseSHA = t.resolveRef(ctx, base)
		baselineRef = base
		diffMode = "direct_ref"
	}

	// Build git diff command using pre-configured workspace root
	diffCmd, err := t.buildDiffCommand(t.workspaceRoot, path, baseSHA)
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
		"success":      true,
		"diff":         result.Stdout,
		"path":         path,
		"truncated":    diffLines >= t.maxDiffLines,
		"lines":        diffLines,
		"head_sha":     headSHA,
		"base_sha":     baseSHA,
		"baseline_ref": baselineRef,
		"diff_mode":    diffMode,
	}

	content, err := json.Marshal(resultMap)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// validateRef validates a git ref using git rev-parse --verify.
// Uses exec-style args (no shell) to prevent command injection from untrusted refs.
func (t *GetDiffTool) validateRef(ctx context.Context, ref string) error {
	cmd := []string{"git", "-C", t.workspaceRoot, "rev-parse", "--verify", ref + "^{commit}"}
	result, err := t.executor.Run(ctx, cmd, nil)
	if err != nil || result.ExitCode != 0 {
		output := ""
		if result.Stdout != "" {
			output = strings.TrimSpace(result.Stdout)
		}
		return fmt.Errorf("ref does not resolve to a valid commit: %s", output)
	}
	return nil
}

// resolveRef resolves a git ref to its SHA. Returns empty string on failure.
// Uses exec-style args (no shell) to prevent command injection from untrusted refs.
func (t *GetDiffTool) resolveRef(ctx context.Context, ref string) string {
	cmd := []string{"git", "-C", t.workspaceRoot, "rev-parse", ref}
	result, err := t.executor.Run(ctx, cmd, nil)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
}

// resolveMergeBase resolves the merge-base of origin/main and HEAD.
// Returns empty string on failure.
func (t *GetDiffTool) resolveMergeBase(ctx context.Context) string {
	cmd := []string{"git", "-C", t.workspaceRoot, "merge-base", "origin/main", "HEAD"}
	result, err := t.executor.Run(ctx, cmd, nil)
	if err != nil || result.ExitCode != 0 {
		return ""
	}
	return strings.TrimSpace(result.Stdout)
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
// If baseSHA is empty, falls back to origin/main (should not happen in normal flow).
func (t *GetDiffTool) buildDiffCommand(workspacePath, path, baseSHA string) (string, error) {
	// Determine the diff range
	diffRange := "origin/main" // fallback
	if baseSHA != "" {
		diffRange = baseSHA + "..HEAD"
	}

	if path == "" {
		return fmt.Sprintf(
			"cd %s && git diff --no-color --no-ext-diff %s 2>&1 | head -n %d",
			workspacePath, diffRange, t.maxDiffLines,
		), nil
	}

	// Clean path to prevent directory traversal
	cleanPath := filepath.Clean(path)
	if strings.HasPrefix(cleanPath, "..") {
		return "", fmt.Errorf("path cannot contain directory traversal (..) attempts")
	}

	return fmt.Sprintf(
		"cd %s && git diff --no-color --no-ext-diff %s -- %s 2>&1 | head -n %d",
		workspacePath, diffRange, cleanPath, t.maxDiffLines,
	), nil
}
