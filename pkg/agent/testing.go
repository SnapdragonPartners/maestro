// Package agent provides LLM client interfaces and factory methods.
package agent

import (
	"fmt"

	"orchestrator/pkg/agent/internal/llmadapter"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/middleware/validation"
	"orchestrator/pkg/config"
)

// NewTestLLMClient creates a raw maestro-llms-backed client for integration
// testing (no middleware). Returns an error if the API key is unavailable.
func NewTestLLMClient(modelName string) (llm.LLMClient, error) {
	provider, err := config.GetModelProvider(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to get model provider for %s: %w", modelName, err)
	}
	apiKey, err := config.GetAPIKey(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for provider %s: %w", provider, err)
	}
	client, err := llmadapter.New(provider, apiKey, modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to create maestro-llms client: %w", err)
	}
	return client, nil
}

// NewTestLLMClientWithMiddleware wraps the raw client in the agent-aware
// empty-response validation decorator (the production app-side behavior),
// for integration tests that exercise that path. Resilience middleware now
// lives in the maestro-llms chain and is not part of this lightweight helper.
func NewTestLLMClientWithMiddleware(modelName string, agentType Type) (llm.LLMClient, error) {
	rawClient, err := NewTestLLMClient(modelName)
	if err != nil {
		return nil, err
	}

	validationType := validation.AgentTypeCoder
	switch agentType {
	case TypeArchitect, TypePM: // PM responds with text like the architect
		validationType = validation.AgentTypeArchitect
	case TypeCoder:
		validationType = validation.AgentTypeCoder
	}

	return validation.NewEmptyResponseValidator(validationType).Wrap(rawClient), nil
}
