package tools

import (
	"context"
	"strings"
	"testing"
)

func TestStoryEditTool_Name(t *testing.T) {
	tool := NewStoryEditTool()
	if tool.Name() != ToolStoryEdit {
		t.Errorf("expected name %q, got %q", ToolStoryEdit, tool.Name())
	}
}

func TestStoryEditTool_Definition(t *testing.T) {
	tool := NewStoryEditTool()
	def := tool.Definition()

	if def.Name != ToolStoryEdit {
		t.Errorf("expected definition name %q, got %q", ToolStoryEdit, def.Name)
	}
	if len(def.InputSchema.Required) != 1 || def.InputSchema.Required[0] != "implementation_notes" {
		t.Errorf("expected required param 'implementation_notes', got %v", def.InputSchema.Required)
	}
	if _, exists := def.InputSchema.Properties["implementation_notes"]; !exists {
		t.Error("expected 'implementation_notes' property in schema")
	}
}

func TestStoryEditTool_WithNotes(t *testing.T) {
	tool := NewStoryEditTool()
	result, err := tool.Exec(context.Background(), map[string]any{
		"implementation_notes": "Use per-page template sets for Go template isolation",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProcessEffect == nil {
		t.Fatal("expected ProcessEffect")
	}
	if result.ProcessEffect.Signal != SignalStoryEditComplete {
		t.Errorf("expected signal %q, got %q", SignalStoryEditComplete, result.ProcessEffect.Signal)
	}
	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any data, got: %T", result.ProcessEffect.Data)
	}
	notes, ok := data["notes"].(string)
	if !ok || notes != "Use per-page template sets for Go template isolation" {
		t.Errorf("expected notes in effect data, got: %v", data)
	}
	if !strings.Contains(result.Content, "submitted") {
		t.Errorf("expected content to mention submission, got: %s", result.Content)
	}
}

func TestStoryEditTool_EmptyNotes(t *testing.T) {
	tool := NewStoryEditTool()
	result, err := tool.Exec(context.Background(), map[string]any{
		"implementation_notes": "",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.ProcessEffect == nil {
		t.Fatal("expected ProcessEffect")
	}
	if result.ProcessEffect.Signal != SignalStoryEditComplete {
		t.Errorf("expected signal %q, got %q", SignalStoryEditComplete, result.ProcessEffect.Signal)
	}
	data, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatalf("expected map[string]any data, got: %T", result.ProcessEffect.Data)
	}
	notes, ok := data["notes"].(string)
	if !ok || notes != "" {
		t.Errorf("expected empty notes, got: %q", notes)
	}
	if !strings.Contains(result.Content, "without annotation") {
		t.Errorf("expected content to mention no annotation, got: %s", result.Content)
	}
}

func TestStoryEditTool_MissingParam(t *testing.T) {
	tool := NewStoryEditTool()
	_, err := tool.Exec(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing parameter")
	}
	if !strings.Contains(err.Error(), "implementation_notes") {
		t.Errorf("expected error to mention 'implementation_notes', got: %v", err)
	}
}

func TestStoryEditTool_Documentation(t *testing.T) {
	tool := NewStoryEditTool()
	doc := tool.PromptDocumentation()
	if !strings.Contains(doc, "story_edit") {
		t.Error("documentation should mention story_edit")
	}
	if !strings.Contains(doc, "implementation_notes") {
		t.Error("documentation should mention implementation_notes")
	}
}
