package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/exec"
)

// ToolDefinition represents an Anthropic Claude tool definition.
type ToolDefinition struct {
	// Name is the tool's identifier.
	Name string `json:"name"`
	// Description explains what the tool does.
	Description string `json:"description"`
	// InputSchema defines the JSON schema for tool inputs.
	InputSchema InputSchema `json:"input_schema"`
}

// InputSchema defines the expected input format for a tool.
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property defines a single property in the input schema.
//
//nolint:govet // fieldalignment: JSON serialization order requirements
type Property struct {
	Type        string               `json:"type"`
	Description string               `json:"description,omitempty"`
	Enum        []string             `json:"enum,omitempty"`
	Items       *Property            `json:"items,omitempty"`
	Properties  map[string]*Property `json:"properties,omitempty"`
	Required    []string             `json:"required,omitempty"`
	MinItems    *int                 `json:"minItems,omitempty"`
	MaxItems    *int                 `json:"maxItems,omitempty"`
}

// ToolUse represents a Claude tool use request.
//
//nolint:govet // fieldalignment: JSON serialization order requirements
type ToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult represents a result from executing a tool.
//
//nolint:govet // fieldalignment: JSON serialization order requirements
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content"`
}

// Tool represents an MCP tool.
// Renamed from ToolChannel - has nothing to do with channels.
type Tool interface {
	// Definition returns the tool's definition.
	Definition() ToolDefinition
	// Name returns the tool's identifier.
	Name() string
	// Exec executes the tool with the given arguments.
	Exec(ctx context.Context, args map[string]any) (any, error)
	// PromptDocumentation returns markdown documentation for LLM prompts.
	PromptDocumentation() string
}

// ShellTool implements Tool for shell command execution.
//
//nolint:govet // fieldalignment: Logical grouping preferred over memory optimization
type ShellTool struct {
	executor        exec.Executor
	readOnly        bool
	networkDisabled bool
	resourceLimits  *exec.ResourceLimits
}

// NewShellTool creates a new shell tool with the specified executor.
func NewShellTool(executor exec.Executor) *ShellTool {
	return &ShellTool{
		executor:        executor,
		readOnly:        true, // Default to read-only root filesystem for security
		networkDisabled: true, // Default to network disabled for security
		resourceLimits:  nil,  // No resource limits by default
	}
}

// NewShellToolWithConfig creates a new shell tool with the specified executor and configuration.
func NewShellToolWithConfig(executor exec.Executor, readOnly, networkDisabled bool, resourceLimits *exec.ResourceLimits) *ShellTool {
	return &ShellTool{
		executor:        executor,
		readOnly:        readOnly,
		networkDisabled: networkDisabled,
		resourceLimits:  resourceLimits,
	}
}

// Definition returns the tool's definition in Claude API format.
func (s *ShellTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "shell",
		Description: "Execute a shell command and return the output",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cmd": {
					Type:        "string",
					Description: "The shell command to execute",
				},
				"cwd": {
					Type:        "string",
					Description: "Optional working directory for the command",
				},
			},
			Required: []string{"cmd"},
		},
	}
}

// Name returns the tool identifier.
func (s *ShellTool) Name() string {
	return "shell"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *ShellTool) PromptDocumentation() string {
	return `- **shell** - Execute shell commands for exploration and file operations
  - Parameters: cmd (required), cwd (optional working directory)
  - Read-only filesystem with network disabled for security
  - Returns: stdout, stderr, exit_code, duration, and command details
  - Use for: find, grep, cat, ls, tree, exploration commands`
}

// Exec executes a shell command with proper validation.
func (s *ShellTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Validate required cmd argument.
	cmd, hasCmd := args["cmd"]
	if !hasCmd {
		return nil, fmt.Errorf("missing required argument: cmd")
	}

	cmdStr, ok := cmd.(string)
	if !ok {
		return nil, fmt.Errorf("cmd argument must be a string")
	}

	if cmdStr == "" {
		return nil, fmt.Errorf("cmd argument cannot be empty")
	}

	// Extract optional cwd.
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Execute shell command.
	return s.executeShellCommand(ctx, cmdStr, cwd)
}

// executeShellCommand performs actual shell command execution using the executor interface.
func (s *ShellTool) executeShellCommand(ctx context.Context, cmdStr, cwd string) (any, error) {
	// Create ExecOpts with the shell command and security settings.
	opts := exec.Opts{
		WorkDir:         cwd,
		Timeout:         30 * time.Second, // Default timeout
		ReadOnly:        s.readOnly,
		NetworkDisabled: s.networkDisabled,
		ResourceLimits:  s.resourceLimits,
	}

	// Check for direct docker usage and add guidance
	dockerWarning := ""
	if strings.Contains(cmdStr, "docker ") || strings.HasPrefix(strings.TrimSpace(cmdStr), "docker") {
		dockerWarning = "\n\nNOTE: Direct docker CLI usage detected. Consider using the provided container_* tools instead (container_build, container_test, container_switch) as they work properly in our containerized environment and provide better integration."
	}

	// Execute the command using the executor interface.
	result, err := s.executor.Run(ctx, []string{"sh", "-c", cmdStr}, &opts)
	if err != nil {
		// For shell tool, provide clean error message without Docker implementation details
		return nil, fmt.Errorf("shell command failed: %s (exit code: %d)", cmdStr, result.ExitCode)
	}

	// Return result in a format consistent with Claude API.
	// Note: We return success even for non-zero exit codes - the LLM can handle exit codes.
	response := map[string]any{
		"stdout":    result.Stdout + dockerWarning,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
		"cwd":       cwd,
		"command":   cmdStr,
		"duration":  result.Duration.String(),
	}

	return response, nil
}
