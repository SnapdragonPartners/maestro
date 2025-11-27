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
		Description: "Submit your plan or mark the story as already complete",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"is_complete": {
					Type:        "boolean",
					Description: "True if story is already fully implemented (no coding needed), false if submitting an implementation plan for approval",
				},
				"plan": {
					Type:        "string",
					Description: "If is_complete=false: your implementation plan ready for architect approval (will be broken into todos in next phase). If is_complete=true: evidence showing the story is already implemented",
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
			Required: []string{"is_complete", "plan", "confidence"},
		},
	}
}

// Name returns the tool identifier.
func (s *SubmitPlanTool) Name() string {
	return "submit_plan"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (s *SubmitPlanTool) PromptDocumentation() string {
	return `- **submit_plan** - Submit implementation plan OR mark story as already complete
  - Parameters: is_complete (boolean), plan, confidence (all required), exploration_summary (optional)
  - If is_complete=false: provide implementation plan ready for architect approval (will be broken into todos in next phase, advances to PLAN_REVIEW)
  - If is_complete=true: provide evidence that story is already done (advances to STORY_COMPLETE for architect verification)`
}

// Exec executes the submit plan operation.
//
//nolint:cyclop // Complex plan validation logic, acceptable for this use case
func (s *SubmitPlanTool) Exec(_ context.Context, args map[string]any) (*ExecResult, error) {
	// Check is_complete flag (required)
	isComplete, ok := args["is_complete"]
	if !ok {
		return nil, fmt.Errorf("is_complete parameter is required")
	}

	isCompleteBool, ok := isComplete.(bool)
	if !ok {
		return nil, fmt.Errorf("is_complete must be a boolean")
	}

	// Validate plan (required for both modes)
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

	// Validate confidence (required for both modes)
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
	if isCompleteBool {
		// Mode 1: Story already complete - no coding needed
		return &ExecResult{
			Content: "Story marked as complete, requesting architect verification",
			ProcessEffect: &ProcessEffect{
				Signal: SignalPlanReview, // Uses same signal, state machine checks is_complete flag
				Data: map[string]any{
					"is_complete":         true,
					"plan":                planStr, // Contains evidence
					"confidence":          confidenceStr,
					"exploration_summary": explorationSummary,
					"knowledge_pack":      knowledgePack,
				},
			},
		}, nil
	}

	// Mode 2: Implementation plan - will be broken into todos in next phase
	return &ExecResult{
		Content: "Plan submitted successfully, advancing to PLAN_REVIEW for architect approval",
		ProcessEffect: &ProcessEffect{
			Signal: SignalPlanReview,
			Data: map[string]any{
				"is_complete":         false,
				"plan":                planStr,
				"confidence":          confidenceStr,
				"exploration_summary": explorationSummary,
				"knowledge_pack":      knowledgePack,
			},
		},
	}, nil
}
