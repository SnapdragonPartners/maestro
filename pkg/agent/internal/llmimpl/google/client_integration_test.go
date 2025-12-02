//go:build integration

package google

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// retryableCompletion wraps client.Complete with retry logic for transient errors.
// If all retries fail with transient errors (504, 503, etc.), the test is skipped
// rather than failed, since sustained API unavailability is an external issue.
func retryableCompletion(t *testing.T, client llm.LLMClient, req llm.CompletionRequest, maxRetries int) (llm.CompletionResponse, error) {
	t.Helper()
	var lastErr error
	transientFailures := 0
	for attempt := 1; attempt <= maxRetries; attempt++ {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		resp, err := client.Complete(ctx, req)
		cancel()

		if err == nil {
			return resp, nil
		}

		// Check for transient errors that should be retried.
		errStr := err.Error()
		isTransient := strings.Contains(errStr, "504") ||
			strings.Contains(errStr, "503") ||
			strings.Contains(errStr, "429") ||
			strings.Contains(errStr, "DEADLINE_EXCEEDED") ||
			strings.Contains(errStr, "RESOURCE_EXHAUSTED")

		if !isTransient {
			return llm.CompletionResponse{}, err
		}

		transientFailures++
		lastErr = err
		if attempt < maxRetries {
			t.Logf("Attempt %d/%d failed with transient error: %v. Retrying...", attempt, maxRetries, err)
			time.Sleep(time.Duration(attempt) * 2 * time.Second) // Exponential backoff.
		}
	}

	// If all failures were transient (API unavailability), skip rather than fail.
	// This prevents CI failures due to external service issues.
	if transientFailures == maxRetries {
		t.Skipf("Skipping test: Gemini API unavailable after %d attempts (last error: %v)", maxRetries, lastErr)
	}

	return llm.CompletionResponse{}, lastErr
}

// TestGeminiBasicCompletion tests basic text completion with Gemini 3 Pro.
func TestGeminiBasicCompletion(t *testing.T) {
	apiKey := os.Getenv("GOOGLE_GENAI_API_KEY")
	if apiKey == "" {
		t.Skip("GOOGLE_GENAI_API_KEY not set, skipping integration test")
	}

	client := NewGeminiClientWithModel(apiKey, "gemini-3-pro-preview")

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{
				Role:    llm.RoleUser,
				Content: "What is 2+2? Answer with just the number.",
			},
		},
		MaxTokens:   100,
		Temperature: 0.0,
	}

	resp, err := retryableCompletion(t, client, req, 3)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	if resp.Content == "" {
		t.Error("Expected non-empty response content")
	}

	t.Logf("Response: %s", resp.Content)
	t.Logf("Stop reason: %s", resp.StopReason)
}

// TestGeminiToolCalling tests tool calling with function_calling_config mode ANY.
func TestGeminiToolCalling(t *testing.T) {
	apiKey := os.Getenv("GOOGLE_GENAI_API_KEY")
	if apiKey == "" {
		t.Skip("GOOGLE_GENAI_API_KEY not set, skipping integration test")
	}

	client := NewGeminiClientWithModel(apiKey, "gemini-3-pro-preview")

	// Define a simple calculator tool.
	calcTool := tools.ToolDefinition{
		Name:        "calculate",
		Description: "Perform basic arithmetic calculations",
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"operation": {
					Type:        "string",
					Description: "The operation to perform",
					Enum:        []string{"add", "subtract", "multiply", "divide"},
				},
				"a": {
					Type:        "number",
					Description: "First operand",
				},
				"b": {
					Type:        "number",
					Description: "Second operand",
				},
			},
			Required: []string{"operation", "a", "b"},
		},
	}

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{
				Role:    llm.RoleUser,
				Content: "Calculate 15 + 27 using the calculate tool.",
			},
		},
		Tools:       []tools.ToolDefinition{calcTool},
		ToolChoice:  "required", // Force tool use.
		MaxTokens:   500,
		Temperature: 0.0,
	}

	resp, err := retryableCompletion(t, client, req, 3)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	if len(resp.ToolCalls) == 0 {
		t.Error("Expected tool calls in response, got none")
	}

	// Verify we got a calculate tool call
	foundCalc := false
	for _, tc := range resp.ToolCalls {
		t.Logf("Tool call: %s (ID: %s)", tc.Name, tc.ID)
		if tc.Name == "calculate" {
			foundCalc = true
			if tc.Parameters == nil {
				t.Error("Expected parameters in tool call")
			}
			t.Logf("Parameters: %+v", tc.Parameters)
		}
	}

	if !foundCalc {
		t.Error("Expected calculate tool call, but didn't find one")
	}
}

// TestGeminiSystemMessage tests that system messages are properly handled with tool calling.
func TestGeminiSystemMessage(t *testing.T) {
	apiKey := os.Getenv("GOOGLE_GENAI_API_KEY")
	if apiKey == "" {
		t.Skip("GOOGLE_GENAI_API_KEY not set, skipping integration test")
	}

	client := NewGeminiClientWithModel(apiKey, "gemini-3-pro-preview")

	// Define a simple answer tool.
	answerTool := tools.ToolDefinition{
		Name:        "answer",
		Description: "Provide the answer to the user's question",
		InputSchema: tools.InputSchema{
			Type: "object",
			Properties: map[string]tools.Property{
				"answer": {
					Type:        "string",
					Description: "The answer to provide",
				},
			},
			Required: []string{"answer"},
		},
	}

	req := llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{
				Role:    llm.RoleSystem,
				Content: "You are a helpful assistant. Use the answer tool to provide responses.",
			},
			{
				Role:    llm.RoleUser,
				Content: "What is the capital of France?",
			},
		},
		Tools:       []tools.ToolDefinition{answerTool},
		MaxTokens:   100,
		Temperature: 0.0,
	}

	resp, err := retryableCompletion(t, client, req, 3)
	if err != nil {
		t.Fatalf("Completion failed: %v", err)
	}

	// Should return a tool call
	if len(resp.ToolCalls) == 0 {
		t.Error("Expected at least one tool call")
	}

	// Verify we got the answer tool call
	foundAnswer := false
	for _, call := range resp.ToolCalls {
		if call.Name == "answer" {
			foundAnswer = true
			t.Logf("Answer tool called with params: %v", call.Parameters)
		}
	}

	if !foundAnswer {
		t.Error("Expected answer tool call, but didn't find one")
	}
}
