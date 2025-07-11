package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sync"
)

// ToolDefinition represents an Anthropic Claude tool definition
type ToolDefinition struct {
	// Name is the tool's identifier
	Name string `json:"name"`
	// Description explains what the tool does
	Description string `json:"description"`
	// InputSchema defines the JSON schema for tool inputs
	InputSchema InputSchema `json:"input_schema"`
}

// InputSchema defines the expected input format for a tool
type InputSchema struct {
	Type       string              `json:"type"`
	Properties map[string]Property `json:"properties"`
	Required   []string            `json:"required,omitempty"`
}

// Property defines a single property in the input schema
type Property struct {
	Type        string   `json:"type"`
	Description string   `json:"description,omitempty"`
	Enum        []string `json:"enum,omitempty"`
}

// ToolUse represents a Claude tool use request
type ToolUse struct {
	ID    string         `json:"id"`
	Name  string         `json:"name"`
	Input map[string]any `json:"input"`
}

// ToolResult represents a result from executing a tool
type ToolResult struct {
	ToolUseID string `json:"tool_use_id"`
	Content   any    `json:"content"`
}

// ToolChannel defines the interface for MCP tool implementations
type ToolChannel interface {
	// Definition returns the tool's definition
	Definition() ToolDefinition
	// Name returns the tool's identifier
	Name() string
	// Exec executes the tool with the given arguments
	Exec(ctx context.Context, args map[string]any) (any, error)
}

// Registry manages registered MCP tools
type Registry struct {
	mu    sync.RWMutex
	tools map[string]ToolChannel
}

// Global registry instance
var globalRegistry = &Registry{
	tools: make(map[string]ToolChannel),
}

// Register adds a tool to the global registry
func Register(tool ToolChannel) error {
	return globalRegistry.Register(tool)
}

// Get retrieves a tool from the global registry
func Get(name string) (ToolChannel, error) {
	return globalRegistry.Get(name)
}

// GetAll returns all registered tools
func GetAll() map[string]ToolChannel {
	return globalRegistry.GetAll()
}

// Register adds a tool to this registry
func (r *Registry) Register(tool ToolChannel) error {
	if tool == nil {
		return fmt.Errorf("tool cannot be nil")
	}

	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name cannot be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %s already registered", name)
	}

	r.tools[name] = tool
	return nil
}

// Get retrieves a tool from this registry
func (r *Registry) Get(name string) (ToolChannel, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}

	return tool, nil
}

// GetAll returns a copy of all registered tools
func (r *Registry) GetAll() map[string]ToolChannel {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]ToolChannel)
	for name, tool := range r.tools {
		result[name] = tool
	}

	return result
}

// Clear removes all tools from the registry (useful for testing)
func (r *Registry) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.tools = make(map[string]ToolChannel)
}

// ShellTool implements ToolChannel for shell command execution
type ShellTool struct{}

// Definition returns the tool's definition in Claude API format
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

// Name returns the tool identifier
func (s *ShellTool) Name() string {
	return "shell"
}

// Exec executes a shell command with proper validation
func (s *ShellTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Validate required cmd argument
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

	// Extract optional cwd
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	// Execute shell command
	return s.executeShellCommand(ctx, cmdStr, cwd)
}

// executeShellCommand performs actual shell command execution
func (s *ShellTool) executeShellCommand(ctx context.Context, cmdStr, cwd string) (any, error) {
	// Create command with context for cancellation
	cmd := exec.CommandContext(ctx, "sh", "-c", cmdStr)

	// Set working directory if specified
	if cwd != "" {
		// Validate that the directory exists
		if _, err := os.Stat(cwd); os.IsNotExist(err) {
			return nil, fmt.Errorf("working directory does not exist: %s", cwd)
		}
		cmd.Dir = cwd
	}

	// Capture stdout and stderr
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Execute the command
	err := cmd.Run()
	exitCode := 0
	if err != nil {
		// Extract exit code from error
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			// Command failed to start or other error
			return nil, fmt.Errorf("failed to execute command: %w", err)
		}
	}

	// Return result in a format consistent with Claude API
	return map[string]any{
		"stdout":    stdout.String(),
		"stderr":    stderr.String(),
		"exit_code": exitCode,
		"cwd":       cwd,
	}, nil
}

// NewShellTool creates a new shell tool instance
func NewShellTool() *ShellTool {
	return &ShellTool{}
}

// GetToolDefinitions returns all registered tool definitions in Claude API format
func GetToolDefinitions() []ToolDefinition {
	tools := GetAll()
	definitions := make([]ToolDefinition, 0, len(tools))

	for _, tool := range tools {
		definitions = append(definitions, tool.Definition())
	}

	return definitions
}

// init registers the shell tool globally
func init() {
	// Register the shell tool on package initialization
	// This ensures it's available whenever the tools package is imported
	if err := Register(NewShellTool()); err != nil {
		// Since this is in init(), we can't return the error
		// In a real application, you might want to panic or log this
		panic(fmt.Sprintf("Failed to register shell tool: %v", err))
	}
}
