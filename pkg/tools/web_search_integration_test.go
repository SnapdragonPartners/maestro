//go:build integration

package tools

import (
	"context"
	"encoding/json"
	"os"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

// TestWebSearchIntegration_GoogleSearch tests the Google Custom Search integration.
// Requires GOOGLE_SEARCH_API_KEY and GOOGLE_SEARCH_CX environment variables.
func TestWebSearchIntegration_GoogleSearch(t *testing.T) {
	// Check for required environment variables
	apiKey := os.Getenv(config.EnvGoogleSearchAPIKey)
	cx := os.Getenv(config.EnvGoogleSearchCX)

	if apiKey == "" || cx == "" {
		t.Skipf("Skipping integration test: %s and %s must be set",
			config.EnvGoogleSearchAPIKey, config.EnvGoogleSearchCX)
	}

	// Verify search is detected as available
	status := config.DetectSearchAPIs()
	if !status.Available {
		t.Fatal("Expected search APIs to be available")
	}
	if status.Provider != config.SearchProviderGoogle {
		t.Fatalf("Expected provider=google, got %q", status.Provider)
	}

	// Create tool and execute search
	tool := NewWebSearchTool()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Test with a query that should return results
	result, err := tool.Exec(ctx, map[string]any{
		"query": "Go programming language official documentation",
	})
	if err != nil {
		t.Fatalf("Exec() error: %v", err)
	}

	// Parse and validate response
	var response map[string]any
	if err := json.Unmarshal([]byte(result.Content), &response); err != nil {
		t.Fatalf("Failed to parse response: %v", err)
	}

	// Verify success
	if response["success"] != true {
		t.Errorf("Expected success=true, got %v", response["success"])
	}

	// Verify provider
	if response["provider"] != "google" {
		t.Errorf("Expected provider=google, got %v", response["provider"])
	}

	// Verify we got results
	resultCount, ok := response["result_count"].(float64)
	if !ok {
		t.Fatalf("result_count not found or not a number: %v", response["result_count"])
	}
	if resultCount == 0 {
		t.Error("Expected at least one search result")
	}

	// Verify results structure
	results, ok := response["results"].([]any)
	if !ok {
		t.Fatalf("results not found or not an array: %v", response["results"])
	}
	if len(results) == 0 {
		t.Error("Expected results array to have entries")
	}

	// Check first result has required fields
	firstResult, ok := results[0].(map[string]any)
	if !ok {
		t.Fatal("First result is not a map")
	}
	if _, ok := firstResult["title"]; !ok {
		t.Error("First result missing 'title' field")
	}
	if _, ok := firstResult["url"]; !ok {
		t.Error("First result missing 'url' field")
	}
	if _, ok := firstResult["description"]; !ok {
		t.Error("First result missing 'description' field")
	}

	t.Logf("Search returned %d results", int(resultCount))
	t.Logf("First result: %s - %s", firstResult["title"], firstResult["url"])
}

// TestWebSearchIntegration_IsSearchEnabled tests the IsSearchEnabled function with real env vars.
func TestWebSearchIntegration_IsSearchEnabled(t *testing.T) {
	apiKey := os.Getenv(config.EnvGoogleSearchAPIKey)
	cx := os.Getenv(config.EnvGoogleSearchCX)

	if apiKey == "" || cx == "" {
		t.Skipf("Skipping integration test: %s and %s must be set",
			config.EnvGoogleSearchAPIKey, config.EnvGoogleSearchCX)
	}

	// Test with nil config (should auto-detect)
	if !config.IsSearchEnabled(nil) {
		t.Error("Expected IsSearchEnabled(nil) = true when env vars are set")
	}

	// Test with empty config (should auto-detect)
	cfg := &config.Config{}
	if !config.IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled(empty config) = true when env vars are set")
	}

	// Test with explicit enable
	enabled := true
	cfg = &config.Config{
		Search: &config.SearchConfig{
			Enabled: &enabled,
		},
	}
	if !config.IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled(enabled=true) = true")
	}

	// Test with explicit disable
	disabled := false
	cfg = &config.Config{
		Search: &config.SearchConfig{
			Enabled: &disabled,
		},
	}
	if config.IsSearchEnabled(cfg) {
		t.Error("Expected IsSearchEnabled(enabled=false) = false even with env vars set")
	}
}

// TestWebSearchIntegration_FilterSearchTools tests filtering tools based on config.
func TestWebSearchIntegration_FilterSearchTools(t *testing.T) {
	apiKey := os.Getenv(config.EnvGoogleSearchAPIKey)
	cx := os.Getenv(config.EnvGoogleSearchCX)

	if apiKey == "" || cx == "" {
		t.Skipf("Skipping integration test: %s and %s must be set",
			config.EnvGoogleSearchAPIKey, config.EnvGoogleSearchCX)
	}

	testTools := []string{
		ToolShell,
		ToolWebSearch,
		ToolWebFetch,
		ToolBuild,
	}

	// With search enabled (nil config = auto-detect with keys present)
	filtered := FilterSearchTools(testTools, nil)
	if len(filtered) != 4 {
		t.Errorf("Expected 4 tools when search enabled, got %d: %v", len(filtered), filtered)
	}

	// With search explicitly disabled
	disabled := false
	cfg := &config.Config{
		Search: &config.SearchConfig{
			Enabled: &disabled,
		},
	}
	filtered = FilterSearchTools(testTools, cfg)
	if len(filtered) != 2 {
		t.Errorf("Expected 2 tools when search disabled, got %d: %v", len(filtered), filtered)
	}

	// Verify search tools were removed
	for _, tool := range filtered {
		if tool == ToolWebSearch || tool == ToolWebFetch {
			t.Errorf("Search tool %s should have been filtered out", tool)
		}
	}
}
