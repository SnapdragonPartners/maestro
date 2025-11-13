package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
)

// SpecFeedbackTool allows architect to send feedback to PM about submitted specs.
type SpecFeedbackTool struct{}

// NewSpecFeedbackTool creates a new spec feedback tool instance.
func NewSpecFeedbackTool() *SpecFeedbackTool {
	return &SpecFeedbackTool{}
}

// Definition returns the tool's definition in Claude API format.
func (s *SpecFeedbackTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "spec_feedback",
		Description: "Send feedback, questions, or requested improvements to PM about their submitted specification",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"feedback": {
					Type:        "string",
					Description: "Detailed feedback: questions for clarification, suggested improvements, concerns, or specific changes needed",
				},
				"urgency": {
					Type:        "string",
					Description: "Priority level for this feedback",
					Enum:        []string{string(proto.PriorityLow), string(proto.PriorityMedium), string(proto.PriorityHigh)},
				},
			},
			Required: []string{"feedback"},
		},
	}
}

// Name returns the tool identifier.
func (s *SpecFeedbackTool) Name() string {
	return "spec_feedback"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SpecFeedbackTool) PromptDocumentation() string {
	return `- **spec_feedback** - Send feedback to PM about their submitted specification
  - Parameters: feedback (required), urgency (optional: low/medium/high)
  - Use when spec needs clarification, improvements, or has concerns
  - PM will receive feedback and re-enter interview loop to address issues
  - Handled via Effects pattern - sends RESULT(approved=false, feedback=...) to PM`
}

// Exec executes the spec feedback operation.
func (s *SpecFeedbackTool) Exec(_ context.Context, args map[string]any) (any, error) {
	// Extract feedback parameter.
	feedback, ok := args["feedback"]
	if !ok {
		return nil, fmt.Errorf("feedback parameter is required")
	}

	feedbackStr, ok := feedback.(string)
	if !ok {
		return nil, fmt.Errorf("feedback must be a string")
	}

	if feedbackStr == "" {
		return nil, fmt.Errorf("feedback cannot be empty")
	}

	// Extract optional urgency (default to MEDIUM).
	urgency := string(proto.PriorityMedium)
	if urgVal, hasUrg := args["urgency"]; hasUrg {
		if urgStr, ok := urgVal.(string); ok {
			// Validate urgency level.
			switch urgStr {
			case string(proto.PriorityLow), string(proto.PriorityMedium), string(proto.PriorityHigh):
				urgency = urgStr
			default:
				return nil, fmt.Errorf("urgency must be %s, %s, or %s", proto.PriorityLow, proto.PriorityMedium, proto.PriorityHigh)
			}
		}
	}

	// Return success - actual RESULT message sent via Effects pattern.
	return map[string]any{
		"success":  true,
		"message":  "Feedback sent to PM",
		"feedback": feedbackStr,
		"urgency":  urgency,
	}, nil
}
