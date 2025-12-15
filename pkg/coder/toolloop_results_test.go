package coder

import (
	"errors"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
)

func TestExtractPlanningResult_SubmitPlan(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: "submit_plan",
			ID:   "tool_1",
			Parameters: map[string]any{
				"plan": "Implementation plan here",
			},
		},
	}

	results := []any{
		map[string]any{
			"success":             true,
			"next_state":          SignalPlanReview,
			"plan":                "Implementation plan here",
			"confidence":          "high",
			"exploration_summary": "Explored the codebase",
			"risks":               "Some risks",
			"knowledge_pack":      "Key learnings",
			"todos": []any{
				map[string]any{
					"id":          "1",
					"description": "First task",
					"completed":   false,
				},
				map[string]any{
					"id":          "2",
					"description": "Second task",
					"completed":   true,
				},
			},
		},
	}

	result, err := ExtractPlanningResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Signal != SignalPlanReview {
		t.Errorf("Expected signal %s, got %s", SignalPlanReview, result.Signal)
	}
	if result.Plan != "Implementation plan here" {
		t.Errorf("Expected plan to be extracted, got: %s", result.Plan)
	}
	if result.Confidence != "high" {
		t.Errorf("Expected confidence 'high', got: %s", result.Confidence)
	}
	if result.ExplorationSummary != "Explored the codebase" {
		t.Errorf("Expected exploration_summary to be extracted")
	}
	if result.Risks != "Some risks" {
		t.Errorf("Expected risks to be extracted")
	}
	if result.KnowledgePack != "Key learnings" {
		t.Errorf("Expected knowledge_pack to be extracted")
	}
	if len(result.Todos) != 2 {
		t.Errorf("Expected 2 todos, got %d", len(result.Todos))
	}
	if result.Todos[0].Description != "First task" {
		t.Errorf("Expected first todo description 'First task', got: %s", result.Todos[0].Description)
	}
	if result.Todos[1].Completed != true {
		t.Error("Expected second todo to be completed")
	}
}

func TestExtractPlanningResult_NoTerminalTool(t *testing.T) {
	// Test with no submit_plan call
	calls := []agent.ToolCall{
		{
			Name:       "read_file",
			ID:         "tool_1",
			Parameters: map[string]any{"path": "main.go"},
		},
	}

	results := []any{
		map[string]any{
			"success": true,
			"content": "file contents",
		},
	}

	_, err := ExtractPlanningResult(calls, results)
	if !errors.Is(err, toolloop.ErrNoTerminalTool) {
		t.Errorf("Expected ErrNoTerminalTool, got: %v", err)
	}
}

func TestExtractPlanningResult_ErrorResult(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: "submit_plan",
			ID:   "tool_1",
			Parameters: map[string]any{
				"plan": "My plan",
			},
		},
	}

	// Result indicates failure
	results := []any{
		map[string]any{
			"success": false,
			"error":   "Invalid plan format",
		},
	}

	_, err := ExtractPlanningResult(calls, results)
	if !errors.Is(err, toolloop.ErrNoTerminalTool) {
		t.Errorf("Expected ErrNoTerminalTool when result has success=false, got: %v", err)
	}
}

func TestExtractPlanningResult_EmptyInputs(t *testing.T) {
	_, err := ExtractPlanningResult([]agent.ToolCall{}, []any{})
	if !errors.Is(err, toolloop.ErrNoTerminalTool) {
		t.Errorf("Expected ErrNoTerminalTool for empty inputs, got: %v", err)
	}
}

func TestExtractCodingResult_Done(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name:       "todo_complete",
			ID:         "tool_1",
			Parameters: map[string]any{"index": float64(0)},
		},
		{
			Name:       "done",
			ID:         "tool_2",
			Parameters: map[string]any{},
		},
	}

	results := []any{
		map[string]any{"success": true},
		map[string]any{"success": true},
	}

	result, err := ExtractCodingResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Signal != SignalTesting {
		t.Errorf("Expected signal %s, got %s", SignalTesting, result.Signal)
	}
	if !result.TestingRequest {
		t.Error("Expected TestingRequest to be true")
	}
	if len(result.TodosCompleted) != 1 {
		t.Errorf("Expected 1 todo completed, got %d", len(result.TodosCompleted))
	}
	if result.TodosCompleted[0] != "index_0" {
		t.Errorf("Expected todo completion 'index_0', got: %s", result.TodosCompleted[0])
	}
}

func TestExtractCodingResult_AskQuestion(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: "ask_question",
			ID:   "tool_1",
			Parameters: map[string]any{
				"question": "How should I implement this?",
				"context":  "Working on feature X",
				"urgency":  "high",
			},
		},
	}

	results := []any{
		map[string]any{"success": true},
	}

	result, err := ExtractCodingResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Signal != SignalQuestion {
		t.Errorf("Expected signal %s, got %s", SignalQuestion, result.Signal)
	}
	if result.Question != "How should I implement this?" {
		t.Errorf("Expected question to be extracted, got: %s", result.Question)
	}
	if result.Context != "Working on feature X" {
		t.Errorf("Expected context to be extracted")
	}
	if result.Urgency != "high" {
		t.Errorf("Expected urgency 'high', got: %s", result.Urgency)
	}
}

func TestExtractCodingResult_AskQuestion_DefaultUrgency(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: "ask_question",
			ID:   "tool_1",
			Parameters: map[string]any{
				"question": "How do I do this?",
			},
		},
	}

	results := []any{
		map[string]any{"success": true},
	}

	result, err := ExtractCodingResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Urgency != "medium" {
		t.Errorf("Expected default urgency 'medium', got: %s", result.Urgency)
	}
}

func TestExtractCodingResult_TodoCompleteOnly(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name:       "todo_complete",
			ID:         "tool_1",
			Parameters: map[string]any{"index": float64(1)},
		},
		{
			Name:       "todo_complete",
			ID:         "tool_2",
			Parameters: map[string]any{}, // No index - uses "current"
		},
	}

	results := []any{
		map[string]any{"success": true},
		map[string]any{"success": true},
	}

	result, err := ExtractCodingResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// No terminal signal, but activity tracked
	if result.Signal != "" {
		t.Errorf("Expected empty signal, got: %s", result.Signal)
	}
	if len(result.TodosCompleted) != 2 {
		t.Errorf("Expected 2 todos completed, got %d", len(result.TodosCompleted))
	}
	if result.TodosCompleted[0] != "index_1" {
		t.Errorf("Expected 'index_1', got: %s", result.TodosCompleted[0])
	}
	if result.TodosCompleted[1] != "current" {
		t.Errorf("Expected 'current', got: %s", result.TodosCompleted[1])
	}
}

func TestExtractCodingResult_NoActivity(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name:       "read_file",
			ID:         "tool_1",
			Parameters: map[string]any{"path": "main.go"},
		},
	}

	results := []any{
		map[string]any{"success": true, "content": "contents"},
	}

	_, err := ExtractCodingResult(calls, results)
	if !errors.Is(err, toolloop.ErrNoActivity) {
		t.Errorf("Expected ErrNoActivity, got: %v", err)
	}
}

func TestExtractTodoCollectionResult_TodosFromMap(t *testing.T) {
	calls := []agent.ToolCall{
		{
			Name: "todos_add",
			ID:   "tool_1",
			Parameters: map[string]any{
				"todos": []string{"Task 1", "Task 2"},
			},
		},
	}

	results := []any{
		map[string]any{
			"success": true,
			"todos":   []any{"Task 1", "Task 2", "Task 3"},
		},
	}

	result, err := ExtractTodoCollectionResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if result.Signal != SignalTodoCollected {
		t.Errorf("Expected signal %s, got %s", SignalTodoCollected, result.Signal)
	}
	if len(result.Todos) != 3 {
		t.Errorf("Expected 3 todos, got %d", len(result.Todos))
	}
	if result.Todos[0] != "Task 1" {
		t.Errorf("Expected first todo 'Task 1', got: %s", result.Todos[0])
	}
}

func TestExtractTodoCollectionResult_TodosDirectArray(t *testing.T) {
	calls := []agent.ToolCall{
		{Name: "todos_add", ID: "tool_1"},
	}

	// Legacy format - direct array
	results := []any{
		[]any{"Task A", "Task B"},
	}

	result, err := ExtractTodoCollectionResult(calls, results)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	if len(result.Todos) != 2 {
		t.Errorf("Expected 2 todos, got %d", len(result.Todos))
	}
	if result.Todos[0] != "Task A" {
		t.Errorf("Expected 'Task A', got: %s", result.Todos[0])
	}
}

func TestExtractTodoCollectionResult_NoResults(t *testing.T) {
	calls := []agent.ToolCall{}
	results := []any{}

	_, err := ExtractTodoCollectionResult(calls, results)
	if !errors.Is(err, toolloop.ErrNoActivity) {
		t.Errorf("Expected ErrNoActivity, got: %v", err)
	}
}

func TestExtractTodoCollectionResult_NoTodos(t *testing.T) {
	calls := []agent.ToolCall{
		{Name: "read_file", ID: "tool_1"},
	}

	results := []any{
		map[string]any{
			"success": true,
			"content": "file contents",
		},
	}

	_, err := ExtractTodoCollectionResult(calls, results)
	if !errors.Is(err, toolloop.ErrNoTerminalTool) {
		t.Errorf("Expected ErrNoTerminalTool, got: %v", err)
	}
}

func TestExtractTodoCollectionResult_ErrorResult(t *testing.T) {
	calls := []agent.ToolCall{
		{Name: "todos_add", ID: "tool_1"},
	}

	results := []any{
		map[string]any{
			"success": false,
			"error":   "Invalid format",
		},
	}

	_, err := ExtractTodoCollectionResult(calls, results)
	if !errors.Is(err, toolloop.ErrNoTerminalTool) {
		t.Errorf("Expected ErrNoTerminalTool when result has success=false, got: %v", err)
	}
}

func TestSignalConstants(t *testing.T) {
	// Verify signal constants are non-empty and unique
	signals := []string{
		SignalPlanReview,
		SignalTesting,
		SignalQuestion,
		SignalBudgetReview,
		SignalStoryComplete,
		SignalTodoCollected,
	}

	seen := make(map[string]bool)
	for _, sig := range signals {
		if sig == "" {
			t.Error("Signal constant should not be empty")
		}
		if seen[sig] {
			t.Errorf("Duplicate signal constant: %s", sig)
		}
		seen[sig] = true
	}
}

func TestPlanningResultStruct(t *testing.T) {
	result := PlanningResult{
		Signal:             "TEST",
		Plan:               "Plan text",
		Confidence:         "high",
		ExplorationSummary: "Summary",
		Risks:              "Risks",
		Todos: []PlanTodo{
			{ID: "1", Description: "Task", Completed: false},
		},
		KnowledgePack: "Knowledge",
	}

	if result.Signal != "TEST" {
		t.Error("Signal not set correctly")
	}
	if result.Plan != "Plan text" {
		t.Error("Plan not set correctly")
	}
	if result.Confidence != "high" {
		t.Error("Confidence not set correctly")
	}
	if result.ExplorationSummary != "Summary" {
		t.Error("ExplorationSummary not set correctly")
	}
	if result.Risks != "Risks" {
		t.Error("Risks not set correctly")
	}
	if len(result.Todos) != 1 {
		t.Error("Todos not set correctly")
	}
	if result.KnowledgePack != "Knowledge" {
		t.Error("KnowledgePack not set correctly")
	}
}

func TestCodingResultStruct(t *testing.T) {
	result := CodingResult{
		Signal:         SignalTesting,
		TodosCompleted: []string{"index_0", "index_1"},
		TestingRequest: true,
		Question:       "Q",
		Context:        "C",
		Urgency:        "U",
	}

	if result.Signal != SignalTesting {
		t.Error("Signal not set correctly")
	}
	if len(result.TodosCompleted) != 2 {
		t.Error("TodosCompleted not set correctly")
	}
	if !result.TestingRequest {
		t.Error("TestingRequest not set correctly")
	}
	if result.Question != "Q" {
		t.Error("Question not set correctly")
	}
	if result.Context != "C" {
		t.Error("Context not set correctly")
	}
	if result.Urgency != "U" {
		t.Error("Urgency not set correctly")
	}
}

func TestTodoCollectionResultStruct(t *testing.T) {
	result := TodoCollectionResult{
		Signal: SignalTodoCollected,
		Todos:  []string{"A", "B", "C"},
	}

	if result.Signal != SignalTodoCollected {
		t.Error("Signal not set correctly")
	}
	if len(result.Todos) != 3 {
		t.Error("Todos not set correctly")
	}
}
