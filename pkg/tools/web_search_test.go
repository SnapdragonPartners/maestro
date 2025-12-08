package tools

import (
	"context"
	"testing"
)

func TestWebSearchTool_Definition(t *testing.T) {
	tool := NewWebSearchTool()

	if tool.Name() != ToolWebSearch {
		t.Errorf("Name() = %q, want %q", tool.Name(), ToolWebSearch)
	}

	def := tool.Definition()
	if def.Name != ToolWebSearch {
		t.Errorf("Definition().Name = %q, want %q", def.Name, ToolWebSearch)
	}

	// Check that query parameter is required
	if len(def.InputSchema.Required) != 1 || def.InputSchema.Required[0] != "query" {
		t.Errorf("Expected 'query' to be required, got: %v", def.InputSchema.Required)
	}

	// Check that query property exists
	if _, ok := def.InputSchema.Properties["query"]; !ok {
		t.Error("Expected 'query' property in input schema")
	}
}

func TestWebSearchTool_PromptDocumentation(t *testing.T) {
	tool := NewWebSearchTool()

	doc := tool.PromptDocumentation()
	if doc == "" {
		t.Error("PromptDocumentation() should not be empty")
	}

	// Check for key information
	if !containsString(doc, "web_search") {
		t.Error("PromptDocumentation should mention 'web_search'")
	}

	if !containsString(doc, "query") {
		t.Error("PromptDocumentation should mention 'query' parameter")
	}
}

func TestWebSearchTool_Exec_MissingQuery(t *testing.T) {
	tool := NewWebSearchTool()

	// Test with missing query
	_, err := tool.Exec(context.Background(), map[string]any{})
	if err == nil {
		t.Error("Expected error for missing query parameter")
	}

	// Test with empty query
	_, err = tool.Exec(context.Background(), map[string]any{"query": ""})
	if err == nil {
		t.Error("Expected error for empty query parameter")
	}

	// Test with wrong type
	_, err = tool.Exec(context.Background(), map[string]any{"query": 123})
	if err == nil {
		t.Error("Expected error for non-string query parameter")
	}
}

// containsString checks if substr is in str.
func containsString(str, substr string) bool {
	return len(str) >= len(substr) && (str == substr ||
		(len(str) > len(substr) && containsSubstring(str, substr)))
}

func containsSubstring(str, substr string) bool {
	for i := 0; i <= len(str)-len(substr); i++ {
		if str[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
