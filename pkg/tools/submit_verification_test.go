package tools

import (
	"context"
	"testing"
)

func TestSubmitVerification_AllPass(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{
				"criterion": "API endpoint returns 200",
				"method":    "command",
				"result":    "pass",
				"evidence":  "curl returned 200 OK",
			},
			map[string]any{
				"criterion": "Tests cover edge cases",
				"method":    "inspection",
				"result":    "pass",
				"evidence":  "Found 5 test cases covering edge cases in test_foo.go",
			},
		},
		"confidence": "high",
		"summary":    "All criteria verified",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect")
	}
	if result.ProcessEffect.Signal != SignalVerificationPass {
		t.Errorf("Expected %s, got %s", SignalVerificationPass, result.ProcessEffect.Signal)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data")
	}
	if data["confidence"] != "high" {
		t.Errorf("Expected confidence 'high', got %v", data["confidence"])
	}

	// Verify emitted criteria are []any of map[string]any (not []map[string]string)
	criteria, ok := data["acceptance_criteria_checked"].([]any)
	if !ok {
		t.Fatal("Expected acceptance_criteria_checked to be []any")
	}
	if len(criteria) != 2 {
		t.Fatalf("Expected 2 criteria, got %d", len(criteria))
	}
	firstCriterion, ok := criteria[0].(map[string]any)
	if !ok {
		t.Fatal("Expected criterion item to be map[string]any")
	}
	if firstCriterion["criterion"] != "API endpoint returns 200" {
		t.Errorf("Expected criterion text, got %v", firstCriterion["criterion"])
	}
}

func TestSubmitVerification_OneFail(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{
				"criterion": "API endpoint returns 200",
				"method":    "command",
				"result":    "pass",
				"evidence":  "curl returned 200 OK",
			},
			map[string]any{
				"criterion": "Error handling for invalid input",
				"method":    "inspection",
				"result":    "fail",
				"evidence":  "No validation found in handler.go",
			},
		},
		"gaps":       []any{"Missing input validation"},
		"confidence": "medium",
		"summary":    "One criterion failed",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result.ProcessEffect.Signal != SignalVerificationFail {
		t.Errorf("Expected %s, got %s", SignalVerificationFail, result.ProcessEffect.Signal)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data")
	}
	gaps, ok := data["gaps"].([]any)
	if !ok || len(gaps) != 1 {
		t.Fatalf("Expected gaps []any with 1 element, got %v", data["gaps"])
	}
	if gs, ok := gaps[0].(string); !ok || gs != "Missing input validation" {
		t.Errorf("Expected gaps[0] to be 'Missing input validation', got %v", gaps[0])
	}
}

func TestSubmitVerification_MixedResults(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{"criterion": "A", "method": "command", "result": "pass", "evidence": "ok"},
			map[string]any{"criterion": "B", "method": "inspection", "result": "partial", "evidence": "half done"},
			map[string]any{"criterion": "C", "method": "command", "result": "unverified", "evidence": "needs runtime"},
		},
		"confidence": "low",
		"summary":    "Mixed results",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	// No "fail" results → should be pass signal
	if result.ProcessEffect.Signal != SignalVerificationPass {
		t.Errorf("Expected %s (no fails), got %s", SignalVerificationPass, result.ProcessEffect.Signal)
	}
}

func TestSubmitVerification_MissingSummary(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{"criterion": "A", "method": "command", "result": "pass", "evidence": "ok"},
		},
		"confidence": "high",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for missing summary")
	}
}

func TestSubmitVerification_MissingConfidence(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{"criterion": "A", "method": "command", "result": "pass", "evidence": "ok"},
		},
		"summary": "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for missing confidence")
	}
}

func TestSubmitVerification_InvalidConfidence(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{"criterion": "A", "method": "command", "result": "pass", "evidence": "ok"},
		},
		"confidence": "very_high",
		"summary":    "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid confidence")
	}
}

func TestSubmitVerification_EmptyCriteria(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{},
		"confidence":                  "high",
		"summary":                     "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for empty criteria array")
	}
}

func TestSubmitVerification_MissingCriteria(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"confidence": "high",
		"summary":    "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for missing criteria")
	}
}

func TestSubmitVerification_InvalidMethod(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{"criterion": "A", "method": "magic", "result": "pass", "evidence": "ok"},
		},
		"confidence": "high",
		"summary":    "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid method")
	}
}

func TestSubmitVerification_InvalidResult(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{"criterion": "A", "method": "command", "result": "maybe", "evidence": "ok"},
		},
		"confidence": "high",
		"summary":    "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid result enum")
	}
}

func TestSubmitVerification_MalformedCriterionObject(t *testing.T) {
	tool := NewSubmitVerificationTool()

	tests := []struct {
		name string
		item map[string]any
	}{
		{"missing criterion", map[string]any{"method": "command", "result": "pass", "evidence": "ok"}},
		{"missing method", map[string]any{"criterion": "A", "result": "pass", "evidence": "ok"}},
		{"missing result", map[string]any{"criterion": "A", "method": "command", "evidence": "ok"}},
		{"missing evidence", map[string]any{"criterion": "A", "method": "command", "result": "pass"}},
		{"empty criterion", map[string]any{"criterion": "", "method": "command", "result": "pass", "evidence": "ok"}},
		{"empty evidence", map[string]any{"criterion": "A", "method": "command", "result": "pass", "evidence": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"acceptance_criteria_checked": []any{tt.item},
				"confidence":                  "high",
				"summary":                     "done",
			}
			_, err := tool.Exec(context.Background(), args)
			if err == nil {
				t.Errorf("Expected error for %s", tt.name)
			}
		})
	}
}

func TestSubmitVerification_InvalidGaps(t *testing.T) {
	tool := NewSubmitVerificationTool()

	tests := []struct {
		name string
		gaps any
	}{
		{"non-array gaps", "not an array"},
		{"non-string gap item", []any{42}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"acceptance_criteria_checked": []any{
					map[string]any{"criterion": "A", "method": "command", "result": "pass", "evidence": "ok"},
				},
				"gaps":       tt.gaps,
				"confidence": "high",
				"summary":    "done",
			}
			_, err := tool.Exec(context.Background(), args)
			if err == nil {
				t.Errorf("Expected error for %s", tt.name)
			}
		})
	}
}

func TestSubmitVerification_NonObjectCriterion(t *testing.T) {
	tool := NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{"not an object"},
		"confidence":                  "high",
		"summary":                     "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for non-object criterion")
	}
}

func TestSubmitVerification_ToolMetadata(t *testing.T) {
	tool := NewSubmitVerificationTool()

	if tool.Name() != ToolSubmitVerification {
		t.Errorf("Expected name %q, got %q", ToolSubmitVerification, tool.Name())
	}

	def := tool.Definition()
	if def.Name != ToolSubmitVerification {
		t.Errorf("Expected definition name %q, got %q", ToolSubmitVerification, def.Name)
	}

	doc := tool.PromptDocumentation()
	if doc == "" {
		t.Error("Expected non-empty prompt documentation")
	}
}
