package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/utils"
)

// StoryEditTool allows the architect to annotate or fully rewrite a story
// before it is requeued after a budget review rejection.
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
	return `- **story_edit** - Edit the story before requeue: append notes OR fully rewrite
  - Parameters:
    - implementation_notes (string, REQUIRED - may be empty): notes appended to the story
    - revised_content (string, optional): if provided, REPLACES the entire story content
  - Use revised_content when the original approach is fundamentally flawed
  - Use implementation_notes alone for adding guidance while keeping the original story`
}

// Definition returns the tool definition for LLM.
func (t *StoryEditTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: ToolStoryEdit,
		Description: "Edit the story before requeueing. You have two options: " +
			"(1) Provide implementation_notes to APPEND guidance to the existing story, or " +
			"(2) Provide revised_content to REPLACE the entire story content when the original approach is fundamentally flawed. " +
			"If both are provided, revised_content takes precedence (notes are ignored). " +
			"Pass empty strings for both if you have no useful edits.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"implementation_notes": {
					Type:        "string",
					Description: "Implementation notes appended to the story. Include what approach failed, what the correct approach should be, and any specific technical guidance. May be empty.",
				},
				"revised_content": {
					Type:        "string",
					Description: "Complete replacement for the story content. Use this when the original story's prescribed approach is fundamentally infeasible and needs to be rewritten. Preserve the title, acceptance criteria, and story intent while fixing the technical approach. Leave empty to keep the original story and just append notes.",
				},
			},
			Required: []string{"implementation_notes"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *StoryEditTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract implementation_notes - required parameter but may be empty
	notes, ok := utils.SafeAssert[string](args["implementation_notes"])
	if !ok {
		return nil, fmt.Errorf("implementation_notes parameter is required")
	}

	// Extract optional revised_content
	revisedContent, _ := utils.SafeAssert[string](args["revised_content"])

	var message string
	switch {
	case revisedContent != "":
		message = "Story content replaced with revised version"
	case notes != "":
		message = "Story notes submitted"
	default:
		message = "No edits provided — story will be requeued without changes"
	}

	return &ExecResult{
		Content: message,
		ProcessEffect: &ProcessEffect{
			Signal: SignalStoryEditComplete,
			Data: map[string]any{
				"notes":           notes,
				"revised_content": revisedContent,
			},
		},
	}, nil
}
