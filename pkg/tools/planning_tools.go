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
		"message":    "Question submitted, transitioning to QUESTION state",
		"question":   questionStr,
		"context":    context,
		"urgency":    urgency,
		"next_state": "QUESTION",
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
		Description: "Submit your final implementation plan to advance to review phase",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"plan": {
					Type:        "string",
					Description: "Your complete implementation plan (JSON or markdown format)",
				},
				"confidence": {
					Type:        "string",
					Description: "Your confidence level based on codebase exploration",
					Enum:        []string{string(proto.PriorityHigh), string(proto.PriorityMedium), string(proto.PriorityLow)},
				},
				"exploration_summary": {
					Type:        "string",
					Description: "Summary of files explored and key findings",
				},
				"risks": {
					Type:        "string",
					Description: "Potential risks or challenges identified (optional)",
				},
				"todos": {
					Type:        "array",
					Description: "Ordered list of implementation tasks (imperative, 5-15 words)",
					Items: &Property{
						Type: "string",
					},
					MinItems: &[]int{1}[0],
					MaxItems: &[]int{25}[0],
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

// Exec executes the submit plan operation.
//
//nolint:cyclop // Complex plan validation logic, acceptable for this use case
func (s *SubmitPlanTool) Exec(_ context.Context, args map[string]any) (any, error) {
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

	confidence, ok := args["confidence"]
	if !ok {
		return nil, fmt.Errorf("confidence parameter is required")
	}

	confidenceStr, ok := confidence.(string)
	if !ok {
		return nil, fmt.Errorf("confidence must be a string")
	}

	// Validate confidence level.
	switch confidenceStr {
	case "HIGH", "MEDIUM", "LOW":
		// Valid confidence level.
	default:
		return nil, fmt.Errorf("confidence must be HIGH, MEDIUM, or LOW")
	}

	// Extract optional exploration summary.
	explorationSummary := ""
	if expVal, hasExp := args["exploration_summary"]; hasExp {
		if expStr, ok := expVal.(string); ok {
			explorationSummary = expStr
		}
	}

	// Extract optional risks.
	risks := ""
	if riskVal, hasRisk := args["risks"]; hasRisk {
		if riskStr, ok := riskVal.(string); ok {
			risks = riskStr
		}
	}

	// Extract and validate todos.
	todos, hasTodos := args["todos"]
	if !hasTodos {
		return nil, fmt.Errorf("todos parameter is required")
	}

	var validatedTodos []map[string]any
	if hasTodos {
		todosArray, ok := todos.([]any)
		if !ok {
			return nil, fmt.Errorf("todos must be an array")
		}

		if len(todosArray) > 0 {
			// Convert string todos to structured format.
			validatedTodos = make([]map[string]any, len(todosArray))
			for i, todoItem := range todosArray {
				todoStr, ok := todoItem.(string)
				if !ok {
					return nil, fmt.Errorf("todo item %d must be a string", i)
				}
				if todoStr == "" {
					return nil, fmt.Errorf("todo item %d cannot be empty", i)
				}

				validatedTodos[i] = map[string]any{
					"id":          fmt.Sprintf("todo_%03d", i+1),
					"description": todoStr,
					"completed":   false,
				}
			}
		}
	}

	// Create default todo if none provided.
	if len(validatedTodos) == 0 {
		validatedTodos = []map[string]any{
			{
				"id":          "todo_001",
				"description": "Implement task according to plan",
				"completed":   false,
			},
		}
	}

	return map[string]any{
		"success":             true,
		"message":             "Plan submitted successfully, advancing to PLAN_REVIEW",
		"plan":                planStr,
		"confidence":          confidenceStr,
		"exploration_summary": explorationSummary,
		"risks":               risks,
		"todos":               validatedTodos,
		"next_state":          "PLAN_REVIEW",
	}, nil
}

// MarkStoryCompleteTool signals that story requirements are already implemented.
type MarkStoryCompleteTool struct{}

// NewMarkStoryCompleteTool creates a new mark story complete tool instance.
func NewMarkStoryCompleteTool() *MarkStoryCompleteTool {
	return &MarkStoryCompleteTool{}
}

// Definition returns the tool's definition in Claude API format.
func (m *MarkStoryCompleteTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "mark_story_complete",
		Description: "Signal that the story requirements are already fully implemented",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"reason": {
					Type:        "string",
					Description: "Clear explanation of why the story is already complete",
				},
				"evidence": {
					Type:        "string",
					Description: "File paths and code evidence supporting the completion claim",
				},
				"confidence": {
					Type:        "string",
					Description: "Your confidence level in this assessment",
					Enum:        []string{string(proto.PriorityHigh), string(proto.PriorityMedium), string(proto.PriorityLow)},
				},
			},
			Required: []string{"reason", "evidence", "confidence"},
		},
	}
}

// Name returns the tool identifier.
func (m *MarkStoryCompleteTool) Name() string {
	return "mark_story_complete"
}

// Exec executes the mark story complete operation.
func (m *MarkStoryCompleteTool) Exec(_ context.Context, args map[string]any) (any, error) {
	reason, ok := args["reason"]
	if !ok {
		return nil, fmt.Errorf("reason parameter is required")
	}

	reasonStr, ok := reason.(string)
	if !ok {
		return nil, fmt.Errorf("reason must be a string")
	}

	if reasonStr == "" {
		return nil, fmt.Errorf("reason cannot be empty")
	}

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

	confidence, ok := args["confidence"]
	if !ok {
		return nil, fmt.Errorf("confidence parameter is required")
	}

	confidenceStr, ok := confidence.(string)
	if !ok {
		return nil, fmt.Errorf("confidence must be a string")
	}

	// Validate confidence level.
	switch confidenceStr {
	case "HIGH", "MEDIUM", "LOW":
		// Valid confidence level.
	default:
		return nil, fmt.Errorf("confidence must be HIGH, MEDIUM, or LOW")
	}

	return map[string]any{
		"success":    true,
		"message":    "Story completion request submitted, requesting architect approval",
		"reason":     reasonStr,
		"evidence":   evidenceStr,
		"confidence": confidenceStr,
		"next_state": "COMPLETION_REVIEW",
	}, nil
}
