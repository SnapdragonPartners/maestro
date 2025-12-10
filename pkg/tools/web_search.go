package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"orchestrator/pkg/config"
)

// ToolWebSearch is the constant name for the web search tool.
const ToolWebSearch = "web_search"

// SearchResult represents a single search result from any provider.
type SearchResult struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// SearchProvider defines the interface for web search backends.
// Implementations can use Google Custom Search, Brave Search, Bing, etc.
type SearchProvider interface {
	// Name returns a human-readable name for the provider.
	Name() string
	// Search performs a web search and returns results.
	Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error)
}

// WebSearchTool allows agents to search the web for information.
// Useful for finding current information about APIs, libraries, versions,
// and other technical details that may be beyond the LLM's training cutoff.
type WebSearchTool struct {
	provider   SearchProvider
	maxResults int
}

// NewWebSearchTool creates a new web search tool.
// It automatically selects the best available provider based on environment configuration.
func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		provider:   selectProvider(),
		maxResults: 5,
	}
}

// NewWebSearchToolWithProvider creates a web search tool with a specific provider.
// Useful for testing or when you want to override the default provider selection.
func NewWebSearchToolWithProvider(provider SearchProvider) *WebSearchTool {
	return &WebSearchTool{
		provider:   provider,
		maxResults: 5,
	}
}

// selectProvider chooses the best available search provider.
// Priority: Google Custom Search > DuckDuckGo (fallback).
func selectProvider() SearchProvider {
	// Use config package to detect available search APIs
	status := config.DetectSearchAPIs()
	if status.Available && status.Provider == config.SearchProviderGoogle {
		return NewGoogleSearchProvider(status.GoogleAPIKey, status.GoogleCX)
	}

	// Fall back to DuckDuckGo (limited functionality)
	return NewDuckDuckGoProvider()
}

// Name returns the tool name.
func (t *WebSearchTool) Name() string {
	return ToolWebSearch
}

// PromptDocumentation returns formatted tool documentation for prompts.
func (t *WebSearchTool) PromptDocumentation() string {
	return `- **web_search** - Search the web for current information
  - Parameters: query (string, REQUIRED)
  - Use to find current documentation, API references, library versions, or other technical information
  - Useful when you need information that may be beyond your training cutoff (e.g., new Go releases, latest library versions)
  - Returns structured search results with titles, descriptions, and URLs`
}

// Definition returns the tool definition for LLM.
func (t *WebSearchTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name: ToolWebSearch,
		Description: `Search the web for current technical information. Use this tool when:
- You need to verify current versions of programming languages, libraries, or frameworks
- You need to look up current API documentation or specifications
- You encounter errors related to deprecated or changed functionality
- You need information about recent releases or changes that may be beyond your training data
Returns search results with titles, descriptions, and URLs for further research.`,
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"query": {
					Type:        "string",
					Description: "Search query string (e.g., 'Go 1.24 release notes', 'Python 3.12 new features')",
				},
			},
			Required: []string{"query"},
		},
	}
}

// Exec executes the web search tool.
func (t *WebSearchTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract query argument
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required and must be a string")
	}

	// Perform search using the configured provider
	results, err := t.provider.Search(ctx, query, t.maxResults)
	if err != nil {
		return t.errorResult(fmt.Sprintf("search failed: %v", err))
	}

	// Build response
	response := map[string]any{
		"success":      true,
		"query":        query,
		"provider":     t.provider.Name(),
		"result_count": len(results),
		"results":      results,
	}

	// Add note if no results found
	if len(results) == 0 {
		response["note"] = "No results found. Try a different search query or rephrase your question."
	}

	content, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// errorResult creates an error result response.
func (t *WebSearchTool) errorResult(errMsg string) (*ExecResult, error) {
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

// =============================================================================
// Google Custom Search Provider
// =============================================================================

// GoogleSearchProvider implements SearchProvider using Google Custom Search API.
type GoogleSearchProvider struct {
	httpClient *http.Client
	apiKey     string
	cx         string
}

// NewGoogleSearchProvider creates a new Google Custom Search provider.
func NewGoogleSearchProvider(apiKey, cx string) *GoogleSearchProvider {
	return &GoogleSearchProvider{
		apiKey: apiKey,
		cx:     cx,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Name returns the provider name.
func (p *GoogleSearchProvider) Name() string {
	return "google"
}

// googleSearchItem represents a single item in the Google Custom Search response.
type googleSearchItem struct {
	Title   string `json:"title"`
	Link    string `json:"link"`
	Snippet string `json:"snippet"`
}

// googleSearchError represents an error response from Google Custom Search API.
type googleSearchError struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

// googleSearchResponse represents the response from Google Custom Search API.
type googleSearchResponse struct {
	Error *googleSearchError `json:"error"`
	Items []googleSearchItem `json:"items"`
}

// Search performs a web search using Google Custom Search API.
func (p *GoogleSearchProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	// Build Google Custom Search URL
	// API docs: https://developers.google.com/custom-search/v1/reference/rest/v1/cse/list
	searchURL := fmt.Sprintf(
		"https://www.googleapis.com/customsearch/v1?key=%s&cx=%s&q=%s&num=%d",
		url.QueryEscape(p.apiKey),
		url.QueryEscape(p.cx),
		url.QueryEscape(query),
		maxResults,
	)

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse response
	var googleResp googleSearchResponse
	if unmarshalErr := json.Unmarshal(body, &googleResp); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse response: %w", unmarshalErr)
	}

	// Check for API error
	if googleResp.Error != nil {
		return nil, fmt.Errorf("API error %d: %s", googleResp.Error.Code, googleResp.Error.Message)
	}

	// Convert to SearchResult format
	results := make([]SearchResult, 0, len(googleResp.Items))
	for i := range googleResp.Items {
		item := &googleResp.Items[i]
		results = append(results, SearchResult{
			Title:       item.Title,
			Description: item.Snippet,
			URL:         item.Link,
		})
	}

	return results, nil
}

// =============================================================================
// DuckDuckGo Provider (Fallback)
// =============================================================================

// DuckDuckGoProvider implements SearchProvider using DuckDuckGo's Instant Answer API.
// NOTE: This is a fallback provider with limited functionality. It only returns
// encyclopedic/instant answers, not general web search results.
type DuckDuckGoProvider struct {
	httpClient *http.Client
}

// NewDuckDuckGoProvider creates a new DuckDuckGo provider.
func NewDuckDuckGoProvider() *DuckDuckGoProvider {
	return &DuckDuckGoProvider{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// Name returns the provider name.
func (p *DuckDuckGoProvider) Name() string {
	return "duckduckgo"
}

// duckDuckGoResponse represents the response from DuckDuckGo's instant answer API.
type duckDuckGoResponse struct {
	Abstract      string `json:"Abstract"`
	AbstractText  string `json:"AbstractText"`
	AbstractURL   string `json:"AbstractURL"`
	Heading       string `json:"Heading"`
	Answer        string `json:"Answer"`
	RelatedTopics []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"RelatedTopics"`
	Results []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"Results"`
}

// Search performs a search using DuckDuckGo's Instant Answer API.
// NOTE: This API is limited to encyclopedic/instant answers and may not return
// results for general web search queries.
func (p *DuckDuckGoProvider) Search(ctx context.Context, query string, maxResults int) ([]SearchResult, error) {
	// Build DuckDuckGo instant answer URL
	searchURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query))

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, http.NoBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// Set a user agent to avoid being blocked
	req.Header.Set("User-Agent", "Maestro/1.0 (AI Development Tool)")

	// Execute request
	resp, err := p.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	// Parse DuckDuckGo response
	var ddgResp duckDuckGoResponse
	if unmarshalErr := json.Unmarshal(body, &ddgResp); unmarshalErr != nil {
		return nil, fmt.Errorf("failed to parse response: %w", unmarshalErr)
	}

	// Build results from response
	var results []SearchResult

	// Add main answer if available
	if ddgResp.AbstractText != "" {
		results = append(results, SearchResult{
			Title:       ddgResp.Heading,
			Description: ddgResp.AbstractText,
			URL:         ddgResp.AbstractURL,
		})
	}

	// Add instant answer if available
	if ddgResp.Answer != "" {
		results = append(results, SearchResult{
			Title:       "Instant Answer",
			Description: ddgResp.Answer,
			URL:         "",
		})
	}

	// Add related topics
	for i := range ddgResp.RelatedTopics {
		topic := &ddgResp.RelatedTopics[i]
		if topic.Text != "" && len(results) < maxResults {
			results = append(results, SearchResult{
				Title:       "",
				Description: topic.Text,
				URL:         topic.FirstURL,
			})
		}
	}

	// Add direct results
	for i := range ddgResp.Results {
		ddgResult := &ddgResp.Results[i]
		if ddgResult.Text != "" && len(results) < maxResults {
			results = append(results, SearchResult{
				Title:       "",
				Description: ddgResult.Text,
				URL:         ddgResult.FirstURL,
			})
		}
	}

	return results, nil
}
