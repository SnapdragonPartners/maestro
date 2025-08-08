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

	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "gpt-4o")

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: "user", Content: "Please respond with the text 'Hello from OpenAI Official client!' and your favorite color."},
		},
		MaxTokens: 50,
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

	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "gpt-4o")

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: "system", Content: "You are a helpful assistant that responds only in valid JSON format."},
			{Role: "user", Content: `Create a JSON object with these fields:
- "status": "success"  
- "provider": "openai_official"
- "model": "gpt-4o"
- "timestamp": current timestamp as string
- "message": "Integration test completed successfully"

Return ONLY the JSON, no other text.`},
		},
		MaxTokens: 150,
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

	client := NewOfficialClientWithModel(os.Getenv("OPENAI_API_KEY"), "gpt-4o")

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
		MaxTokens: 200,
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

		// Check if the response at least mentions calculation or the numbers
		content := strings.ToLower(resp.Content)
		if !strings.Contains(content, "15") || !strings.Contains(content, "27") {
			t.Errorf("Response doesn't reference the requested numbers: %s", resp.Content)
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
	client := NewOfficialClientWithModel("invalid-key", "gpt-4o")

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
