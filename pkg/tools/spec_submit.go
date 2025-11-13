package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/specs"
)

// SpecSubmitTool allows PM agent to submit finalized specifications.
type SpecSubmitTool struct{}

// NewSpecSubmitTool creates a new spec submit tool instance.
func NewSpecSubmitTool() *SpecSubmitTool {
	return &SpecSubmitTool{}
}

// Definition returns the tool's definition in Claude API format.
func (s *SpecSubmitTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "spec_submit",
		Description: "Submit the finalized specification for user review (architect will provide feedback on the spec later)",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"markdown": {
					Type:        "string",
					Description: "The complete specification in markdown format (flexible format - architect will review)",
				},
				"summary": {
					Type:        "string",
					Description: "Brief summary of the specification (1-2 sentences)",
				},
			},
			Required: []string{"markdown", "summary"},
		},
	}
}

// Name returns the tool identifier.
func (s *SpecSubmitTool) Name() string {
	return "spec_submit"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SpecSubmitTool) PromptDocumentation() string {
	return `- **spec_submit** - Submit finalized specification for user review
  - Parameters: markdown (required), summary (required)
  - Accepts flexible markdown format - architect will review and provide feedback
  - Use when you have completed the specification interview and drafted the full spec`
}

// Exec executes the spec submit operation.
func (s *SpecSubmitTool) Exec(_ context.Context, args map[string]any) (any, error) {
	// Extract markdown parameter.
	markdown, ok := args["markdown"]
	if !ok {
		return nil, fmt.Errorf("markdown parameter is required")
	}

	markdownStr, ok := markdown.(string)
	if !ok {
		return nil, fmt.Errorf("markdown must be a string")
	}

	if markdownStr == "" {
		return nil, fmt.Errorf("markdown cannot be empty")
	}

	// Extract summary parameter.
	summary, ok := args["summary"]
	if !ok {
		return nil, fmt.Errorf("summary parameter is required")
	}

	summaryStr, ok := summary.(string)
	if !ok {
		return nil, fmt.Errorf("summary must be a string")
	}

	if summaryStr == "" {
		return nil, fmt.Errorf("summary cannot be empty")
	}

	// Parse the specification to extract basic metadata (but don't enforce strict validation).
	// The architect will review the spec and provide feedback if needed.
	spec, err := specs.Parse(markdownStr)

	// Build metadata (best effort - use empty values if parsing failed)
	metadata := map[string]any{}
	if err == nil && spec != nil {
		metadata = map[string]any{
			"title":              spec.Title,
			"version":            spec.Version,
			"priority":           spec.Priority,
			"requirements_count": len(spec.Requirements),
		}
	}

	// Accept the spec and let architect review - no strict validation here.
	// This allows PM flexibility in spec format, and architect can request changes via feedback loop.
	return map[string]any{
		"success":       true,
		"message":       "Specification accepted and ready for user review",
		"summary":       summaryStr,
		"spec_markdown": markdownStr, // Store for preview display
		"metadata":      metadata,
		// Signal to PM driver to transition to PREVIEW state
		"preview_ready": true,
	}, nil
}
