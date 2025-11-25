package tools

import (
	"context"
	"fmt"
)

// SubmitReplyTool signals iteration loop termination with a final response.
type SubmitReplyTool struct {
	// No executor needed - this is a control flow tool
}

// NewSubmitReplyTool creates a new submit_reply tool.
func NewSubmitReplyTool() *SubmitReplyTool {
	return &SubmitReplyTool{}
}

// Name returns the tool name.
func (t *SubmitReplyTool) Name() string {
	return ToolSubmitReply
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *SubmitReplyTool) PromptDocumentation() string {
	return `- **submit_reply** - Submit your final response and exit iteration loop
  - Parameters: response (string, REQUIRED - your final response text)
  - Call this when you have completed your analysis`
}

// Definition returns the tool definition for LLM.
func (t *SubmitReplyTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolSubmitReply,
		Description: "Submit your final response and exit the iteration loop. Call this when you have completed your analysis and are ready to move to the next state.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"response": {
					Type:        "string",
					Description: "Your final response text",
				},
			},
			Required: []string{"response"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *SubmitReplyTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract response argument
	response, ok := args["response"].(string)
	if !ok || response == "" {
		return nil, fmt.Errorf("response is required and must be a non-empty string")
	}

	// Return human-readable message for LLM context
	// Return structured data via ProcessEffect.Data for state machine
	return &ExecResult{
		Content: "Reply submitted successfully",
		ProcessEffect: &ProcessEffect{
			Signal: SignalReplySubmitted,
			Data: map[string]any{
				"response": response,
			},
		},
	}, nil
}
