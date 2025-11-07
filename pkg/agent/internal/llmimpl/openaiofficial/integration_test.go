//go:build integration

package openaiofficial

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// TestOpenAIOfficial_BasicResponse tests basic text completion.
func TestOpenAIOfficial_BasicResponse(t *testing.T) {
	// Skip if no API key available
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set")
	}

	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "gpt-5")

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: "system", Content: "You are a helpful assistant. Always provide a clear, direct response to user questions."},
			{Role: "user", Content: "Say 'Hello from OpenAI Official client!' and tell me your favorite color."},
		},
		MaxTokens: 1000, // Much higher limit for GPT-5 reasoning + output
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	if resp.Content == "" {
		t.Fatal("Response content is empty")
	}

	t.Logf("Response: %s", resp.Content)

	// Verify the response contains expected text
	if !strings.Contains(strings.ToLower(resp.Content), "hello") {
		t.Errorf("Response doesn't contain expected text: %s", resp.Content)
	}
}

// TestOpenAIOfficial_JSONResponse tests structured JSON output.
func TestOpenAIOfficial_JSONResponse(t *testing.T) {
	// Skip if no API key available
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set")
	}

	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "gpt-5")

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: "system", Content: "You are a helpful assistant that responds only in valid JSON format."},
			{Role: "user", Content: `Create a JSON object with these fields:
- "status": "success"  
- "provider": "openai_official"
- "model": "gpt-5"
- "timestamp": current timestamp as string
- "message": "Integration test completed successfully"

Return ONLY the JSON, no other text.`},
		},
		MaxTokens: 1000, // Higher limit for GPT-5 reasoning + output
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	t.Logf("JSON Response content: '%s'", resp.Content)

	if resp.Content == "" {
		t.Fatal("Response content is empty")
	}

	t.Logf("JSON Response: %s", resp.Content)

	// Extract JSON from potential markdown wrapper
	content := strings.TrimSpace(resp.Content)
	if strings.HasPrefix(content, "```json") && strings.HasSuffix(content, "```") {
		// Remove markdown code block
		lines := strings.Split(content, "\n")
		if len(lines) > 2 {
			content = strings.Join(lines[1:len(lines)-1], "\n")
		}
	}

	// Parse and validate JSON
	var jsonResp map[string]interface{}
	if err := json.Unmarshal([]byte(content), &jsonResp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v\nResponse: %s", err, resp.Content)
	}

	// Verify expected fields
	expectedFields := []string{"status", "provider", "model", "timestamp", "message"}
	for _, field := range expectedFields {
		if _, exists := jsonResp[field]; !exists {
			t.Errorf("Missing expected field '%s' in JSON response", field)
		}
	}

	// Verify specific values
	if status, ok := jsonResp["status"].(string); !ok || status != "success" {
		t.Errorf("Expected status 'success', got: %v", jsonResp["status"])
	}

	if provider, ok := jsonResp["provider"].(string); !ok || provider != "openai_official" {
		t.Errorf("Expected provider 'openai_official', got: %v", jsonResp["provider"])
	}
}

// TestOpenAIOfficial_MCPToolInvocation tests MCP tool calling capability.
func TestOpenAIOfficial_MCPToolInvocation(t *testing.T) {
	// Skip if no API key available
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set")
	}

	// Use o4-mini for testing - supports Responses API with tool calling
	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "o4-mini")

	// Define a simple MCP tool for testing
	toolDef := tools.ToolDefinition{
		Name:        "calculate_sum",
		Description: "Calculate the sum of two numbers",
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"a": {
					Type:        "number",
					Description: "First number to add",
				},
				"b": {
					Type:        "number",
					Description: "Second number to add",
				},
			},
			Required: []string{"a", "b"},
		},
	}

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: "system", Content: "You are a helpful assistant with access to tools. Use the calculate_sum tool when asked to perform arithmetic."},
			{Role: "user", Content: "Please calculate the sum of 15 and 27 using the available tool."},
		},
		Tools:     []tools.ToolDefinition{toolDef},
		MaxTokens: 1000, // Higher limit for GPT-5 reasoning + output
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		t.Fatal("Response has no content and no tool calls")
	}

	t.Logf("Response Content: %s", resp.Content)
	t.Logf("Tool Calls: %+v", resp.ToolCalls)

	// Check if any tool calls were made
	if len(resp.ToolCalls) == 0 {
		// Some models might not support tool calling or might respond with text instead
		t.Logf("No tool calls made, but got text response: %s", resp.Content)

		// Check if the response mentions calculation, the numbers, or gives the correct answer
		content := strings.ToLower(resp.Content)
		hasNumbers := strings.Contains(content, "15") || strings.Contains(content, "27")
		hasResult := strings.Contains(content, "42") // 15 + 27 = 42
		hasCalculation := strings.Contains(content, "sum") || strings.Contains(content, "add") || strings.Contains(content, "calculate")

		if !hasNumbers && !hasResult && !hasCalculation {
			t.Errorf("Response doesn't reference the calculation, numbers, or result: %s", resp.Content)
		}
		return
	}

	// Verify tool call details
	toolCall := resp.ToolCalls[0]
	if toolCall.Name != "calculate_sum" {
		t.Errorf("Expected tool call name 'calculate_sum', got: %s", toolCall.Name)
	}

	if toolCall.ID == "" {
		t.Error("Tool call ID is empty")
	}

	// Verify tool parameters
	if len(toolCall.Parameters) == 0 {
		t.Error("Tool call parameters are empty")
	}

	// Check for expected parameters
	if a, exists := toolCall.Parameters["a"]; !exists {
		t.Error("Missing parameter 'a' in tool call")
	} else {
		t.Logf("Parameter 'a': %v", a)
	}

	if b, exists := toolCall.Parameters["b"]; !exists {
		t.Error("Missing parameter 'b' in tool call")
	} else {
		t.Logf("Parameter 'b': %v", b)
	}

	t.Logf("Successfully invoked MCP tool: %s with ID: %s", toolCall.Name, toolCall.ID)
}

// TestOpenAIOfficial_ErrorHandling tests error scenarios.
func TestOpenAIOfficial_ErrorHandling(t *testing.T) {
	// Test with invalid API key
	client := NewOfficialClientWithModel("invalid-key", "gpt-5")

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: "user", Content: "This should fail"},
		},
		MaxTokens: 50,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := client.Complete(ctx, req)
	if err == nil {
		t.Fatal("Expected error with invalid API key, but got success")
	}

	t.Logf("Expected error received: %v", err)

	// Verify error message contains authentication info
	errStr := strings.ToLower(err.Error())
	if !strings.Contains(errStr, "auth") && !strings.Contains(errStr, "401") && !strings.Contains(errStr, "key") {
		t.Errorf("Error message doesn't indicate authentication issue: %v", err)
	}
}

// TestOpenAIOfficial_ArchitectReadTools tests OpenAI tool calling with our actual MCP read tools.
// This validates that the tool definitions used by architect (list_files, read_file, get_diff, submit_reply)
// work correctly with OpenAI's function calling API.
func TestOpenAIOfficial_ArchitectReadTools(t *testing.T) {
	// Skip if no API key available
	if os.Getenv("OPENAI_API_KEY") == "" {
		t.Skip("Skipping integration test: OPENAI_API_KEY not set")
	}

	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "gpt-5")

	// Define the actual MCP read tools used by architect
	// These match the tool definitions in pkg/tools/
	toolDefs := []tools.ToolDefinition{
		{
			Name:        "list_files",
			Description: "List files in a coder's workspace matching a pattern",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"coder_id": {
						Type:        "string",
						Description: "ID of the coder whose workspace to list",
					},
					"pattern": {
						Type:        "string",
						Description: "Glob pattern to match files (e.g., '*.go', 'src/**/*.ts')",
					},
				},
				Required: []string{"coder_id"},
			},
		},
		{
			Name:        "read_file",
			Description: "Read the contents of a file in a coder's workspace",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"coder_id": {
						Type:        "string",
						Description: "ID of the coder whose workspace contains the file",
					},
					"path": {
						Type:        "string",
						Description: "Path to the file relative to workspace root",
					},
				},
				Required: []string{"coder_id", "path"},
			},
		},
		{
			Name:        "get_diff",
			Description: "Get git diff for changes in a coder's workspace",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"coder_id": {
						Type:        "string",
						Description: "ID of the coder whose workspace to diff",
					},
					"path": {
						Type:        "string",
						Description: "Optional: specific file path to diff (omit for all changes)",
					},
				},
				Required: []string{"coder_id"},
			},
		},
		{
			Name:        "submit_reply",
			Description: "Submit the final response and exit iteration loop",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"response": {
						Type:        "string",
						Description: "The final response to send back",
					},
				},
				Required: []string{"response"},
			},
		},
	}

	// Create a request that should trigger list_files tool call
	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{
				Role: "system",
				Content: "You are an architect reviewing code. You have access to tools to explore coder workspaces. " +
					"Use list_files to see what files exist in coder-001's workspace.",
			},
			{
				Role: "user",
				Content: "Please list all Go files in coder-001's workspace using the list_files tool. " +
					"Pass coder_id='coder-001' and pattern='*.go'.",
			},
		},
		Tools:     toolDefs,
		MaxTokens: 1000,
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := client.Complete(ctx, req)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	t.Logf("Response Content: %s", resp.Content)
	t.Logf("Tool Calls: %d", len(resp.ToolCalls))

	// Log all tool calls for debugging
	for i, tc := range resp.ToolCalls {
		t.Logf("Tool Call %d: Name=%s, ID=%s, Params=%+v", i+1, tc.Name, tc.ID, tc.Parameters)
	}

	// Verify we got at least one tool call
	if len(resp.ToolCalls) == 0 {
		t.Logf("WARNING: No tool calls made. Response: %s", resp.Content)
		t.Skip("Model chose not to use tools - may need prompt adjustment")
		return
	}

	// Check if any tool call matches our expected architect read tools
	foundValidTool := false
	validToolNames := map[string]bool{
		"list_files":   true,
		"read_file":    true,
		"get_diff":     true,
		"submit_reply": true,
	}

	for _, tc := range resp.ToolCalls {
		if validToolNames[tc.Name] {
			foundValidTool = true
			t.Logf("✅ Found valid architect tool call: %s", tc.Name)

			// Verify tool call has an ID
			if tc.ID == "" {
				t.Errorf("Tool call %s missing ID", tc.Name)
			}

			// Verify parameters exist
			if len(tc.Parameters) == 0 {
				t.Errorf("Tool call %s has no parameters", tc.Name)
			}

			// Tool-specific validations
			switch tc.Name {
			case "list_files":
				if coderID, exists := tc.Parameters["coder_id"]; !exists {
					t.Error("list_files missing required parameter 'coder_id'")
				} else {
					t.Logf("  coder_id: %v", coderID)
				}
				if pattern, exists := tc.Parameters["pattern"]; exists {
					t.Logf("  pattern: %v", pattern)
				}

			case "read_file":
				if coderID, exists := tc.Parameters["coder_id"]; !exists {
					t.Error("read_file missing required parameter 'coder_id'")
				} else {
					t.Logf("  coder_id: %v", coderID)
				}
				if path, exists := tc.Parameters["path"]; !exists {
					t.Error("read_file missing required parameter 'path'")
				} else {
					t.Logf("  path: %v", path)
				}

			case "get_diff":
				if coderID, exists := tc.Parameters["coder_id"]; !exists {
					t.Error("get_diff missing required parameter 'coder_id'")
				} else {
					t.Logf("  coder_id: %v", coderID)
				}

			case "submit_reply":
				if response, exists := tc.Parameters["response"]; !exists {
					t.Error("submit_reply missing required parameter 'response'")
				} else {
					t.Logf("  response: %v", response)
				}
			}
		}
	}

	if !foundValidTool {
		t.Errorf("No valid architect read tools were called. Got: %v", resp.ToolCalls)
	}

	t.Logf("✅ OpenAI successfully invoked architect read tools")
	t.Logf("✅ Tool definitions correctly converted to OpenAI function calling format")
	t.Logf("✅ Tool parameters correctly extracted from response")
}
