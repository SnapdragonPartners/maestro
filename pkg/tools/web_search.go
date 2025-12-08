package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"
)

// ToolWebSearch is the constant name for the web search tool.
const ToolWebSearch = "web_search"

// WebSearchTool allows agents to search the web for information.
// Useful for finding current information about APIs, libraries, versions,
// and other technical details that may be beyond the LLM's training cutoff.
type WebSearchTool struct {
	httpClient *http.Client
	maxResults int
}

// NewWebSearchTool creates a new web search tool.
func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		maxResults: 5,
	}
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

// duckDuckGoResponse represents the response from DuckDuckGo's instant answer API.
type duckDuckGoResponse struct {
	Abstract       string `json:"Abstract"`
	AbstractText   string `json:"AbstractText"`
	AbstractSource string `json:"AbstractSource"`
	AbstractURL    string `json:"AbstractURL"`
	Heading        string `json:"Heading"`
	Answer         string `json:"Answer"`
	AnswerType     string `json:"AnswerType"`
	RelatedTopics  []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"RelatedTopics"`
	Results []struct {
		Text     string `json:"Text"`
		FirstURL string `json:"FirstURL"`
	} `json:"Results"`
}

// searchResult represents a single search result.
type searchResult struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// Exec executes the web search tool.
func (t *WebSearchTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract query argument
	query, ok := args["query"].(string)
	if !ok || query == "" {
		return nil, fmt.Errorf("query is required and must be a string")
	}

	// Fetch search results from DuckDuckGo
	ddgResp, fetchErr := t.fetchDuckDuckGo(ctx, query)
	if fetchErr != nil {
		return fetchErr.result, fetchErr.err
	}

	// Build results from response
	results := t.buildResults(ddgResp)

	// Build response
	response := map[string]any{
		"success":      true,
		"query":        query,
		"result_count": len(results),
		"results":      results,
	}

	// Add note if no results found
	if len(results) == 0 {
		response["note"] = "No direct results found. Try a more specific search query or rephrase your question."
	}

	content, err := json.Marshal(response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// fetchError holds the result and error from errorResult for clean return handling.
type fetchError struct {
	result *ExecResult
	err    error
}

// fetchDuckDuckGo performs the HTTP request to DuckDuckGo's API.
func (t *WebSearchTool) fetchDuckDuckGo(ctx context.Context, query string) (*duckDuckGoResponse, *fetchError) {
	// Build DuckDuckGo instant answer URL
	// Using the instant answer API which is free and doesn't require authentication
	searchURL := fmt.Sprintf("https://api.duckduckgo.com/?q=%s&format=json&no_html=1&skip_disambig=1",
		url.QueryEscape(query))

	// Create request with context
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, http.NoBody)
	if err != nil {
		result, resErr := t.errorResult("failed to create request: " + err.Error())
		return nil, &fetchError{result, resErr}
	}

	// Set a user agent to avoid being blocked
	req.Header.Set("User-Agent", "Maestro/1.0 (AI Development Tool)")

	// Execute request
	resp, err := t.httpClient.Do(req)
	if err != nil {
		result, resErr := t.errorResult("search request failed: " + err.Error())
		return nil, &fetchError{result, resErr}
	}
	defer func() { _ = resp.Body.Close() }()

	// Read response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		result, resErr := t.errorResult("failed to read response: " + err.Error())
		return nil, &fetchError{result, resErr}
	}

	// Parse DuckDuckGo response
	var ddgResp duckDuckGoResponse
	if unmarshalErr := json.Unmarshal(body, &ddgResp); unmarshalErr != nil {
		result, resErr := t.errorResult("failed to parse search results: " + unmarshalErr.Error())
		return nil, &fetchError{result, resErr}
	}

	return &ddgResp, nil
}

// buildResults extracts search results from the DuckDuckGo response.
func (t *WebSearchTool) buildResults(ddgResp *duckDuckGoResponse) []searchResult {
	var results []searchResult

	// Add main answer if available
	if ddgResp.AbstractText != "" {
		results = append(results, searchResult{
			Title:       ddgResp.Heading,
			Description: ddgResp.AbstractText,
			URL:         ddgResp.AbstractURL,
		})
	}

	// Add instant answer if available
	if ddgResp.Answer != "" {
		results = append(results, searchResult{
			Title:       "Instant Answer",
			Description: ddgResp.Answer,
			URL:         "",
		})
	}

	// Add related topics
	for i := range ddgResp.RelatedTopics {
		topic := &ddgResp.RelatedTopics[i]
		if topic.Text != "" && len(results) < t.maxResults {
			results = append(results, searchResult{
				Title:       "",
				Description: topic.Text,
				URL:         topic.FirstURL,
			})
		}
	}

	// Add direct results
	for i := range ddgResp.Results {
		ddgResult := &ddgResp.Results[i]
		if ddgResult.Text != "" && len(results) < t.maxResults {
			results = append(results, searchResult{
				Title:       "",
				Description: ddgResult.Text,
				URL:         ddgResult.FirstURL,
			})
		}
	}

	return results
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
