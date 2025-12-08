package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// ToolWebFetch is the constant name for the web fetch tool.
const ToolWebFetch = "web_fetch"

// WebFetchTool allows agents to fetch and read web page content.
// Use this after web_search to read the actual content of pages found in search results.
type WebFetchTool struct {
	httpClient   *http.Client
	maxBodyBytes int64
}

// NewWebFetchTool creates a new web fetch tool.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
			// Don't follow too many redirects
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return fmt.Errorf("too many redirects")
				}
				return nil
			},
		},
		maxBodyBytes: 100 * 1024, // 100KB max - enough for most docs but not huge pages
	}
}

// Name returns the tool name.
func (t *WebFetchTool) Name() string {
	return ToolWebFetch
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *WebFetchTool) PromptDocumentation() string {
	return `- **web_fetch** - Fetch and read content from a web page URL
  - Parameters: url (string, REQUIRED)
  - Use after web_search to read actual page content from search result URLs
  - Returns text content extracted from the page (HTML tags stripped)
  - Best for documentation pages, release notes, API references
  - Has a 100KB size limit to avoid huge pages`
}

// Definition returns the tool definition for LLM.
func (t *WebFetchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: ToolWebFetch,
		Description: `Fetch and read the content of a web page. Use this tool after web_search to read the actual content of pages from search results. The tool:
- Fetches the URL and extracts text content (strips HTML)
- Works well for documentation, release notes, API references
- Has a 100KB limit to avoid very large pages
- Returns the page title and main text content`,
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"url": {
					Type:        "string",
					Description: "Full URL to fetch (e.g., 'https://go.dev/doc/go1.22')",
				},
			},
			Required: []string{"url"},
		},
	}
}

// Exec executes the web fetch tool.
func (t *WebFetchTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract URL argument
	urlStr, ok := args["url"].(string)
	if !ok || urlStr == "" {
		return nil, fmt.Errorf("url is required and must be a string")
	}

	// Basic URL validation
	if !strings.HasPrefix(urlStr, "http://") && !strings.HasPrefix(urlStr, "https://") {
		return t.errorResult("URL must start with http:// or https://")
	}

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urlStr, http.NoBody)
	if err != nil {
		return t.errorResult("failed to create request: " + err.Error())
	}

	// Set headers to appear as a browser
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; Maestro/1.0; AI Development Tool)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,text/plain;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")

	// Execute request
	resp, err := t.httpClient.Do(req)
	if err != nil {
		return t.errorResult("fetch request failed: " + err.Error())
	}
	defer func() { _ = resp.Body.Close() }()

	// Check status code
	if resp.StatusCode != http.StatusOK {
		return t.errorResult(fmt.Sprintf("HTTP error: %d %s", resp.StatusCode, resp.Status))
	}

	// Check content type - we only want text-based content
	contentType := resp.Header.Get("Content-Type")
	if !isTextContent(contentType) {
		return t.errorResult(fmt.Sprintf("unsupported content type: %s (only text/html and text/plain supported)", contentType))
	}

	// Read body with size limit
	limitedReader := io.LimitReader(resp.Body, t.maxBodyBytes)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		return t.errorResult("failed to read response: " + err.Error())
	}

	// Extract text content
	content := string(body)
	title := extractTitle(content)
	text := extractText(content)

	// Truncate if still too long after processing
	const maxOutputChars = 50000 // ~50KB of processed text
	truncated := false
	if len(text) > maxOutputChars {
		text = text[:maxOutputChars]
		truncated = true
	}

	// Build response
	response := map[string]any{
		"success":   true,
		"url":       urlStr,
		"title":     title,
		"content":   text,
		"truncated": truncated,
	}

	resultContent, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(resultContent)}, nil
}

// errorResult creates an error result response.
func (t *WebFetchTool) errorResult(errMsg string) (*ExecResult, error) {
	response := map[string]any{
		"success": false,
		"error":   errMsg,
	}
	content, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal error response: %w", err)
	}
	return &ExecResult{Content: string(content)}, nil
}

// isTextContent checks if the content type is text-based.
func isTextContent(contentType string) bool {
	ct := strings.ToLower(contentType)
	return strings.Contains(ct, "text/html") ||
		strings.Contains(ct, "text/plain") ||
		strings.Contains(ct, "application/xhtml") ||
		strings.Contains(ct, "application/xml") ||
		strings.Contains(ct, "text/xml")
}

// extractTitle extracts the title from HTML content.
func extractTitle(html string) string {
	// Simple regex to extract title
	titleRegex := regexp.MustCompile(`(?i)<title[^>]*>([^<]+)</title>`)
	matches := titleRegex.FindStringSubmatch(html)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// extractText extracts readable text from HTML content.
func extractText(html string) string {
	// Remove script and style blocks first
	scriptRegex := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	html = scriptRegex.ReplaceAllString(html, "")

	styleRegex := regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`)
	html = styleRegex.ReplaceAllString(html, "")

	// Remove HTML comments
	commentRegex := regexp.MustCompile(`(?s)<!--.*?-->`)
	html = commentRegex.ReplaceAllString(html, "")

	// Replace common block elements with newlines
	blockRegex := regexp.MustCompile(`(?i)</(p|div|h[1-6]|li|tr|br|hr)[^>]*>`)
	html = blockRegex.ReplaceAllString(html, "\n")

	// Replace br tags
	brRegex := regexp.MustCompile(`(?i)<br[^>]*>`)
	html = brRegex.ReplaceAllString(html, "\n")

	// Remove all remaining HTML tags
	tagRegex := regexp.MustCompile(`<[^>]+>`)
	text := tagRegex.ReplaceAllString(html, "")

	// Decode common HTML entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = strings.ReplaceAll(text, "&apos;", "'")

	// Normalize whitespace
	// Replace multiple spaces with single space
	spaceRegex := regexp.MustCompile(`[ \t]+`)
	text = spaceRegex.ReplaceAllString(text, " ")

	// Replace multiple newlines with double newline (paragraph break)
	newlineRegex := regexp.MustCompile(`\n{3,}`)
	text = newlineRegex.ReplaceAllString(text, "\n\n")

	// Trim each line
	lines := strings.Split(text, "\n")
	var cleanLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" {
			cleanLines = append(cleanLines, trimmed)
		}
	}

	return strings.Join(cleanLines, "\n")
}
