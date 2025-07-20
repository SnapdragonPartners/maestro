package tools

import (
	"context"
	"fmt"
	"sync"
	"time"

	"orchestrator/pkg/exec"
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
	Type        string               `json:"type"`
	Description string               `json:"description,omitempty"`
	Enum        []string             `json:"enum,omitempty"`
	Items       *Property            `json:"items,omitempty"`
	Properties  map[string]*Property `json:"properties,omitempty"`
	Required    []string             `json:"required,omitempty"`
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
type ShellTool struct {
	executor        exec.Executor
	readOnly        bool
	networkDisabled bool
	resourceLimits  *exec.ResourceLimits
}

// NewShellTool creates a new shell tool with the specified executor
func NewShellTool(executor exec.Executor) *ShellTool {
	return &ShellTool{
		executor:        executor,
		readOnly:        true, // Default to read-only root filesystem for security
		networkDisabled: true, // Default to network disabled for security
		resourceLimits:  nil,  // No resource limits by default
	}
}

// NewShellToolWithConfig creates a new shell tool with the specified executor and configuration
func NewShellToolWithConfig(executor exec.Executor, readOnly, networkDisabled bool, resourceLimits *exec.ResourceLimits) *ShellTool {
	return &ShellTool{
		executor:        executor,
		readOnly:        readOnly,
		networkDisabled: networkDisabled,
		resourceLimits:  resourceLimits,
	}
}

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

// executeShellCommand performs actual shell command execution using the executor interface
func (s *ShellTool) executeShellCommand(ctx context.Context, cmdStr, cwd string) (any, error) {
	// Create ExecOpts with the shell command and security settings
	opts := exec.ExecOpts{
		WorkDir:         cwd,
		Timeout:         30 * time.Second, // Default timeout
		ReadOnly:        s.readOnly,
		NetworkDisabled: s.networkDisabled,
		ResourceLimits:  s.resourceLimits,
	}

	// Execute the command using the executor interface
	result, err := s.executor.Run(ctx, []string{"sh", "-c", cmdStr}, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to execute command: %w", err)
	}

	// Return result in a format consistent with Claude API
	return map[string]any{
		"stdout":    result.Stdout,
		"stderr":    result.Stderr,
		"exit_code": result.ExitCode,
		"cwd":       cwd,
	}, nil
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

// UpdateShellToolExecutor updates the executor for the registered shell tool
func UpdateShellToolExecutor(executor exec.Executor) error {
	// Clear the registry and re-register with the new executor
	globalRegistry.Clear()

	// Re-register the shell tool with the new executor
	if err := Register(NewShellTool(executor)); err != nil {
		return fmt.Errorf("failed to register shell tool with new executor: %w", err)
	}

	return nil
}

// InitializeShellTool registers the shell tool with the specified executor
// This should be called during application startup after the executor is configured
func InitializeShellTool(executor exec.Executor) error {
	// Clear any existing tools and register the shell tool with the configured executor
	globalRegistry.Clear()

	if err := Register(NewShellTool(executor)); err != nil {
		return fmt.Errorf("failed to register shell tool: %w", err)
	}

	return nil
}

// InitializeShellToolWithConfig registers the shell tool with the specified executor and configuration
func InitializeShellToolWithConfig(executor exec.Executor, readOnly, networkDisabled bool, resourceLimits *exec.ResourceLimits) error {
	// Clear any existing tools and register the shell tool with the configured executor
	globalRegistry.Clear()

	if err := Register(NewShellToolWithConfig(executor, readOnly, networkDisabled, resourceLimits)); err != nil {
		return fmt.Errorf("failed to register shell tool: %w", err)
	}

	return nil
}
