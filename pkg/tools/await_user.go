package tools

import (
	"context"
)

// AwaitUserTool allows PM agent to explicitly signal it's waiting for user response.
// This prevents empty response retries when PM has asked a question and is waiting.
type AwaitUserTool struct{}

// NewAwaitUserTool creates a new await_user tool instance.
func NewAwaitUserTool() *AwaitUserTool {
	return &AwaitUserTool{}
}

// Definition returns the tool's definition in Claude API format.
func (a *AwaitUserTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "await_user",
		Description: "Signal that you are waiting for user response. Use after posting a question via chat_post when you need user input before proceeding.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// Name returns the tool identifier.
func (a *AwaitUserTool) Name() string {
	return "await_user"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (a *AwaitUserTool) PromptDocumentation() string {
	return `- **await_user** - Signal that you are waiting for user response
  - No parameters required
  - Use after asking a question via chat_post when you need user input to continue
  - Prevents system from expecting immediate follow-up actions`
}

// Exec executes the await_user operation.
func (a *AwaitUserTool) Exec(_ context.Context, _ map[string]any) (any, error) {
	return map[string]any{
		"success":    true,
		"message":    "Waiting for user response",
		"await_user": true, // Signal to PM driver to pause LLM calls until new messages arrive
	}, nil
}
