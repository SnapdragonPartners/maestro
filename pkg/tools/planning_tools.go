package tools

import (
	"context"
	"fmt"
)

// AskQuestionTool provides structured communication with architect during planning
type AskQuestionTool struct{}

// NewAskQuestionTool creates a new ask question tool instance
func NewAskQuestionTool() *AskQuestionTool {
	return &AskQuestionTool{}
}

// Definition returns the tool's definition in Claude API format
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
					Enum:        []string{"LOW", "MEDIUM", "HIGH"},
				},
			},
			Required: []string{"question"},
		},
	}
}

// Name returns the tool identifier
func (a *AskQuestionTool) Name() string {
	return "ask_question"
}

// Exec executes the ask question operation
func (a *AskQuestionTool) Exec(ctx context.Context, args map[string]any) (any, error) {
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

	// Extract optional context
	context := ""
	if ctxVal, hasCtx := args["context"]; hasCtx {
		if ctxStr, ok := ctxVal.(string); ok {
			context = ctxStr
		}
	}

	// Extract optional urgency (default to MEDIUM)
	urgency := "MEDIUM"
	if urgVal, hasUrg := args["urgency"]; hasUrg {
		if urgStr, ok := urgVal.(string); ok {
			// Validate urgency level
			switch urgStr {
			case "LOW", "MEDIUM", "HIGH":
				urgency = urgStr
			default:
				return nil, fmt.Errorf("urgency must be LOW, MEDIUM, or HIGH")
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

// SubmitPlanTool finalizes planning and triggers review
type SubmitPlanTool struct{}

// NewSubmitPlanTool creates a new submit plan tool instance
func NewSubmitPlanTool() *SubmitPlanTool {
	return &SubmitPlanTool{}
}

// Definition returns the tool's definition in Claude API format
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
					Enum:        []string{"HIGH", "MEDIUM", "LOW"},
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
					Description: "Ordered list of implementation tasks to be completed during coding phase",
					Items: &Property{
						Type: "object",
						Properties: map[string]*Property{
							"task": {
								Type:        "string",
								Description: "Single implementation task description",
							},
						},
						Required: []string{"task"},
					},
				},
			},
			Required: []string{"plan", "confidence", "todos"},
		},
	}
}

// Name returns the tool identifier
func (s *SubmitPlanTool) Name() string {
	return "submit_plan"
}

// Exec executes the submit plan operation
func (s *SubmitPlanTool) Exec(ctx context.Context, args map[string]any) (any, error) {
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

	// Validate confidence level
	switch confidenceStr {
	case "HIGH", "MEDIUM", "LOW":
		// Valid confidence level
	default:
		return nil, fmt.Errorf("confidence must be HIGH, MEDIUM, or LOW")
	}

	// Extract optional exploration summary
	explorationSummary := ""
	if expVal, hasExp := args["exploration_summary"]; hasExp {
		if expStr, ok := expVal.(string); ok {
			explorationSummary = expStr
		}
	}

	// Extract optional risks
	risks := ""
	if riskVal, hasRisk := args["risks"]; hasRisk {
		if riskStr, ok := riskVal.(string); ok {
			risks = riskStr
		}
	}

	// Extract and validate todos
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
			// Convert object todos to structured format
			validatedTodos = make([]map[string]any, len(todosArray))
			for i, todoItem := range todosArray {
				todoObj, ok := todoItem.(map[string]any)
				if !ok {
					return nil, fmt.Errorf("todo item %d must be an object", i)
				}
				task, hasTask := todoObj["task"]
				if !hasTask {
					return nil, fmt.Errorf("todo item %d missing 'task' field", i)
				}
				taskStr, ok := task.(string)
				if !ok {
					return nil, fmt.Errorf("todo item %d 'task' field must be a string", i)
				}
				if taskStr == "" {
					return nil, fmt.Errorf("todo item %d 'task' cannot be empty", i)
				}

				validatedTodos[i] = map[string]any{
					"id":          fmt.Sprintf("todo_%03d", i+1),
					"description": taskStr,
					"completed":   false,
				}
			}
		}
	}

	// Create default todo if none provided
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
