package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/utils"
)

// MaestroMdSubmitTool submits generated MAESTRO.md content.
type MaestroMdSubmitTool struct{}

// NewMaestroMdSubmitTool creates a new maestro_md_submit tool.
func NewMaestroMdSubmitTool() *MaestroMdSubmitTool {
	return &MaestroMdSubmitTool{}
}

// Name returns the tool name.
func (t *MaestroMdSubmitTool) Name() string {
	return ToolMaestroMdSubmit
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *MaestroMdSubmitTool) PromptDocumentation() string {
	return `- **maestro_md_submit** - Submit the generated MAESTRO.md content
  - Parameters: content (string, REQUIRED - the MAESTRO.md content following the schema)
  - Call this when you have generated the project overview content`
}

// Definition returns the tool definition for LLM.
func (t *MaestroMdSubmitTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolMaestroMdSubmit,
		Description: "Submit the generated MAESTRO.md content. The content should follow the required schema with sections for Purpose, Architecture, Technologies, and Constraints.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"content": {
					Type:        "string",
					Description: "The MAESTRO.md content following the required schema",
				},
			},
			Required: []string{"content"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *MaestroMdSubmitTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract content argument
	content, ok := args["content"].(string)
	if !ok || content == "" {
		return nil, fmt.Errorf("content is required and must be a non-empty string")
	}

	// Validate content length
	if len(content) > utils.MaestroMdCharLimit {
		return nil, fmt.Errorf("content exceeds maximum length of %d characters (got %d)", utils.MaestroMdCharLimit, len(content))
	}

	// Return success with content in ProcessEffect.Data
	return &ExecResult{
		Content: "MAESTRO.md content submitted successfully",
		ProcessEffect: &ProcessEffect{
			Signal: SignalMaestroMdComplete,
			Data: map[string]any{
				"content": content,
			},
		},
	}, nil
}
