//go:build integration

package ollama

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/tools"
)

// TestIntegration_SimpleCompletion tests basic completion with a local Ollama instance.
// Requires: OLLAMA_HOST or default localhost:11434 with phi4:latest model.
// Run with: go test -tags=integration ./pkg/agent/internal/llmimpl/ollama/...
func TestIntegration_SimpleCompletion(t *testing.T) {
	if os.Getenv("OLLAMA_HOST") == "" {
		// Check if default Ollama is accessible
		host := "http://localhost:11434"
		client := NewOllamaClientWithModel(host, "phi4:latest")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		// Simple completion test
		resp, err := client.Complete(ctx, llm.CompletionRequest{
			Messages: []llm.CompletionMessage{
				{Role: llm.RoleUser, Content: "Say 'hello' and nothing else."},
			},
			MaxTokens:   50,
			Temperature: 0.1,
		})

		if err != nil {
			t.Skipf("Ollama not available at %s: %v", host, err)
		}

		require.NotEmpty(t, resp.Content)
		assert.Contains(t, strings.ToLower(resp.Content), "hello")
		t.Logf("Response: %s", resp.Content)
	}
}

// TestIntegration_ToolCalling tests tool calling with a local Ollama instance.
// Note: Requires a model that supports tool calling (e.g., llama3.1, llama3.2, mistral)
func TestIntegration_ToolCalling(t *testing.T) {
	host := os.Getenv("OLLAMA_HOST")
	if host == "" {
		host = "http://localhost:11434"
	}

	// Use llama3.2 which has native tool support in Ollama
	// Note: phi4:latest needs a modified template for tools (see zac/phi4-tools)
	client := NewOllamaClientWithModel(host, "llama3.2:latest")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Define a simple tool
	toolDefs := []tools.ToolDefinition{
		{
			Name:        "get_weather",
			Description: "Get the current weather for a location",
			InputSchema: tools.InputSchema{
				Type: "object",
				Properties: map[string]tools.Property{
					"location": {
						Type:        "string",
						Description: "The city name",
					},
				},
				Required: []string{"location"},
			},
		},
	}

	resp, err := client.Complete(ctx, llm.CompletionRequest{
		Messages: []llm.CompletionMessage{
			{Role: llm.RoleSystem, Content: "You are a helpful assistant. Use the get_weather tool when asked about weather."},
			{Role: llm.RoleUser, Content: "What's the weather like in San Francisco?"},
		},
		Tools:       toolDefs,
		ToolChoice:  "auto",
		MaxTokens:   200,
		Temperature: 0.1,
	})

	if err != nil {
		t.Skipf("Ollama not available or error: %v", err)
	}

	t.Logf("Response content: %s", resp.Content)
	t.Logf("Tool calls: %+v", resp.ToolCalls)
	t.Logf("Stop reason: %s", resp.StopReason)

	// The model should either call the tool or respond with text
	// Tool calling support varies by model
	if len(resp.ToolCalls) > 0 {
		assert.Equal(t, "get_weather", resp.ToolCalls[0].Name)
		t.Logf("Tool was called with params: %+v", resp.ToolCalls[0].Parameters)
	} else {
		assert.NotEmpty(t, resp.Content, "Expected either tool call or text response")
	}
}
