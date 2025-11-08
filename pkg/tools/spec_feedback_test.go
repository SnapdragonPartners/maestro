package tools

import (
	"context"
	"testing"

	"orchestrator/pkg/proto"
)

func TestSpecFeedbackTool_ValidFeedback(t *testing.T) {
	tool := NewSpecFeedbackTool()
	ctx := context.Background()

	args := map[string]any{
		"feedback": "Please clarify the authentication requirements in R-001",
		"urgency":  string(proto.PriorityHigh),
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map result, got: %T", result)
	}

	success, ok := resultMap["success"].(bool)
	if !ok || !success {
		t.Errorf("Expected success=true, got: %v", resultMap)
	}

	feedback, ok := resultMap["feedback"].(string)
	if !ok || feedback != "Please clarify the authentication requirements in R-001" {
		t.Errorf("Expected feedback to match input, got: %v", feedback)
	}

	urgency, ok := resultMap["urgency"].(string)
	if !ok || urgency != string(proto.PriorityHigh) {
		t.Errorf("Expected urgency=high, got: %v", urgency)
	}
}

func TestSpecFeedbackTool_DefaultUrgency(t *testing.T) {
	tool := NewSpecFeedbackTool()
	ctx := context.Background()

	args := map[string]any{
		"feedback": "Consider adding more acceptance criteria to R-002",
	}

	result, err := tool.Exec(ctx, args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	resultMap, ok := result.(map[string]any)
	if !ok {
		t.Fatalf("Expected map result, got: %T", result)
	}

	urgency, ok := resultMap["urgency"].(string)
	if !ok || urgency != string(proto.PriorityMedium) {
		t.Errorf("Expected default urgency=medium, got: %v", urgency)
	}
}

func TestSpecFeedbackTool_MissingFeedback(t *testing.T) {
	tool := NewSpecFeedbackTool()
	ctx := context.Background()

	args := map[string]any{
		"urgency": string(proto.PriorityLow),
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for missing feedback, got nil")
	}
}

func TestSpecFeedbackTool_EmptyFeedback(t *testing.T) {
	tool := NewSpecFeedbackTool()
	ctx := context.Background()

	args := map[string]any{
		"feedback": "",
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for empty feedback, got nil")
	}
}

func TestSpecFeedbackTool_InvalidUrgency(t *testing.T) {
	tool := NewSpecFeedbackTool()
	ctx := context.Background()

	args := map[string]any{
		"feedback": "Test feedback",
		"urgency":  "critical",
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for invalid urgency, got nil")
	}
}

func TestSpecFeedbackTool_InvalidFeedbackType(t *testing.T) {
	tool := NewSpecFeedbackTool()
	ctx := context.Background()

	args := map[string]any{
		"feedback": 123, // Should be string
	}

	_, err := tool.Exec(ctx, args)
	if err == nil {
		t.Error("Expected error for non-string feedback, got nil")
	}
}

func TestSpecFeedbackTool_Definition(t *testing.T) {
	tool := NewSpecFeedbackTool()
	def := tool.Definition()

	if def.Name != "spec_feedback" {
		t.Errorf("Expected name 'spec_feedback', got: %s", def.Name)
	}

	if def.Description == "" {
		t.Error("Expected non-empty description")
	}

	if len(def.InputSchema.Required) != 1 {
		t.Errorf("Expected 1 required field, got: %d", len(def.InputSchema.Required))
	}

	if def.InputSchema.Required[0] != "feedback" {
		t.Errorf("Expected required field 'feedback', got: %s", def.InputSchema.Required[0])
	}
}
