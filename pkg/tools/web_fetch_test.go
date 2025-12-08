package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWebFetchTool_Definition(t *testing.T) {
	tool := NewWebFetchTool()

	if tool.Name() != ToolWebFetch {
		t.Errorf("Name() = %q, want %q", tool.Name(), ToolWebFetch)
	}

	def := tool.Definition()
	if def.Name != ToolWebFetch {
		t.Errorf("Definition().Name = %q, want %q", def.Name, ToolWebFetch)
	}

	// Check that url parameter is required
	if len(def.InputSchema.Required) != 1 || def.InputSchema.Required[0] != "url" {
		t.Errorf("Expected 'url' to be required, got: %v", def.InputSchema.Required)
	}

	// Check that url property exists
	if _, ok := def.InputSchema.Properties["url"]; !ok {
		t.Error("Expected 'url' property in input schema")
	}
}

func TestWebFetchTool_PromptDocumentation(t *testing.T) {
	tool := NewWebFetchTool()

	doc := tool.PromptDocumentation()
	if doc == "" {
		t.Error("PromptDocumentation() should not be empty")
	}

	// Check for key information
	if !containsString(doc, "web_fetch") {
		t.Error("PromptDocumentation should mention 'web_fetch'")
	}

	if !containsString(doc, "url") {
		t.Error("PromptDocumentation should mention 'url' parameter")
	}
}

func TestWebFetchTool_Exec_MissingURL(t *testing.T) {
	tool := NewWebFetchTool()

	// Test with missing url
	_, err := tool.Exec(context.Background(), map[string]any{})
	if err == nil {
		t.Error("Expected error for missing url parameter")
	}

	// Test with empty url
	_, err = tool.Exec(context.Background(), map[string]any{"url": ""})
	if err == nil {
		t.Error("Expected error for empty url parameter")
	}

	// Test with wrong type
	_, err = tool.Exec(context.Background(), map[string]any{"url": 123})
	if err == nil {
		t.Error("Expected error for non-string url parameter")
	}
}

func TestWebFetchTool_Exec_InvalidURL(t *testing.T) {
	tool := NewWebFetchTool()

	// Test with invalid URL (no scheme)
	result, err := tool.Exec(context.Background(), map[string]any{"url": "example.com"})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should return error result, not Go error
	if !containsString(result.Content, "URL must start with http://") {
		t.Errorf("Expected URL validation error, got: %s", result.Content)
	}
}

func TestWebFetchTool_Exec_Success(t *testing.T) {
	// Create a test server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`<!DOCTYPE html>
<html>
<head><title>Test Page</title></head>
<body>
<h1>Hello World</h1>
<p>This is a test paragraph.</p>
<script>console.log('should be removed');</script>
<style>.hidden { display: none; }</style>
</body>
</html>`))
	}))
	defer server.Close()

	tool := NewWebFetchTool()

	result, err := tool.Exec(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Check for success
	if !containsString(result.Content, `"success":true`) {
		t.Errorf("Expected success:true, got: %s", result.Content)
	}

	// Check title extraction
	if !containsString(result.Content, "Test Page") {
		t.Errorf("Expected title 'Test Page' in result, got: %s", result.Content)
	}

	// Check content extraction
	if !containsString(result.Content, "Hello World") {
		t.Errorf("Expected 'Hello World' in content, got: %s", result.Content)
	}

	// Verify script content was removed
	if containsString(result.Content, "should be removed") {
		t.Errorf("Script content should be removed, got: %s", result.Content)
	}
}

func TestWebFetchTool_Exec_HTTPError(t *testing.T) {
	// Create a test server that returns 404
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tool := NewWebFetchTool()

	result, err := tool.Exec(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should return error result
	if !containsString(result.Content, `"success":false`) {
		t.Errorf("Expected success:false for HTTP error, got: %s", result.Content)
	}

	if !containsString(result.Content, "404") {
		t.Errorf("Expected 404 error in result, got: %s", result.Content)
	}
}

func TestWebFetchTool_Exec_UnsupportedContentType(t *testing.T) {
	// Create a test server that returns binary content
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte{0x00, 0x01, 0x02})
	}))
	defer server.Close()

	tool := NewWebFetchTool()

	result, err := tool.Exec(context.Background(), map[string]any{"url": server.URL})
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should return error result for unsupported content type
	if !containsString(result.Content, `"success":false`) {
		t.Errorf("Expected success:false for unsupported content type, got: %s", result.Content)
	}

	if !containsString(result.Content, "unsupported content type") {
		t.Errorf("Expected unsupported content type error, got: %s", result.Content)
	}
}

func TestExtractTitle(t *testing.T) {
	tests := []struct {
		name     string
		html     string
		expected string
	}{
		{
			name:     "simple title",
			html:     "<html><head><title>Test Title</title></head></html>",
			expected: "Test Title",
		},
		{
			name:     "title with whitespace",
			html:     "<html><head><title>  Spaced Title  </title></head></html>",
			expected: "Spaced Title",
		},
		{
			name:     "no title",
			html:     "<html><head></head></html>",
			expected: "",
		},
		{
			name:     "title with attributes",
			html:     `<html><head><title lang="en">English Title</title></head></html>`,
			expected: "English Title",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractTitle(tt.html)
			if result != tt.expected {
				t.Errorf("extractTitle() = %q, want %q", result, tt.expected)
			}
		})
	}
}

func TestExtractText(t *testing.T) {
	tests := []struct {
		name         string
		html         string
		shouldHave   []string
		shouldntHave []string
	}{
		{
			name:         "removes script tags",
			html:         "<p>Keep this</p><script>remove this</script>",
			shouldHave:   []string{"Keep this"},
			shouldntHave: []string{"remove this"},
		},
		{
			name:         "removes style tags",
			html:         "<p>Keep this</p><style>.hidden { display: none; }</style>",
			shouldHave:   []string{"Keep this"},
			shouldntHave: []string{"display", "hidden"},
		},
		{
			name:         "removes HTML comments",
			html:         "<p>Keep this</p><!-- remove this comment -->",
			shouldHave:   []string{"Keep this"},
			shouldntHave: []string{"remove this comment"},
		},
		{
			name:       "decodes HTML entities",
			html:       "<p>&amp; &lt; &gt; &quot; &#39;</p>",
			shouldHave: []string{"&", "<", ">", "\"", "'"},
		},
		{
			name:       "handles block elements",
			html:       "<p>Paragraph 1</p><p>Paragraph 2</p>",
			shouldHave: []string{"Paragraph 1", "Paragraph 2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractText(tt.html)

			for _, want := range tt.shouldHave {
				if !containsString(result, want) {
					t.Errorf("extractText() should contain %q, got: %s", want, result)
				}
			}

			for _, unwanted := range tt.shouldntHave {
				if containsString(result, unwanted) {
					t.Errorf("extractText() should NOT contain %q, got: %s", unwanted, result)
				}
			}
		})
	}
}

func TestIsTextContent(t *testing.T) {
	tests := []struct {
		contentType string
		expected    bool
	}{
		{"text/html", true},
		{"text/html; charset=utf-8", true},
		{"TEXT/HTML", true},
		{"text/plain", true},
		{"application/xhtml+xml", true},
		{"application/xml", true},
		{"text/xml", true},
		{"application/json", false},
		{"application/octet-stream", false},
		{"image/png", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.contentType, func(t *testing.T) {
			result := isTextContent(tt.contentType)
			if result != tt.expected {
				t.Errorf("isTextContent(%q) = %v, want %v", tt.contentType, result, tt.expected)
			}
		})
	}
}
