package coder

import (
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/utils"
)

// Signal constants for Coder workflows.
const (
	SignalPlanReview    = "PLAN_REVIEW"
	SignalTesting       = "TESTING"
	SignalQuestion      = "QUESTION"
	SignalBudgetReview  = "BUDGET_REVIEW"
	SignalStoryComplete = "STORY_COMPLETE"
	SignalTodoCollected = "CODING"
)

// PlanningResult contains the outcome of the planning phase toolloop.
// Captures data from submit_plan tool.
//
//nolint:govet // String fields are logically grouped, optimization not beneficial for this result type
type PlanningResult struct {
	Signal             string
	Plan               string
	Confidence         string
	ExplorationSummary string
	Risks              string
	Todos              []PlanTodo
	KnowledgePack      string
}

// CodingResult contains the outcome of the coding phase toolloop.
// Captures data from todo operations and testing requests.
type CodingResult struct {
	Signal         string
	TodosCompleted []string
	TestingRequest bool
}

// TodoCollectionResult contains the outcome of the todo collection phase.
// Captures the list of todos extracted from todos_add tool.
type TodoCollectionResult struct {
	Signal string
	Todos  []string
}

// ExtractPlanningResult extracts the result from planning phase tools.
// Returns the appropriate result based on which terminal tool was called.
func ExtractPlanningResult(calls []agent.ToolCall, results []any) (PlanningResult, error) {
	result := PlanningResult{}

	for i := range calls {
		// Only process successful results
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			continue
		}

		// Check for errors in result
		if success, ok := resultMap["success"].(bool); ok && !success {
			continue // Skip error results
		}

		// Check for next_state signal (submit_plan)
		if nextState, ok := resultMap["next_state"].(string); ok {
			result.Signal = nextState

			// Extract plan data from submit_plan
			if calls[i].Name == "submit_plan" {
				result.Plan = utils.GetMapFieldOr[string](resultMap, "plan", "")
				result.Confidence = utils.GetMapFieldOr[string](resultMap, "confidence", "")
				result.ExplorationSummary = utils.GetMapFieldOr[string](resultMap, "exploration_summary", "")
				result.Risks = utils.GetMapFieldOr[string](resultMap, "risks", "")
				result.KnowledgePack = utils.GetMapFieldOr[string](resultMap, "knowledge_pack", "")

				// Extract todos if present
				todos := utils.GetMapFieldOr[[]any](resultMap, "todos", []any{})
				result.Todos = make([]PlanTodo, len(todos))
				for j, todoItem := range todos {
					if todoMap, ok := utils.SafeAssert[map[string]any](todoItem); ok {
						result.Todos[j] = PlanTodo{
							ID:          utils.GetMapFieldOr[string](todoMap, "id", ""),
							Description: utils.GetMapFieldOr[string](todoMap, "description", ""),
							Completed:   utils.GetMapFieldOr[bool](todoMap, "completed", false),
						}
					}
				}
			}

			return result, nil
		}
	}

	// No terminal tool was called - this is not an error, just means continue looping
	return PlanningResult{}, fmt.Errorf("no terminal tool was called in planning phase")
}

// ExtractCodingResult extracts the result from coding phase tools.
func ExtractCodingResult(calls []agent.ToolCall, _ []any) (CodingResult, error) {
	result := CodingResult{}
	todosCompleted := []string{}

	for i := range calls {
		// Track todo_complete calls (side effects happen in checkCodingTerminal)
		if calls[i].Name == "todo_complete" {
			// Extract index from parameters for tracking
			if index, ok := calls[i].Parameters["index"].(float64); ok {
				todosCompleted = append(todosCompleted, fmt.Sprintf("index_%d", int(index)))
			} else {
				todosCompleted = append(todosCompleted, "current")
			}
		}

		// Check for done tool signal (transitions to TESTING)
		if calls[i].Name == "done" {
			result.Signal = SignalTesting
			result.TodosCompleted = todosCompleted
			result.TestingRequest = true
			return result, nil
		}

		// Check for ask_question signal
		if calls[i].Name == "ask_question" {
			result.Signal = SignalQuestion
			result.TodosCompleted = todosCompleted
			return result, nil
		}
	}

	// No terminal signal - store what we have so far
	result.TodosCompleted = todosCompleted
	if len(result.TodosCompleted) > 0 {
		// Had some activity, just no terminal signal yet
		return result, nil
	}

	// No terminal tool was called
	return CodingResult{}, fmt.Errorf("no terminal tool was called in coding phase")
}

// ExtractTodoCollectionResult extracts the result from todo collection phase.
func ExtractTodoCollectionResult(_ []agent.ToolCall, results []any) (TodoCollectionResult, error) {
	result := TodoCollectionResult{}

	// Debug: log what we're extracting from
	if len(results) == 0 {
		return TodoCollectionResult{}, fmt.Errorf("no tool results to extract todos from")
	}

	// DEBUG: Print what we actually received
	fmt.Printf("DEBUG ExtractTodoCollectionResult: received %d results\n", len(results))
	for i, r := range results {
		fmt.Printf("DEBUG result[%d] type=%T value=%+v\n", i, r, r)
	}

	for i := range results {
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			// Debug: result is not a map - check if it's a slice
			if todosArray, ok := results[i].([]any); ok {
				// Direct array of todos (legacy format?)
				todos := make([]string, 0, len(todosArray))
				for _, todoItem := range todosArray {
					if todoStr, ok := todoItem.(string); ok {
						todos = append(todos, todoStr)
					}
				}
				if len(todos) > 0 {
					result.Todos = todos
					result.Signal = SignalTodoCollected // "CODING"
					return result, nil
				}
			}
			continue
		}

		// Check for errors
		if success, ok := resultMap["success"].(bool); ok && !success {
			continue
		}

		// Extract todos from todos_add result
		if todosRaw, ok := resultMap["todos"]; ok {
			// Use type switch to handle different formats
			switch v := todosRaw.(type) {
			case []any:
				todos := make([]string, 0, len(v))
				for _, todoItem := range v {
					if todoStr, ok := todoItem.(string); ok {
						todos = append(todos, todoStr)
					}
				}
				if len(todos) > 0 {
					result.Todos = todos
					result.Signal = SignalTodoCollected // "CODING"
					return result, nil
				}
			case []string:
				if len(v) > 0 {
					result.Todos = v
					result.Signal = SignalTodoCollected
					return result, nil
				}
			}
		}
	}

	// If we got here, we found results but no todos
	return TodoCollectionResult{}, fmt.Errorf("no todos found in todo collection phase (found %d results, but no valid todos)", len(results))
}
