package tools

import (
	"context"
	"encoding/json"
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

// MockSearchProvider is a test implementation of SearchProvider.
type MockSearchProvider struct {
	name    string
	results []SearchResult
	err     error
}

func (m *MockSearchProvider) Name() string {
	return m.name
}

func (m *MockSearchProvider) Search(_ context.Context, _ string, _ int) ([]SearchResult, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.results, nil
}

func TestWebSearchTool_WithMockProvider(t *testing.T) {
	// Create a mock provider that returns known results
	mockProvider := &MockSearchProvider{
		name: "mock",
		results: []SearchResult{
			{Title: "Test Result 1", Description: "Description 1", URL: "https://example.com/1"},
			{Title: "Test Result 2", Description: "Description 2", URL: "https://example.com/2"},
		},
	}

	tool := NewWebSearchToolWithProvider(mockProvider)

	// Execute search
	result, err := tool.Exec(context.Background(), map[string]any{"query": "test query"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	// Parse response
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response fields
	if response["success"] != true {
		t.Errorf("Expected success=true, got %v", response["success"])
	}

	if response["provider"] != "mock" {
		t.Errorf("Expected provider=mock, got %v", response["provider"])
	}

	if response["query"] != "test query" {
		t.Errorf("Expected query='test query', got %v", response["query"])
	}

	resultCount, ok := response["result_count"].(float64)
	if !ok || resultCount != 2 {
		t.Errorf("Expected result_count=2, got %v", response["result_count"])
	}

	results, ok := response["results"].([]any)
	if !ok || len(results) != 2 {
		t.Errorf("Expected 2 results, got %v", response["results"])
	}
}

func TestWebSearchTool_EmptyResults(t *testing.T) {
	// Create a mock provider that returns no results
	mockProvider := &MockSearchProvider{
		name:    "mock",
		results: []SearchResult{},
	}

	tool := NewWebSearchToolWithProvider(mockProvider)

	// Execute search
	result, err := tool.Exec(context.Background(), map[string]any{"query": "nonexistent query"})
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}

	// Parse response
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify response fields
	if response["success"] != true {
		t.Errorf("Expected success=true even with no results, got %v", response["success"])
	}

	if response["note"] == nil {
		t.Error("Expected note field when no results found")
	}
}

func TestSearchProviderInterface(_ *testing.T) {
	// Test that our providers implement the interface
	var _ SearchProvider = &GoogleSearchProvider{}
	var _ SearchProvider = &DuckDuckGoProvider{}
	var _ SearchProvider = &MockSearchProvider{}
}

func TestGoogleSearchProvider_Name(t *testing.T) {
	provider := NewGoogleSearchProvider("test-key", "test-cx")
	if provider.Name() != "google" {
		t.Errorf("Name() = %q, want %q", provider.Name(), "google")
	}
}

func TestDuckDuckGoProvider_Name(t *testing.T) {
	provider := NewDuckDuckGoProvider()
	if provider.Name() != "duckduckgo" {
		t.Errorf("Name() = %q, want %q", provider.Name(), "duckduckgo")
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
