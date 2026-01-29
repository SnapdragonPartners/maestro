// Package agent provides LLM client interfaces and factory methods.
package agent

import (
	"fmt"

	"orchestrator/pkg/agent/internal/llmimpl/anthropic"
	"orchestrator/pkg/agent/internal/llmimpl/google"
	"orchestrator/pkg/agent/internal/llmimpl/ollama"
	"orchestrator/pkg/agent/internal/llmimpl/openaiofficial"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/middleware/logging"
	"orchestrator/pkg/agent/middleware/validation"
	"orchestrator/pkg/config"
)

// NewTestLLMClient creates a raw LLM client for integration testing.
// This bypasses the middleware chain for simpler testing.
// Returns an error if the API key is not available.
func NewTestLLMClient(modelName string) (llm.LLMClient, error) {
	provider, err := config.GetModelProvider(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to get model provider for %s: %w", modelName, err)
	}

	apiKey, err := config.GetAPIKey(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for provider %s: %w", provider, err)
	}

	var client llm.LLMClient
	switch provider {
	case config.ProviderAnthropic:
		client = anthropic.NewClaudeClientWithModel(apiKey, modelName)
	case config.ProviderOpenAI:
		client = openaiofficial.NewOfficialClientWithModel(apiKey, modelName)
	case config.ProviderGoogle:
		client = google.NewGeminiClientWithModel(apiKey, modelName)
	case config.ProviderOllama:
		client = ollama.NewOllamaClientWithModel(apiKey, modelName)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}

	return client, nil
}

// NewTestLLMClientWithMiddleware creates an LLM client with validation and logging middleware.
// This more closely matches the production middleware chain for integration testing.
// The agentType parameter determines agent-specific validation behavior:
//   - TypeArchitect: Architect agent (can respond with text only).
//   - TypeCoder: Coder agent (must use tools).
func NewTestLLMClientWithMiddleware(modelName string, agentType Type) (llm.LLMClient, error) {
	// First get the raw client
	rawClient, err := NewTestLLMClient(modelName)
	if err != nil {
		return nil, err
	}

	// Determine validation agent type
	var validationType validation.AgentType
	switch agentType {
	case TypeArchitect:
		validationType = validation.AgentTypeArchitect
	case TypePM:
		validationType = validation.AgentTypeArchitect // PM can respond with text
	default:
		validationType = validation.AgentTypeCoder
	}

	// Apply validation and logging middleware (subset of production chain)
	// Note: We skip rate limiting, circuit breaker, and timeout for simpler testing
	validator := validation.NewEmptyResponseValidator(validationType)

	client := llm.Chain(rawClient,
		validator.Middleware(),                   // Agent-aware empty response validation
		logging.EmptyResponseLoggingMiddleware(), // Log empty responses
	)

	return client, nil
}
