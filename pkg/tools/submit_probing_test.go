package tools

import (
	"context"
	"testing"
)

func TestSubmitProbing_AllAdvisory(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "Missing nil check on optional field",
				"method":      "inspection",
				"result":      "issue_found",
				"severity":    "advisory",
				"evidence":    "handler.go:42 does not check for nil",
			},
			map[string]any{
				"category":    "boundary_values",
				"description": "Empty string input handled correctly",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "grep shows validation at line 10",
			},
		},
		"summary": "Minor advisory findings only",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result.ProcessEffect == nil {
		t.Fatal("Expected ProcessEffect")
	}
	if result.ProcessEffect.Signal != SignalProbingPass {
		t.Errorf("Expected %s, got %s", SignalProbingPass, result.ProcessEffect.Signal)
	}

	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data")
	}

	// Verify emitted findings are []any of map[string]any (not []map[string]string)
	findings, ok := data["findings"].([]any)
	if !ok {
		t.Fatal("Expected findings to be []any")
	}
	if len(findings) != 2 {
		t.Fatalf("Expected 2 findings, got %d", len(findings))
	}
	firstFinding, ok := findings[0].(map[string]any)
	if !ok {
		t.Fatal("Expected finding item to be map[string]any")
	}
	if firstFinding["category"] != "error_handling" {
		t.Errorf("Expected category 'error_handling', got %v", firstFinding["category"])
	}
}

func TestSubmitProbing_OneCritical(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "security",
				"description": "SQL injection in user input",
				"method":      "inspection",
				"result":      "issue_found",
				"severity":    "critical",
				"evidence":    "query.go:15 concatenates user input into SQL",
			},
			map[string]any{
				"category":    "error_handling",
				"description": "Graceful error messages",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "All errors wrapped with context",
			},
		},
		"summary": "Critical SQL injection found",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	if result.ProcessEffect.Signal != SignalProbingFail {
		t.Errorf("Expected %s, got %s", SignalProbingFail, result.ProcessEffect.Signal)
	}
}

func TestSubmitProbing_CriticalButNoIssue(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "security",
				"description": "Checked for SQL injection",
				"method":      "inspection",
				"result":      "no_issue",
				"severity":    "critical",
				"evidence":    "All queries use parameterized statements",
			},
		},
		"summary": "No issues found",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
	// critical severity but no_issue result → pass
	if result.ProcessEffect.Signal != SignalProbingPass {
		t.Errorf("Expected %s (no issue_found), got %s", SignalProbingPass, result.ProcessEffect.Signal)
	}
}

func TestSubmitProbing_MissingSummary(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "ok",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "ok",
			},
		},
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for missing summary")
	}
}

func TestSubmitProbing_EmptyFindings(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{},
		"summary":  "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for empty findings array")
	}
}

func TestSubmitProbing_MissingFindings(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"summary": "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for missing findings")
	}
}

func TestSubmitProbing_InvalidCategory(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "magic",
				"description": "test",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "ok",
			},
		},
		"summary": "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid category")
	}
}

func TestSubmitProbing_InvalidSeverity(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "test",
				"method":      "command",
				"result":      "no_issue",
				"severity":    "high",
				"evidence":    "ok",
			},
		},
		"summary": "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid severity")
	}
}

func TestSubmitProbing_InvalidResult(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "test",
				"method":      "command",
				"result":      "maybe",
				"severity":    "advisory",
				"evidence":    "ok",
			},
		},
		"summary": "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid result enum")
	}
}

func TestSubmitProbing_InvalidMethod(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{
			map[string]any{
				"category":    "error_handling",
				"description": "test",
				"method":      "magic",
				"result":      "no_issue",
				"severity":    "advisory",
				"evidence":    "ok",
			},
		},
		"summary": "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for invalid method")
	}
}

func TestSubmitProbing_MalformedFindingObject(t *testing.T) {
	tool := NewSubmitProbingTool()

	tests := []struct {
		name string
		item map[string]any
	}{
		{"missing category", map[string]any{"description": "d", "method": "command", "result": "no_issue", "severity": "advisory", "evidence": "ok"}},
		{"missing description", map[string]any{"category": "error_handling", "method": "command", "result": "no_issue", "severity": "advisory", "evidence": "ok"}},
		{"missing method", map[string]any{"category": "error_handling", "description": "d", "result": "no_issue", "severity": "advisory", "evidence": "ok"}},
		{"missing result", map[string]any{"category": "error_handling", "description": "d", "method": "command", "severity": "advisory", "evidence": "ok"}},
		{"missing severity", map[string]any{"category": "error_handling", "description": "d", "method": "command", "result": "no_issue", "evidence": "ok"}},
		{"missing evidence", map[string]any{"category": "error_handling", "description": "d", "method": "command", "result": "no_issue", "severity": "advisory"}},
		{"empty description", map[string]any{"category": "error_handling", "description": "", "method": "command", "result": "no_issue", "severity": "advisory", "evidence": "ok"}},
		{"empty evidence", map[string]any{"category": "error_handling", "description": "d", "method": "command", "result": "no_issue", "severity": "advisory", "evidence": ""}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"findings": []any{tt.item},
				"summary":  "done",
			}
			_, err := tool.Exec(context.Background(), args)
			if err == nil {
				t.Errorf("Expected error for %s", tt.name)
			}
		})
	}
}

func TestSubmitProbing_NonObjectFinding(t *testing.T) {
	tool := NewSubmitProbingTool()

	args := map[string]any{
		"findings": []any{"not an object"},
		"summary":  "done",
	}

	_, err := tool.Exec(context.Background(), args)
	if err == nil {
		t.Fatal("Expected error for non-object finding")
	}
}

func TestSubmitProbing_ToolMetadata(t *testing.T) {
	tool := NewSubmitProbingTool()

	if tool.Name() != ToolSubmitProbing {
		t.Errorf("Expected name %q, got %q", ToolSubmitProbing, tool.Name())
	}

	def := tool.Definition()
	if def.Name != ToolSubmitProbing {
		t.Errorf("Expected definition name %q, got %q", ToolSubmitProbing, def.Name)
	}

	doc := tool.PromptDocumentation()
	if doc == "" {
		t.Error("Expected non-empty prompt documentation")
	}
}
