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
				"urgency": {
					Type:        "string",
					Description: "How critical this question is for proceeding",
					Enum:        []string{string(proto.PriorityLow), string(proto.PriorityMedium), string(proto.PriorityHigh)},
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
  - Parameters: question (required), context, urgency
  - Handled inline via Effects pattern, blocks until architect's answer received
  - Use when you need guidance on requirements or technical decisions`
}

// Exec executes the ask question operation.
func (a *AskQuestionTool) Exec(_ context.Context, args map[string]any) (any, error) {
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

	// Extract optional urgency (default to MEDIUM)
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

	return map[string]any{
		"success":    true,
		"message":    "Question handled inline via Effects pattern",
		"question":   questionStr,
		"context":    context,
		"urgency":    urgency,
		"next_state": "INLINE_HANDLED",
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
func (s *SubmitPlanTool) Exec(_ context.Context, args map[string]any) (any, error) {
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

	// Mode 1: Story already complete - no coding needed
	if isCompleteBool {
		return map[string]any{
			"success":             true,
			"message":             "Story marked as complete, requesting architect verification",
			"plan":                planStr, // Contains evidence
			"confidence":          confidenceStr,
			"exploration_summary": explorationSummary,
			"knowledge_pack":      knowledgePack,
			"next_state":          "STORY_COMPLETE",
		}, nil
	}

	// Mode 2: Implementation plan - will be broken into todos in next phase
	return map[string]any{
		"success":             true,
		"message":             "Plan submitted successfully, advancing to PLAN_REVIEW for architect approval",
		"plan":                planStr,
		"confidence":          confidenceStr,
		"exploration_summary": explorationSummary,
		"knowledge_pack":      knowledgePack,
		"next_state":          "PLAN_REVIEW",
	}, nil
}

