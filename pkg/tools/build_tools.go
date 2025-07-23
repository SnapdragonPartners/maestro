package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"orchestrator/pkg/build"
	"orchestrator/pkg/logx"
)

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

// executeBuildOperation executes a build operation with common error handling.
func executeBuildOperation(ctx context.Context, buildService *build.Service, operation, absPath string, timeout int, errorMsg string) (any, error) {
	req := &build.Request{
		ProjectRoot: absPath,
		Operation:   operation,
		Timeout:     timeout,
		Context:     make(map[string]string),
	}

	response, err := buildService.ExecuteBuild(ctx, req)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, logx.Wrap(err, errorMsg)
	}

	return map[string]any{
		"success":     response.Success,
		"backend":     response.Backend,
		"output":      response.Output,
		"duration_ms": response.Duration.Milliseconds(),
		"error":       response.Error,
	}, nil
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
		Description: "Build the project using the detected backend (go, python, node, etc.)",
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
func (b *BuildTool) Name() string {
	return "build"
}

// Exec executes the build operation.
func (b *BuildTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	cwd, timeout, err := extractExecArgs(args)
	if err != nil {
		return nil, err
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

// Exec executes the test operation.
func (t *TestTool) Exec(ctx context.Context, args map[string]any) (any, error) {
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

// Exec executes the lint operation.
func (l *LintTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	cwd, timeout, err := extractExecArgs(args)
	if err != nil {
		return nil, err
	}

	return executeBuildOperation(ctx, l.buildService, "lint", cwd, timeout, "lint execution failed")
}

// DoneTool provides MCP interface for signaling task completion.
type DoneTool struct{}

// NewDoneTool creates a new done tool instance.
func NewDoneTool() *DoneTool {
	return &DoneTool{}
}

// Definition returns the tool's definition in Claude API format.
func (d *DoneTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "done",
		Description: "Signal that the coding task is complete and advance the FSM to TESTING state",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// Name returns the tool identifier.
func (d *DoneTool) Name() string {
	return "done"
}

// Exec executes the done operation.
func (d *DoneTool) Exec(_ context.Context, _ map[string]any) (any, error) {
	return map[string]any{
		"success": true,
		"message": "Task marked as complete, advancing to TESTING state",
	}, nil
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

// Exec executes the backend info operation.
func (b *BackendInfoTool) Exec(_ context.Context, args map[string]any) (any, error) {
	cwd, _, err := extractExecArgs(args)
	if err != nil {
		return nil, err
	}

	// Get backend info.
	info, err := b.buildService.GetBackendInfo(cwd)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, logx.Wrap(err, "failed to get backend info")
	}

	return map[string]any{
		"success":      true,
		"backend":      info.Name,
		"project_root": info.ProjectRoot,
		"operations":   info.Operations,
		"detected_at":  info.DetectedAt.Format(time.RFC3339),
	}, nil
}
