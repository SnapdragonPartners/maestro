package tools

import (
	"context"
	"strings"
	"sync"
	"testing"
)

// mockMaintenanceLog is a test double that records items.
type mockMaintenanceLog struct {
	mu    sync.Mutex
	items []MaintenanceItem
}

func (m *mockMaintenanceLog) AddMaintenanceItem(item MaintenanceItem) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, item)
}

func TestAddMaintenanceItemTool_Name(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	if tool.Name() != ToolAddMaintenanceItem {
		t.Errorf("expected name %q, got %q", ToolAddMaintenanceItem, tool.Name())
	}
}

func TestAddMaintenanceItemTool_Definition(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	def := tool.Definition()

	if def.Name != ToolAddMaintenanceItem {
		t.Errorf("expected definition name %q, got %q", ToolAddMaintenanceItem, def.Name)
	}
	if len(def.InputSchema.Required) != 2 {
		t.Errorf("expected 2 required params, got %d", len(def.InputSchema.Required))
	}
	if _, exists := def.InputSchema.Properties["description"]; !exists {
		t.Error("expected 'description' property in schema")
	}
	if _, exists := def.InputSchema.Properties["priority"]; !exists {
		t.Error("expected 'priority' property in schema")
	}
	// Verify priority enum values
	priorityProp := def.InputSchema.Properties["priority"]
	if len(priorityProp.Enum) != 3 {
		t.Errorf("expected 3 priority enum values, got %d", len(priorityProp.Enum))
	}
}

func TestAddMaintenanceItemTool_Success(t *testing.T) {
	log := &mockMaintenanceLog{}
	tool := NewAddMaintenanceItemTool(log, "coder-001", "story-abc")

	result, err := tool.Exec(context.Background(), map[string]any{
		"description": "Missing .gitignore for Go binaries",
		"priority":    "p2",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should be non-terminal (no ProcessEffect)
	if result.ProcessEffect != nil {
		t.Error("expected nil ProcessEffect (non-terminal tool)")
	}

	// Should return confirmation message
	if !strings.Contains(result.Content, "p2") {
		t.Errorf("expected content to mention priority, got: %s", result.Content)
	}

	// Verify item was logged
	if len(log.items) != 1 {
		t.Fatalf("expected 1 item logged, got %d", len(log.items))
	}
	item := log.items[0]
	if item.Description != "Missing .gitignore for Go binaries" {
		t.Errorf("wrong description: %s", item.Description)
	}
	if item.Priority != "p2" {
		t.Errorf("wrong priority: %s", item.Priority)
	}
	if item.Source != "coder-001:story-abc" {
		t.Errorf("wrong source: %s", item.Source)
	}
	if item.AddedAt.IsZero() {
		t.Error("expected non-zero AddedAt")
	}
}

func TestAddMaintenanceItemTool_SourceWithoutStory(t *testing.T) {
	log := &mockMaintenanceLog{}
	tool := NewAddMaintenanceItemTool(log, "coder-002", "")

	_, err := tool.Exec(context.Background(), map[string]any{
		"description": "Outdated dependency",
		"priority":    "p1",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(log.items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(log.items))
	}
	// Without story ID, source is just the agent ID
	if log.items[0].Source != "coder-002" {
		t.Errorf("expected source 'coder-002', got %q", log.items[0].Source)
	}
}

func TestAddMaintenanceItemTool_NilLog(t *testing.T) {
	// Should not panic with nil log, but should indicate item was not persisted
	tool := NewAddMaintenanceItemTool(nil, "test", "")
	result, err := tool.Exec(context.Background(), map[string]any{
		"description": "Test item",
		"priority":    "p3",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !strings.Contains(result.Content, "not persisted") {
		t.Errorf("expected content to indicate item was not persisted, got: %s", result.Content)
	}
}

func TestAddMaintenanceItemTool_InvalidPriority(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	_, err := tool.Exec(context.Background(), map[string]any{
		"description": "Some issue",
		"priority":    "critical",
	})
	if err == nil {
		t.Fatal("expected error for invalid priority")
	}
	if !strings.Contains(err.Error(), "p1, p2, p3") {
		t.Errorf("expected error to list valid priorities, got: %v", err)
	}
}

func TestAddMaintenanceItemTool_MissingDescription(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	_, err := tool.Exec(context.Background(), map[string]any{
		"priority": "p1",
	})
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestAddMaintenanceItemTool_MissingPriority(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	_, err := tool.Exec(context.Background(), map[string]any{
		"description": "Some issue",
	})
	if err == nil {
		t.Fatal("expected error for missing priority")
	}
}

func TestAddMaintenanceItemTool_EmptyDescription(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	_, err := tool.Exec(context.Background(), map[string]any{
		"description": "",
		"priority":    "p1",
	})
	if err == nil {
		t.Fatal("expected error for empty description")
	}
}

func TestAddMaintenanceItemTool_Documentation(t *testing.T) {
	tool := NewAddMaintenanceItemTool(nil, "", "")
	doc := tool.PromptDocumentation()
	if !strings.Contains(doc, "add_maintenance_item") {
		t.Error("documentation should mention add_maintenance_item")
	}
	if !strings.Contains(doc, "description") {
		t.Error("documentation should mention description parameter")
	}
	if !strings.Contains(doc, "priority") {
		t.Error("documentation should mention priority parameter")
	}
	if !strings.Contains(doc, "Non-terminal") {
		t.Error("documentation should mention non-terminal behavior")
	}
}
