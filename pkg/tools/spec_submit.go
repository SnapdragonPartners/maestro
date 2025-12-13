package tools

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/specs"
)

// SpecSubmitTool allows PM agent to submit finalized specifications.
type SpecSubmitTool struct {
	projectDir        string
	bootstrapMarkdown string // Injected bootstrap requirements markdown
	inFlight          bool   // True when development is in progress (only hotfixes allowed)
}

// NewSpecSubmitTool creates a new spec submit tool instance.
func NewSpecSubmitTool(projectDir string) *SpecSubmitTool {
	return &SpecSubmitTool{
		projectDir:        projectDir,
		bootstrapMarkdown: "", // Will be injected by PM if bootstrap requirements exist
		inFlight:          false,
	}
}

// SetBootstrapMarkdown injects bootstrap requirements markdown from PM state.
// This allows spec_submit to automatically prepend bootstrap requirements without
// the LLM needing to handle them explicitly.
func (s *SpecSubmitTool) SetBootstrapMarkdown(markdown string) {
	s.bootstrapMarkdown = markdown
}

// SetInFlight sets the in_flight flag from PM state.
// When true, only hotfix submissions are allowed.
func (s *SpecSubmitTool) SetInFlight(inFlight bool) {
	s.inFlight = inFlight
}

// Definition returns the tool's definition in Claude API format.
func (s *SpecSubmitTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "spec_submit",
		Description: "Submit the finalized specification for user review (architect will provide feedback on the spec later). If bootstrap is required, prerequisite sections will be automatically prepended. When development is in progress, only hotfix submissions are allowed.",
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
				"hotfix": {
					Type:        "boolean",
					Description: "Set to true if this is a hotfix (small, scoped change) rather than a full specification. Hotfixes are allowed while development is in progress.",
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
  - Parameters: markdown (required), summary (required), hotfix (optional, boolean)
  - Accepts flexible markdown format - architect will review and provide feedback
  - Use when you have completed the specification interview and drafted the full spec
  - If bootstrap requirements detected, they will be automatically prepended
  - If bootstrap needed but not configured, returns error (call bootstrap tool first)
  - When development is in progress (in_flight=true), set hotfix=true for small changes
  - Full specs (hotfix=false) are rejected while development is in progress`
}

// Exec executes the spec submit operation.
func (s *SpecSubmitTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract hotfix parameter (optional, defaults to false).
	isHotfix := false
	if hotfix, ok := args["hotfix"].(bool); ok {
		isHotfix = hotfix
	}

	// Enforce in_flight restriction: when development is in progress, only hotfixes allowed
	if s.inFlight && !isHotfix {
		return nil, fmt.Errorf("cannot submit new full spec while development is in progress. Wait for completion or scope down to the hotfix level for immediate processing")
	}

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

	// Parse the user specification to extract basic metadata (but don't enforce strict validation).
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

	// For hotfixes, don't include bootstrap spec (it was already submitted with original spec)
	infrastructureSpec := ""
	if !isHotfix {
		infrastructureSpec = strings.TrimSpace(s.bootstrapMarkdown)
	}

	// Return human-readable message for LLM context
	// Return structured data via ProcessEffect.Data for state machine
	// Infrastructure spec (bootstrap markdown) and user spec are kept separate
	return &ExecResult{
		Content: "Specification accepted and ready for user review",
		ProcessEffect: &ProcessEffect{
			Signal: SignalSpecPreview,
			Data: map[string]any{
				"infrastructure_spec": infrastructureSpec,             // Infrastructure requirements (bootstrap) - empty for hotfixes
				"user_spec":           strings.TrimSpace(markdownStr), // User requirements (from LLM)
				"summary":             summaryStr,
				"metadata":            metadata,
				"is_hotfix":           isHotfix, // Pass hotfix flag to state machine
			},
		},
	}, nil
}
