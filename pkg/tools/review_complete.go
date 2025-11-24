package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ReviewCompleteTool signals completion of an approval review with status and feedback.
// Used by architect for plan approval and budget review requests.
type ReviewCompleteTool struct {
	// No executor needed - this is a control flow tool
}

// NewReviewCompleteTool creates a new review_complete tool.
func NewReviewCompleteTool() *ReviewCompleteTool {
	return &ReviewCompleteTool{}
}

// Name returns the tool name.
func (t *ReviewCompleteTool) Name() string {
	return ToolReviewComplete
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *ReviewCompleteTool) PromptDocumentation() string {
	return `- **review_complete** - Complete the review with a decision
  - Parameters:
    - status (string, REQUIRED): Review decision - must be one of: APPROVED, NEEDS_CHANGES, REJECTED
    - feedback (string, REQUIRED): Detailed feedback explaining your decision
  - Call this when you have completed your review and are ready to provide a decision`
}

// Definition returns the tool definition for LLM.
func (t *ReviewCompleteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        ToolReviewComplete,
		Description: "Complete the review with a decision (APPROVED, NEEDS_CHANGES, or REJECTED) and feedback. This tool signals that you have finished your review and are ready to provide your final decision.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"status": {
					Type:        "string",
					Description: "Review decision: APPROVED, NEEDS_CHANGES, or REJECTED",
					Enum:        []string{"APPROVED", "NEEDS_CHANGES", "REJECTED"},
				},
				"feedback": {
					Type:        "string",
					Description: "Detailed feedback explaining your decision and any required changes",
				},
			},
			Required: []string{"status", "feedback"},
		},
	}
}

// Exec executes the tool with the given arguments.
func (t *ReviewCompleteTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Extract and validate status
	status, ok := args["status"].(string)
	if !ok || status == "" {
		return nil, fmt.Errorf("status is required and must be a non-empty string")
	}

	// Normalize status to uppercase
	status = strings.ToUpper(status)

	// Validate status is one of the allowed values
	validStatuses := map[string]bool{
		"APPROVED":      true,
		"NEEDS_CHANGES": true,
		"REJECTED":      true,
	}
	if !validStatuses[status] {
		return nil, fmt.Errorf("status must be one of: APPROVED, NEEDS_CHANGES, REJECTED (got: %s)", status)
	}

	// Extract feedback
	feedback, ok := args["feedback"].(string)
	if !ok || feedback == "" {
		return nil, fmt.Errorf("feedback is required and must be a non-empty string")
	}

	// Return special signal that review is complete
	// The state handler will check for action="review_complete" to know to exit
	result := map[string]any{
		"success":  true,
		"action":   "review_complete",
		"status":   status,
		"feedback": feedback,
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}
