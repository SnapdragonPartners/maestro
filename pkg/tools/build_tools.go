package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"orchestrator/pkg/build"
)

// BuildTool provides MCP interface for build operations
type BuildTool struct {
	buildService *build.BuildService
}

// NewBuildTool creates a new build tool instance
func NewBuildTool(buildService *build.BuildService) *BuildTool {
	return &BuildTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format
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

// Name returns the tool identifier
func (b *BuildTool) Name() string {
	return "build"
}

// Exec executes the build operation
func (b *BuildTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Use current directory if not specified
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Make path absolute
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Extract timeout
	timeout := 300 // Default 5 minutes
	if timeoutVal, hasTimeout := args["timeout"]; hasTimeout {
		if timeoutFloat, ok := timeoutVal.(float64); ok {
			timeout = int(timeoutFloat)
		}
	}

	// Create build request
	req := &build.BuildRequest{
		ProjectRoot: absPath,
		Operation:   "build",
		Timeout:     timeout,
		Context:     make(map[string]string),
	}

	// Execute build
	response, err := b.buildService.ExecuteBuild(ctx, req)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success":     response.Success,
		"backend":     response.Backend,
		"output":      response.Output,
		"duration_ms": response.Duration.Milliseconds(),
		"error":       response.Error,
	}, nil
}

// TestTool provides MCP interface for test operations
type TestTool struct {
	buildService *build.BuildService
}

// NewTestTool creates a new test tool instance
func NewTestTool(buildService *build.BuildService) *TestTool {
	return &TestTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format
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

// Name returns the tool identifier
func (t *TestTool) Name() string {
	return "test"
}

// Exec executes the test operation
func (t *TestTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Use current directory if not specified
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Make path absolute
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Extract timeout
	timeout := 300 // Default 5 minutes
	if timeoutVal, hasTimeout := args["timeout"]; hasTimeout {
		if timeoutFloat, ok := timeoutVal.(float64); ok {
			timeout = int(timeoutFloat)
		}
	}

	// Create test request
	req := &build.BuildRequest{
		ProjectRoot: absPath,
		Operation:   "test",
		Timeout:     timeout,
		Context:     make(map[string]string),
	}

	// Execute test
	response, err := t.buildService.ExecuteBuild(ctx, req)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success":     response.Success,
		"backend":     response.Backend,
		"output":      response.Output,
		"duration_ms": response.Duration.Milliseconds(),
		"error":       response.Error,
	}, nil
}

// LintTool provides MCP interface for linting operations
type LintTool struct {
	buildService *build.BuildService
}

// NewLintTool creates a new lint tool instance
func NewLintTool(buildService *build.BuildService) *LintTool {
	return &LintTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format
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

// Name returns the tool identifier
func (l *LintTool) Name() string {
	return "lint"
}

// Exec executes the lint operation
func (l *LintTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Use current directory if not specified
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Make path absolute
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Extract timeout
	timeout := 300 // Default 5 minutes
	if timeoutVal, hasTimeout := args["timeout"]; hasTimeout {
		if timeoutFloat, ok := timeoutVal.(float64); ok {
			timeout = int(timeoutFloat)
		}
	}

	// Create lint request
	req := &build.BuildRequest{
		ProjectRoot: absPath,
		Operation:   "lint",
		Timeout:     timeout,
		Context:     make(map[string]string),
	}

	// Execute lint
	response, err := l.buildService.ExecuteBuild(ctx, req)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success":     response.Success,
		"backend":     response.Backend,
		"output":      response.Output,
		"duration_ms": response.Duration.Milliseconds(),
		"error":       response.Error,
	}, nil
}

// BackendInfoTool provides MCP interface for backend information
type BackendInfoTool struct {
	buildService *build.BuildService
}

// NewBackendInfoTool creates a new backend info tool instance
func NewBackendInfoTool(buildService *build.BuildService) *BackendInfoTool {
	return &BackendInfoTool{
		buildService: buildService,
	}
}

// Definition returns the tool's definition in Claude API format
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

// Name returns the tool identifier
func (b *BackendInfoTool) Name() string {
	return "backend_info"
}

// Exec executes the backend info operation
func (b *BackendInfoTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract working directory
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Use current directory if not specified
	if cwd == "" {
		var err error
		cwd, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get working directory: %w", err)
		}
	}

	// Make path absolute
	absPath, err := filepath.Abs(cwd)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve absolute path: %w", err)
	}

	// Get backend info
	info, err := b.buildService.GetBackendInfo(absPath)
	if err != nil {
		return map[string]any{
			"success": false,
			"error":   err.Error(),
		}, nil
	}

	return map[string]any{
		"success":      true,
		"backend":      info.Name,
		"project_root": info.ProjectRoot,
		"operations":   info.Operations,
		"detected_at":  info.DetectedAt.Format(time.RFC3339),
	}, nil
}
