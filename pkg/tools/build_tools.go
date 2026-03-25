package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"orchestrator/pkg/build"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// isExecutorUsable checks if an executor interface is non-nil and not a typed nil pointer.
// In Go, an interface wrapping a typed nil pointer (e.g., (*LongRunningDockerExec)(nil) as Executor)
// is non-nil at the interface level but will panic when methods are called.
func isExecutorUsable(e execpkg.Executor) bool {
	if e == nil {
		return false
	}
	v := reflect.ValueOf(e)
	return !v.IsNil()
}

// extractExecArgs extracts common arguments from tool execution.
func extractExecArgs(args map[string]any) (cwd string, timeout int, err error) {
	// Extract working directory.
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Use current directory if not specified.
	if cwd == "" {
		cwd, err = os.Getwd()
		if err != nil {
			return "", 0, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Make path absolute.
	cwd, err = filepath.Abs(cwd)
	if err != nil {
		return "", 0, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Extract timeout.
	timeout = 300 // Default 5 minutes
	if timeoutVal, hasTimeout := args["timeout"]; hasTimeout {
		if timeoutFloat, ok := timeoutVal.(float64); ok {
			timeout = int(timeoutFloat)
		}
	}

	return cwd, timeout, nil
}

// validateBuildRequirements validates build requirements based on story type.
func validateBuildRequirements(cwd, storyType string) error {
	// Check for Makefile (preferred)
	makefilePath := filepath.Join(cwd, "Makefile")
	if !fileExists(makefilePath) {
		makefilePath = filepath.Join(cwd, "makefile")
	}

	if fileExists(makefilePath) {
		return validateMakefileTargets(makefilePath, storyType)
	}

	// Check for other build systems
	if fileExists(filepath.Join(cwd, "go.mod")) ||
		fileExists(filepath.Join(cwd, "package.json")) ||
		fileExists(filepath.Join(cwd, "pyproject.toml")) ||
		fileExists(filepath.Join(cwd, "Cargo.toml")) {
		// These build systems have built-in targets, so validation is less strict
		if storyType == string(proto.StoryTypeApp) {
			// App stories should have proper project structure
			return nil // Build service will handle validation
		}
		return nil // DevOps stories are flexible
	}

	// No recognized build system
	if storyType == string(proto.StoryTypeApp) {
		return fmt.Errorf("no build system detected - app stories require Makefile, go.mod, package.json, pyproject.toml, or Cargo.toml")
	}
	return fmt.Errorf("no build system detected (devops story - consider adding build files)")
}

// validateMakefileTargets validates that required Makefile targets exist.
func validateMakefileTargets(makefilePath, storyType string) error {
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}

	makefileContent := string(content)
	requiredTargets := []string{"build"}

	if storyType == string(proto.StoryTypeApp) {
		// App stories require standard targets
		requiredTargets = []string{"build", "test", "lint"}
	}

	var missingTargets []string
	for _, target := range requiredTargets {
		targetPattern := target + ":"
		if !strings.Contains(makefileContent, targetPattern) {
			missingTargets = append(missingTargets, target)
		}
	}

	if len(missingTargets) > 0 {
		if storyType == string(proto.StoryTypeApp) {
			return fmt.Errorf("makefile missing required targets for app story: %s", strings.Join(missingTargets, ", "))
		}
		return fmt.Errorf("makefile missing targets: %s (devops story - consider adding these targets)", strings.Join(missingTargets, ", "))
	}

	return nil
}

// executeBuildOperation executes a build operation with common error handling.
func executeBuildOperation(ctx context.Context, buildService *build.Service, operation, absPath string, timeout int, errorMsg string) (*ExecResult, error) {
	req := &build.Request{
		ProjectRoot: absPath,
		Operation:   operation,
		Timeout:     timeout,
		Context:     make(map[string]string),
	}

	response, err := buildService.ExecuteBuild(ctx, req)
	if err != nil {
		result := map[string]any{
			"success": false,
			"error":   err.Error(),
		}
		content, _ := json.Marshal(result)
		return &ExecResult{Content: string(content)}, logx.Wrap(err, errorMsg)
	}

	result := map[string]any{
		"success":     response.Success,
		"backend":     response.Backend,
		"output":      response.Output,
		"duration_ms": response.Duration.Milliseconds(),
		"error":       response.Error,
	}
	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal build result: %w", err)
	}
	return &ExecResult{Content: string(content)}, nil
}

// BuildTool provides MCP interface for build operations.
type BuildTool struct {
	buildService *build.Service
}

// NewBuildTool creates a new build tool instance.
func NewBuildTool(buildService *build.Service) *BuildTool {
	return &BuildTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format.
func (b *BuildTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "build",
		Description: "Build the project using the detected backend with story-type awareness",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory (defaults to current directory)",
				},
				"timeout": {
					Type:        "number",
					Description: "Timeout in seconds (default: 300)",
				},
				"story_type": {
					Type:        "string",
					Description: "Type of story: 'devops' or 'app' - affects validation requirements",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (b *BuildTool) Name() string {
	return "build"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (b *BuildTool) PromptDocumentation() string {
	return `- **build** - Build the project using detected backend with story-type awareness
  - Parameters: cwd (optional), timeout (default 300s), story_type (optional)
  - Auto-detects project type and runs appropriate build commands
  - Story-type aware: stricter validation for app stories, flexible for devops
  - Returns: success status, backend used, output, duration`
}

// Exec executes the build operation.
func (b *BuildTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	cwd, timeout, err := extractExecArgs(args)
	if err != nil {
		return nil, err
	}

	// Extract story type for validation
	storyType := string(proto.StoryTypeApp) // Default to app
	if storyTypeVal, hasStoryType := args["story_type"]; hasStoryType {
		if storyTypeStr, ok := storyTypeVal.(string); ok && proto.IsValidStoryType(storyTypeStr) {
			storyType = storyTypeStr
		}
	}

	// Validate build requirements based on story type
	if err := validateBuildRequirements(cwd, storyType); err != nil {
		result := map[string]any{
			"success":  false,
			"backend":  "none",
			"output":   "",
			"duration": "0s",
			"error":    fmt.Sprintf("build validation failed: %v", err),
		}
		content, _ := json.Marshal(result)
		return &ExecResult{Content: string(content)}, nil
	}

	return executeBuildOperation(ctx, b.buildService, "build", cwd, timeout, "build execution failed")
}

// TestTool provides MCP interface for test operations.
type TestTool struct {
	buildService *build.Service
}

// NewTestTool creates a new test tool instance.
func NewTestTool(buildService *build.Service) *TestTool {
	return &TestTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format.
func (t *TestTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "test",
		Description: "Run tests for the project using the detected backend",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory (defaults to current directory)",
				},
				"timeout": {
					Type:        "number",
					Description: "Timeout in seconds (default: 300)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (t *TestTool) Name() string {
	return "test"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (t *TestTool) PromptDocumentation() string {
	return `- **test** - Run tests for the project using detected backend
  - Parameters: cwd (optional), timeout (default 300s)
  - Executes appropriate test commands based on project type
  - Returns: success status, test output, duration`
}

// Exec executes the test operation.
func (t *TestTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	cwd, timeout, err := extractExecArgs(args)
	if err != nil {
		return nil, err
	}

	return executeBuildOperation(ctx, t.buildService, "test", cwd, timeout, "test execution failed")
}

// LintTool provides MCP interface for linting operations.
type LintTool struct {
	buildService *build.Service
}

// NewLintTool creates a new lint tool instance.
func NewLintTool(buildService *build.Service) *LintTool {
	return &LintTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format.
func (l *LintTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "lint",
		Description: "Run linting checks on the project using the detected backend",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory (defaults to current directory)",
				},
				"timeout": {
					Type:        "number",
					Description: "Timeout in seconds (default: 300)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (l *LintTool) Name() string {
	return "lint"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (l *LintTool) PromptDocumentation() string {
	return `- **lint** - Run linting checks on the project using detected backend
  - Parameters: cwd (optional), timeout (default 300s)
  - Executes appropriate linting commands based on project type
  - Returns: success status, lint output, duration`
}

// Exec executes the lint operation.
func (l *LintTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	cwd, timeout, err := extractExecArgs(args)
	if err != nil {
		return nil, err
	}

	return executeBuildOperation(ctx, l.buildService, "lint", cwd, timeout, "lint execution failed")
}

// DoneTool provides MCP interface for signaling task completion.
// When called, it commits all changes (git add -A + git commit) using the summary
// as the commit message, then advances the FSM to TESTING state.
// If no changes exist on the branch at all (Case A), it instead signals
// STORY_COMPLETE for architect verification.
type DoneTool struct {
	agent        Agent            // Optional agent reference for todo checking
	executor     execpkg.Executor // Optional executor for git commit operations
	workDir      string           // Workspace directory for git operations
	storyID      string           // Story ID for commit message prefix
	targetBranch string           // Target branch for merge-base check (defaults to "main")
}

// NewDoneTool creates a new done tool instance.
func NewDoneTool(agent Agent, executor execpkg.Executor, workDir, storyID, targetBranch string) *DoneTool {
	return &DoneTool{
		agent:        agent,
		executor:     executor,
		workDir:      workDir,
		storyID:      storyID,
		targetBranch: targetBranch,
	}
}

// Definition returns the tool's definition in Claude API format.
func (d *DoneTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: "done",
		Description: "Commit all changes and advance to TESTING state. " +
			"Automatically runs git add -A and git commit using your summary as the commit message. " +
			"Pre-commit hooks (lint, format) will run as part of the commit. " +
			"If no changes were needed to satisfy the story requirements, call done with a summary " +
			"explaining why the requirements are already satisfied. The system will automatically " +
			"detect no changes were made and request completion approval from the architect.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"summary": {
					Type: "string",
					Description: "Description of what was accomplished. This becomes the git commit message, " +
						"so write it as a clear, concise summary of the changes made.",
				},
			},
			Required: []string{"summary"},
		},
	}
}

// Name returns the tool identifier.
func (d *DoneTool) Name() string {
	return "done"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (d *DoneTool) PromptDocumentation() string {
	return `- **done** - Commit all changes and advance to TESTING state
  - Parameter: summary (required)
  - summary: Description of what was accomplished (becomes the git commit message)
  - Automatically runs git add -A and git commit using your summary as the commit message
  - Pre-commit hooks (lint, format) will run as part of the commit
  - Use when all implementation work is finished and you are ready for testing
  - If no changes were needed: call done with a summary explaining why requirements are already satisfied (e.g., "Claude Code already installed in container, verified via claude --version"). The system detects no changes on the branch and requests completion approval from the architect.`
}

// Exec executes the done operation: commits all changes, then signals TESTING state.
func (d *DoneTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract summary
	summary, ok := utils.SafeAssert[string](args["summary"])
	if !ok || summary == "" {
		return nil, fmt.Errorf("summary is required and must be a non-empty string")
	}

	// Check for incomplete todos and include warning in response
	var warnings []string
	if d.agent != nil {
		incompleteCount := d.agent.GetIncompleteTodoCount()
		if incompleteCount > 0 {
			warnings = append(warnings, fmt.Sprintf("WARNING: %d todo(s) still incomplete", incompleteCount))
		}
	}

	// Commit all changes if executor is available
	commitResult := d.commitChanges(ctx, summary)
	if commitResult.err != nil {
		return nil, fmt.Errorf("git commit failed: %w", commitResult.err)
	}

	// Build response content
	content := commitResult.message
	if len(warnings) > 0 {
		content = strings.Join(warnings, "; ") + ". " + content
	}

	// If no changes on branch at all (Case A), signal story complete instead of testing
	if commitResult.storyComplete {
		return &ExecResult{
			Content: content,
			ProcessEffect: &ProcessEffect{
				Signal: SignalStoryComplete,
				Data: map[string]any{
					"evidence":   summary,
					"confidence": "MEDIUM", // Done tool infers completion from empty diff, not explicit user assessment
				},
			},
		}, nil
	}

	// Return human-readable message for LLM context
	// Return structured data via ProcessEffect.Data for state machine
	return &ExecResult{
		Content: content,
		ProcessEffect: &ProcessEffect{
			Signal: SignalTesting,
			Data: map[string]any{
				"summary": summary,
			},
		},
	}, nil
}

// commitResult holds the outcome of a git commit attempt.
type commitResult struct {
	err           error  // Non-nil if commit failed
	message       string // Human-readable description of what happened
	storyComplete bool   // True when no changes exist on branch (Case A: story already complete)
}

// commitChanges runs git add -A + git commit with the summary as commit message.
// Returns a commitResult describing the outcome.
func (d *DoneTool) commitChanges(ctx context.Context, summary string) commitResult {
	if !isExecutorUsable(d.executor) {
		return commitResult{message: "Changes committed (no executor - skipped git operations), advancing to TESTING state"}
	}

	opts := &execpkg.Opts{
		WorkDir: d.workDir,
		Timeout: 30 * time.Second,
	}

	// Stage all changes
	result, err := d.executor.Run(ctx, []string{"git", "add", "-A"}, opts)
	if err != nil || result.ExitCode != 0 {
		errMsg := result.Stderr
		if errMsg == "" {
			errMsg = result.Stdout
		}
		return commitResult{err: fmt.Errorf("git add failed (exit %d): %s", result.ExitCode, errMsg)}
	}

	// Check if there are any changes to commit
	// git diff --cached --exit-code returns exit code 1 if there are staged changes
	result, err = d.executor.Run(ctx, []string{"git", "diff", "--cached", "--exit-code"}, opts)
	if err != nil {
		return commitResult{err: fmt.Errorf("git diff --cached failed: %w", err)}
	}
	if result.ExitCode == 0 {
		// Exit code 0 means no staged changes — determine Case A vs Case B
		if d.branchHasCommits(ctx, opts) {
			// Case B: prior commits exist but nothing new this cycle → normal TESTING flow
			return commitResult{message: "No changes to commit, advancing to TESTING state"}
		}
		// Case A: no changes on branch at all → story already complete
		return commitResult{
			storyComplete: true,
			message:       "No changes on branch — story requirements already satisfied, requesting completion approval",
		}
	}

	// Build commit message with story prefix
	commitMsg := summary
	if d.storyID != "" {
		commitMsg = fmt.Sprintf("Story %s: %s", d.storyID, summary)
	}

	// Commit
	result, err = d.executor.Run(ctx, []string{"git", "commit", "-m", commitMsg}, opts)
	if err != nil || result.ExitCode != 0 {
		errMsg := result.Stderr
		if errMsg == "" {
			errMsg = result.Stdout
		}
		return commitResult{err: fmt.Errorf("git commit failed (exit %d): %s", result.ExitCode, errMsg)}
	}

	return commitResult{message: fmt.Sprintf("Changes committed and advancing to TESTING state. Commit: %s", strings.TrimSpace(result.Stdout))}
}

// branchHasCommits checks whether the current branch has any commits beyond the target branch.
// Returns true if there are prior commits (Case B) or if the check fails (safe fallback).
// Returns false if the branch has no commits at all (Case A: story already complete).
func (d *DoneTool) branchHasCommits(ctx context.Context, opts *execpkg.Opts) bool {
	targetBranch := d.targetBranch
	if targetBranch == "" {
		targetBranch = "main"
	}

	// Find the merge base between origin/<targetBranch> and HEAD
	mergeBaseResult, err := d.executor.Run(ctx, []string{
		"git", "merge-base", "origin/" + targetBranch, "HEAD",
	}, opts)
	if err != nil || mergeBaseResult.ExitCode != 0 {
		// merge-base check failed — safe fallback to Case B (assume prior commits)
		logx.Debugf("branchHasCommits: merge-base failed (err=%v, exit=%d), falling back to Case B", err, mergeBaseResult.ExitCode)
		return true
	}

	mergeBase := strings.TrimSpace(mergeBaseResult.Stdout)
	if mergeBase == "" {
		logx.Debugf("branchHasCommits: empty merge-base, falling back to Case B")
		return true
	}

	// Count commits between merge-base and HEAD
	countResult, err := d.executor.Run(ctx, []string{
		"git", "rev-list", "--count", mergeBase + "..HEAD",
	}, opts)
	if err != nil || countResult.ExitCode != 0 {
		logx.Debugf("branchHasCommits: rev-list failed (err=%v, exit=%d), falling back to Case B", err, countResult.ExitCode)
		return true
	}

	n, parseErr := strconv.Atoi(strings.TrimSpace(countResult.Stdout))
	if parseErr != nil {
		logx.Debugf("branchHasCommits: failed to parse commit count %q, falling back to Case B", countResult.Stdout)
		return true
	}
	return n > 0
}

// BackendInfoTool provides MCP interface for backend information.
type BackendInfoTool struct {
	buildService *build.Service
}

// NewBackendInfoTool creates a new backend info tool instance.
func NewBackendInfoTool(buildService *build.Service) *BackendInfoTool {
	return &BackendInfoTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format.
func (b *BackendInfoTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "backend_info",
		Description: "Get information about the detected build backend for the project",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory (defaults to current directory)",
				},
			},
			Required: []string{},
		},
	}
}

// Name returns the tool identifier.
func (b *BackendInfoTool) Name() string {
	return "backend_info"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (b *BackendInfoTool) PromptDocumentation() string {
	return `- **backend_info** - Get information about detected build backend
  - Parameters: cwd (optional)
  - Returns: backend type, project root, available operations
  - Use to understand project structure and available build commands`
}

// Exec executes the backend info operation.
func (b *BackendInfoTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	cwd, _, err := extractExecArgs(args)
	if err != nil {
		return nil, err
	}

	// Get backend info.
	info, err := b.buildService.GetBackendInfo(cwd)
	if err != nil {
		result := map[string]any{
			"success": false,
			"error":   err.Error(),
		}
		content, _ := json.Marshal(result)
		return &ExecResult{Content: string(content)}, logx.Wrap(err, "failed to get backend info")
	}

	result := map[string]any{
		"success":      true,
		"backend":      info.Name,
		"project_root": info.ProjectRoot,
		"operations":   info.Operations,
		"detected_at":  info.DetectedAt.Format(time.RFC3339),
	}
	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal backend info: %w", err)
	}
	return &ExecResult{Content: string(content)}, nil
}
