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
					Description: "Your implementation plan ready for architect approval (will be broken into todos in next phase)",
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
				"knowledge_pack": {
					Type:        "string",
					Description: "Relevant knowledge graph subgraph in DOT format (auto-populated, optional)",
				},
			},
			Required: []string{"plan", "confidence"},
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
  - Parameters: plan, confidence (required), exploration_summary (optional)
  - Advances to PLAN_REVIEW for architect approval
  - If the story requires no changes, use done with a summary explaining why`
}

// Exec executes the submit plan operation.
func (s *SubmitPlanTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Validate plan (required)
	plan, ok := args["plan"]
	if !ok {
		return nil, fmt.Errorf("plan parameter is required")
	}

	planStr, ok := plan.(string)
	if !ok {
		return nil, fmt.Errorf("plan must be a string")
	}

	if planStr == "" {
		return nil, fmt.Errorf("plan cannot be empty")
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

	// Extract optional knowledge_pack.
	knowledgePack := ""
	if packVal, hasPack := args["knowledge_pack"]; hasPack {
		if packStr, ok := packVal.(string); ok {
			knowledgePack = packStr
		}
	}

	// Return human-readable message for LLM context
	// Return structured data via ProcessEffect.Data for state machine
	return &ExecResult{
		Content: "Plan submitted successfully, advancing to PLAN_REVIEW for architect approval",
		ProcessEffect: &ProcessEffect{
			Signal: SignalPlanReview,
			Data: map[string]any{
				"plan":                planStr,
				"confidence":          confidenceStr,
				"exploration_summary": explorationSummary,
				"knowledge_pack":      knowledgePack,
			},
		},
	}, nil
}
