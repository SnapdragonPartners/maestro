package coder

import (
	"context"
	"fmt"
	"testing"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
)

// TestAskQuestionToolValidation tests the AskQuestionTool validation and execution.
func TestAskQuestionToolValidation(t *testing.T) {
	tool := tools.NewAskQuestionTool()

	// Test tool definition.
	def := tool.Definition()
	if def.Name != "ask_question" {
		t.Errorf("Expected tool name 'ask_question', got %s", def.Name)
	}

	if def.Description == "" {
		t.Error("Expected non-empty description")
	}

	// Test required parameters.
	if len(def.InputSchema.Required) != 1 || def.InputSchema.Required[0] != "question" {
		t.Errorf("Expected required parameter 'question', got %v", def.InputSchema.Required)
	}

	// Test valid tool execution.
	ctx := context.Background()
	validArgs := map[string]any{
		"question": "How should I implement this feature?",
		"context":  "Found existing patterns in the codebase",
		"urgency":  "HIGH",
	}

	result, err := tool.Exec(ctx, validArgs)
	if err != nil {
		t.Fatalf("Expected successful execution, got error: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatal("Expected result to be a map")
	}

	// Validate result structure.
	if success, exists := resultMap["success"]; !exists || success != true {
		t.Error("Expected success field to be true")
	}

	if question, exists := resultMap["question"]; !exists || question != validArgs["question"] {
		t.Error("Expected question to be preserved in result")
	}

	if urgency, exists := resultMap["urgency"]; !exists || urgency != "HIGH" {
		t.Error("Expected urgency to be preserved")
	}

	if nextState, exists := resultMap["next_state"]; !exists || nextState != string(StateQuestion) {
		t.Error("Expected next_state to be QUESTION")
	}
}

// TestAskQuestionToolErrorHandling tests error scenarios.
func TestAskQuestionToolErrorHandling(t *testing.T) {
	tool := tools.NewAskQuestionTool()
	ctx := context.Background()

	//nolint:govet // Test struct, optimization not critical
	testCases := []struct {
		name        string
		args        map[string]any
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Missing question parameter",
			args:        map[string]any{"context": "some context"},
			expectError: true,
			errorMsg:    "question parameter is required",
		},
		{
			name:        "Empty question",
			args:        map[string]any{"question": ""},
			expectError: true,
			errorMsg:    "question cannot be empty",
		},
		{
			name:        "Invalid question type",
			args:        map[string]any{"question": 123},
			expectError: true,
			errorMsg:    "question must be a string",
		},
		{
			name: "Invalid urgency level",
			args: map[string]any{
				"question": "Valid question?",
				"urgency":  "INVALID",
			},
			expectError: true,
			errorMsg:    "urgency must be LOW, MEDIUM, or HIGH",
		},
		{
			name: "Valid with defaults",
			args: map[string]any{
				"question": "Valid question without optional params?",
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Exec(ctx, tc.args)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none, result: %v", result)
				} else if err.Error() != tc.errorMsg {
					t.Errorf("Expected error message '%s', got '%s'", tc.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				// Verify default values are set.
				if resultMap, ok := result.(map[string]any); ok {
					if urgency, exists := resultMap["urgency"]; !exists || urgency != "MEDIUM" {
						t.Error("Expected default urgency to be MEDIUM")
					}
					if context, exists := resultMap["context"]; !exists || context != "" {
						t.Error("Expected default context to be empty string")
					}
				}
			}
		})
	}
}

// TestSubmitPlanToolValidation tests the SubmitPlanTool validation and execution.
func TestSubmitPlanToolValidation(t *testing.T) {
	tool := tools.NewSubmitPlanTool()

	// Test tool definition.
	def := tool.Definition()
	if def.Name != "submit_plan" {
		t.Errorf("Expected tool name 'submit_plan', got %s", def.Name)
	}

	// Test required parameters.
	expectedRequired := []string{"plan", "confidence", "todos"}
	if len(def.InputSchema.Required) != len(expectedRequired) {
		t.Errorf("Expected %d required parameters, got %d", len(expectedRequired), len(def.InputSchema.Required))
	}

	for _, param := range expectedRequired {
		found := false
		for _, required := range def.InputSchema.Required {
			if required == param {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected required parameter '%s' not found", param)
		}
	}

	// Test valid execution.
	ctx := context.Background()
	validArgs := map[string]any{
		"plan":                "Detailed implementation plan...",
		"confidence":          string(proto.ConfidenceHigh),
		"exploration_summary": "Explored 15 files, found 3 patterns",
		"risks":               "Potential performance impact on auth flow",
		"todos":               []any{"Implement authentication logic", "Add validation", "Update tests"},
	}

	result, err := tool.Exec(ctx, validArgs)
	if err != nil {
		t.Fatalf("Expected successful execution, got error: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatal("Expected result to be a map")
	}

	// Validate result structure.
	if success, exists := resultMap["success"]; !exists || success != true {
		t.Error("Expected success field to be true")
	}

	if plan, exists := resultMap["plan"]; !exists || plan != validArgs["plan"] {
		t.Error("Expected plan to be preserved in result")
	}

	if confidence, exists := resultMap["confidence"]; !exists || confidence != string(proto.ConfidenceHigh) {
		t.Error("Expected confidence to be preserved")
	}

	if nextState, exists := resultMap["next_state"]; !exists || nextState != string(StatePlanReview) {
		t.Error("Expected next_state to be PLAN_REVIEW")
	}
}

// TestSubmitPlanToolErrorHandling tests error scenarios for submit_plan tool.
func TestSubmitPlanToolErrorHandling(t *testing.T) {
	tool := tools.NewSubmitPlanTool()
	ctx := context.Background()

	//nolint:govet // Test struct, optimization not critical
	testCases := []struct {
		name        string
		args        map[string]any
		expectError bool
		errorMsg    string
	}{
		{
			name:        "Missing plan parameter",
			args:        map[string]any{"confidence": string(proto.ConfidenceHigh)},
			expectError: true,
			errorMsg:    "plan parameter is required",
		},
		{
			name:        "Missing confidence parameter",
			args:        map[string]any{"plan": "Some plan"},
			expectError: true,
			errorMsg:    "confidence parameter is required",
		},
		{
			name:        "Empty plan",
			args:        map[string]any{"plan": "", "confidence": string(proto.ConfidenceHigh)},
			expectError: true,
			errorMsg:    "plan cannot be empty",
		},
		{
			name:        "Invalid plan type",
			args:        map[string]any{"plan": 123, "confidence": string(proto.ConfidenceHigh)},
			expectError: true,
			errorMsg:    "plan must be a string",
		},
		{
			name:        "Invalid confidence type",
			args:        map[string]any{"plan": "Valid plan", "confidence": 123},
			expectError: true,
			errorMsg:    "confidence must be a string",
		},
		{
			name: "Invalid confidence level",
			args: map[string]any{
				"plan":       "Valid plan",
				"confidence": "INVALID",
				"todos":      []any{"Some task"},
			},
			expectError: true,
			errorMsg:    fmt.Sprintf("confidence must be %s, %s, or %s", proto.ConfidenceHigh, proto.ConfidenceMedium, proto.ConfidenceLow),
		},
		{
			name: "Valid with minimal parameters",
			args: map[string]any{
				"plan":       "Minimal valid plan",
				"confidence": "MEDIUM",
				"todos":      []any{"Implement feature"},
			},
			expectError: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Exec(ctx, tc.args)

			if tc.expectError {
				if err == nil {
					t.Errorf("Expected error but got none, result: %v", result)
				} else if err.Error() != tc.errorMsg {
					t.Errorf("Expected error message '%s', got '%s'", tc.errorMsg, err.Error())
				}
			} else {
				if err != nil {
					t.Errorf("Expected no error but got: %v", err)
				}
				// Verify optional fields default to empty string.
				if resultMap, ok := result.(map[string]any); ok {
					if exploration, exists := resultMap["exploration_summary"]; !exists || exploration != "" {
						t.Error("Expected default exploration_summary to be empty string")
					}
					if risks, exists := resultMap["risks"]; !exists || risks != "" {
						t.Error("Expected default risks to be empty string")
					}
				}
			}
		})
	}
}

// TestToolDefinitionConsistency tests that tool definitions are consistent.
func TestToolDefinitionConsistency(t *testing.T) {
	askTool := tools.NewAskQuestionTool()
	submitTool := tools.NewSubmitPlanTool()

	// Test tool names match their functions.
	if askTool.Name() != askTool.Definition().Name {
		t.Error("AskQuestionTool Name() method doesn't match Definition().Name")
	}

	if submitTool.Name() != submitTool.Definition().Name {
		t.Error("SubmitPlanTool Name() method doesn't match Definition().Name")
	}

	// Test all required parameters have property definitions.
	askDef := askTool.Definition()
	for _, required := range askDef.InputSchema.Required {
		if _, exists := askDef.InputSchema.Properties[required]; !exists {
			t.Errorf("AskQuestionTool required parameter '%s' missing from properties", required)
		}
	}

	submitDef := submitTool.Definition()
	for _, required := range submitDef.InputSchema.Required {
		if _, exists := submitDef.InputSchema.Properties[required]; !exists {
			t.Errorf("SubmitPlanTool required parameter '%s' missing from properties", required)
		}
	}

	// Test enum validations exist for constrained fields.
	if urgencyProp, exists := askDef.InputSchema.Properties["urgency"]; exists {
		if len(urgencyProp.Enum) == 0 {
			t.Error("AskQuestionTool urgency parameter should have enum constraints")
		}
	}

	if confidenceProp, exists := submitDef.InputSchema.Properties["confidence"]; exists {
		if len(confidenceProp.Enum) == 0 {
			t.Error("SubmitPlanTool confidence parameter should have enum constraints")
		}
	}
}
