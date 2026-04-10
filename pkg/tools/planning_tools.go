package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/proto"
)

// AskQuestionTool provides structured communication with architect during planning.
type AskQuestionTool struct{}

// NewAskQuestionTool creates a new ask question tool instance.
func NewAskQuestionTool() *AskQuestionTool {
	return &AskQuestionTool{}
}

// Definition returns the tool's definition in Claude API format.
func (a *AskQuestionTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "ask_question",
		Description: "Ask the architect for clarification or guidance during planning",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"question": {
					Type:        "string",
					Description: "The specific question or problem you need help with",
				},
				"context": {
					Type:        "string",
					Description: "Relevant context from your exploration (file paths, code snippets, etc.)",
				},
			},
			Required: []string{"question"},
		},
	}
}

// Name returns the tool identifier.
func (a *AskQuestionTool) Name() string {
	return "ask_question"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (a *AskQuestionTool) PromptDocumentation() string {
	return `- **ask_question** - Ask architect for clarification during planning
  - Parameters: question (required), context (optional)
  - Pauses work until architect's answer is received
  - Use when you need guidance on requirements or technical decisions`
}

// Exec executes the ask question operation.
// Returns ProcessEffect to pause the toolloop and transition to QUESTION state.
func (a *AskQuestionTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	question, ok := args["question"]
	if !ok {
		return nil, fmt.Errorf("question parameter is required")
	}

	questionStr, ok := question.(string)
	if !ok {
		return nil, fmt.Errorf("question must be a string")
	}

	if questionStr == "" {
		return nil, fmt.Errorf("question cannot be empty")
	}

	// Extract optional context.
	context := ""
	if ctxVal, hasCtx := args["context"]; hasCtx {
		if ctxStr, ok := ctxVal.(string); ok {
			context = ctxStr
		}
	}

	// Return ProcessEffect to pause the loop and transition to QUESTION state
	// The question data is stored in ProcessEffect.Data for the state machine to process
	return &ExecResult{
		Content: "Question submitted to architect",
		ProcessEffect: &ProcessEffect{
			Signal: string(proto.StateQuestion),
			Data: map[string]string{
				"question": questionStr,
				"context":  context,
			},
		},
	}, nil
}

// StoryCompleteTool signals that the story is already implemented - no work needed.
// Goes to PLAN_REVIEW for architect verification before advancing to DONE.
type StoryCompleteTool struct{}

// NewStoryCompleteTool creates a new story complete tool instance.
func NewStoryCompleteTool() *StoryCompleteTool {
	return &StoryCompleteTool{}
}

// Definition returns the tool's definition in Claude API format.
func (s *StoryCompleteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "story_complete",
		Description: "Signal that the story is already implemented or requires no changes. Architect will verify before marking complete.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"evidence": {
					Type:        "string",
					Description: "Evidence that the story is already complete (file paths, existing functionality, test results)",
				},
				"confidence": {
					Type:        "string",
					Description: "Your confidence level based on codebase exploration",
					Enum:        []string{string(proto.ConfidenceHigh), string(proto.ConfidenceMedium), string(proto.ConfidenceLow)},
				},
				"exploration_summary": {
					Type:        "string",
					Description: "Summary of files explored and key findings (optional)",
				},
			},
			Required: []string{"evidence", "confidence"},
		},
	}
}

// Name returns the tool identifier.
func (s *StoryCompleteTool) Name() string {
	return "story_complete"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *StoryCompleteTool) PromptDocumentation() string {
	return `- **story_complete** - Signal that the story is already implemented
  - Parameters: evidence, confidence (required), exploration_summary (optional)
  - Advances to PLAN_REVIEW for architect verification
  - Use when codebase exploration shows the story requirements are already met
  - Provide specific evidence (file paths, existing functionality, test results)`
}

// Exec executes the story complete operation.
// Returns ProcessEffect to pause the toolloop and transition to PLAN_REVIEW with ApprovalTypeCompletion.
func (s *StoryCompleteTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Validate evidence (required)
	evidence, ok := args["evidence"]
	if !ok {
		return nil, fmt.Errorf("evidence parameter is required")
	}

	evidenceStr, ok := evidence.(string)
	if !ok {
		return nil, fmt.Errorf("evidence must be a string")
	}

	if evidenceStr == "" {
		return nil, fmt.Errorf("evidence cannot be empty")
	}

	// Validate confidence (required)
	confidence, ok := args["confidence"]
	if !ok {
		return nil, fmt.Errorf("confidence parameter is required")
	}

	confidenceStr, ok := confidence.(string)
	if !ok {
		return nil, fmt.Errorf("confidence must be a string")
	}

	// Validate confidence level.
	switch proto.Confidence(confidenceStr) {
	case proto.ConfidenceHigh, proto.ConfidenceMedium, proto.ConfidenceLow:
		// Valid confidence level.
	default:
		return nil, fmt.Errorf("confidence must be %s, %s, or %s", proto.ConfidenceHigh, proto.ConfidenceMedium, proto.ConfidenceLow)
	}

	// Extract optional exploration summary.
	explorationSummary := ""
	if expVal, hasExp := args["exploration_summary"]; hasExp {
		if expStr, ok := expVal.(string); ok {
			explorationSummary = expStr
		}
	}

	return &ExecResult{
		Content: "Story completion claim submitted, requesting architect verification",
		ProcessEffect: &ProcessEffect{
			Signal: SignalStoryComplete,
			Data: map[string]any{
				"evidence":            evidenceStr,
				"confidence":          confidenceStr,
				"exploration_summary": explorationSummary,
			},
		},
	}, nil
}

// SubmitPlanTool finalizes planning and triggers review.
type SubmitPlanTool struct{}

// NewSubmitPlanTool creates a new submit plan tool instance.
func NewSubmitPlanTool() *SubmitPlanTool {
	return &SubmitPlanTool{}
}

// Definition returns the tool's definition in Claude API format.
func (s *SubmitPlanTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "submit_plan",
		Description: "Submit your implementation plan for architect approval",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"plan": {
					Type:        "string",
					Description: "Your implementation plan ready for architect approval",
				},
				"confidence": {
					Type:        "string",
					Description: "Your confidence level based on codebase exploration",
					Enum:        []string{string(proto.ConfidenceHigh), string(proto.ConfidenceMedium), string(proto.ConfidenceLow)},
				},
				"todos": {
					Type:        "array",
					Description: "Ordered list of implementation tasks. Each should start with an action verb and have clear completion criteria. Example: [\"Create main.go with basic structure\", \"Implement HTTP server setup\", \"Add error handling and tests\"]",
					Items: &Property{
						Type: "string",
					},
					MinItems: &[]int{1}[0],
					MaxItems: &[]int{20}[0],
				},
				"exploration_summary": {
					Type:        "string",
					Description: "Summary of files explored and key findings (optional)",
				},
				"knowledge_pack": {
					Type:        "string",
					Description: "Relevant knowledge graph subgraph in DOT format (auto-populated, optional)",
				},
			},
			Required: []string{"plan", "confidence", "todos"},
		},
	}
}

// Name returns the tool identifier.
func (s *SubmitPlanTool) Name() string {
	return "submit_plan"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SubmitPlanTool) PromptDocumentation() string {
	return `- **submit_plan** - Submit implementation plan for architect approval
  - Parameters: plan, confidence, todos (required), exploration_summary (optional)
  - Include 1-20 ordered implementation todos that will track progress during coding
  - Advances to PLAN_REVIEW for architect approval
  - If the story requires no changes, use story_complete with evidence instead`
}

// Exec executes the submit plan operation.
func (s *SubmitPlanTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	planStr, err := extractRequiredString(args, "plan")
	if err != nil {
		return nil, err
	}

	confidenceStr, err := extractRequiredString(args, "confidence")
	if err != nil {
		return nil, err
	}

	// Validate confidence level.
	switch proto.Confidence(confidenceStr) {
	case proto.ConfidenceHigh, proto.ConfidenceMedium, proto.ConfidenceLow:
		// Valid confidence level.
	default:
		return nil, fmt.Errorf("confidence must be %s, %s, or %s", proto.ConfidenceHigh, proto.ConfidenceMedium, proto.ConfidenceLow)
	}

	validatedTodos, err := extractStringArray(args, "todos", 1, 20)
	if err != nil {
		return nil, err
	}

	explorationSummary := extractOptionalString(args, "exploration_summary")
	knowledgePack := extractOptionalString(args, "knowledge_pack")

	// Return human-readable message for LLM context
	// Return structured data via ProcessEffect.Data for state machine
	return &ExecResult{
		Content: "Plan submitted successfully, advancing to PLAN_REVIEW for architect approval",
		ProcessEffect: &ProcessEffect{
			Signal: SignalPlanReview,
			Data: map[string]any{
				"plan":                planStr,
				"confidence":          confidenceStr,
				"todos":               validatedTodos,
				"exploration_summary": explorationSummary,
				"knowledge_pack":      knowledgePack,
			},
		},
	}, nil
}

// extractRequiredString extracts and validates a required string parameter from tool args.
func extractRequiredString(args map[string]any, key string) (string, error) {
	val, ok := args[key]
	if !ok {
		return "", fmt.Errorf("%s parameter is required", key)
	}
	str, ok := val.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	if str == "" {
		return "", fmt.Errorf("%s cannot be empty", key)
	}
	return str, nil
}

// extractOptionalString extracts an optional string parameter from tool args.
func extractOptionalString(args map[string]any, key string) string {
	if val, ok := args[key]; ok {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

// extractStringArray extracts and validates a required array of strings from tool args.
func extractStringArray(args map[string]any, key string, minItems, maxItems int) ([]string, error) {
	raw, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("%s parameter is required", key)
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array", key)
	}
	if len(arr) < minItems || len(arr) > maxItems {
		return nil, fmt.Errorf("%s must contain %d-%d items (got %d)", key, minItems, maxItems, len(arr))
	}
	result := make([]string, len(arr))
	for i, item := range arr {
		str, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s item %d must be a string", key, i)
		}
		if str == "" {
			return nil, fmt.Errorf("%s item %d cannot be empty", key, i)
		}
		result[i] = str
	}
	return result, nil
}
