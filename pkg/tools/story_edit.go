package tools

import (
	"context"
	"fmt"
)

// StoryEditTool allows the architect to annotate a story with implementation notes
// before it is requeued after a hard budget review limit.
type StoryEditTool struct {
	// No executor needed - this is a control flow tool
}

// NewStoryEditTool creates a new story_edit tool.
func NewStoryEditTool() *StoryEditTool {
	return &StoryEditTool{}
}

// Name returns the tool name.
func (t *StoryEditTool) Name() string {
	return ToolStoryEdit
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *StoryEditTool) PromptDocumentation() string {
	return `- **story_edit** - Annotate the story with implementation notes for the next coder
  - Parameters: implementation_notes (string, REQUIRED - may be empty if no guidance to add)
  - Call this to provide lessons learned before the story is requeued`
}

// Definition returns the tool definition for LLM.
func (t *StoryEditTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolStoryEdit,
		Description: "Annotate the story with implementation notes for the next coder. The notes will be appended to the story content before requeueing. Pass an empty string if you have no useful guidance to add.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"implementation_notes": {
					Type:        "string",
					Description: "Implementation notes for the next coder. Include what approach failed, what the correct approach should be, and any specific technical guidance. May be empty if no useful guidance to add.",
				},
			},
			Required: []string{"implementation_notes"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *StoryEditTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract implementation_notes - required parameter but may be empty
	notes, ok := args["implementation_notes"].(string)
	if !ok {
		return nil, fmt.Errorf("implementation_notes parameter is required")
	}

	message := "Story notes submitted"
	if notes == "" {
		message = "No implementation notes provided — story will be requeued without annotation"
	}

	return &ExecResult{
		Content: message,
		ProcessEffect: &ProcessEffect{
			Signal: SignalStoryEditComplete,
			Data: map[string]any{
				"notes": notes,
			},
		},
	}, nil
}
