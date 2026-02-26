// Package agent provides LLM client factory with middleware chain construction.
package agent

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent/internal/llmimpl/anthropic"
	"orchestrator/pkg/agent/internal/llmimpl/google"
	"orchestrator/pkg/agent/internal/llmimpl/ollama"
	"orchestrator/pkg/agent/internal/llmimpl/openaiofficial"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/middleware/logging"
	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/agent/middleware/resilience/circuit"
	"orchestrator/pkg/agent/middleware/resilience/ratelimit"
	"orchestrator/pkg/agent/middleware/resilience/retry"
	"orchestrator/pkg/agent/middleware/resilience/timeout"
	"orchestrator/pkg/agent/middleware/validation"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// LLMClientFactory creates LLM clients with properly configured middleware chains.
type LLMClientFactory struct {
	circuitBreakers map[string]circuit.Breaker
	rateLimitMap    *ratelimit.ProviderLimiterMap
	metricsRecorder metrics.Recorder
	config          config.Config
}

// NewLLMClientFactory creates a new LLM client factory with the given configuration.
// Uses context.Background() for rate limiter lifecycle - callers should call Stop() on shutdown.
func NewLLMClientFactory(cfg *config.Config) (*LLMClientFactory, error) {
	logger := logx.NewLogger("factory")

	// Create metrics recorder based on configuration
	var recorder metrics.Recorder
	if cfg.Agents != nil && cfg.Agents.Metrics.Enabled {
		logger.Info("📊 Using internal metrics recorder (enabled=%v)",
			cfg.Agents.Metrics.Enabled)
		recorder = metrics.NewInternalRecorder()
	} else {
		logger.Info("📊 Using no-op metrics recorder (enabled=%v)",
			cfg.Agents != nil && cfg.Agents.Metrics.Enabled)
		recorder = metrics.Nop()
	}

	// Initialize circuit breakers for each provider
	circuitBreakers := make(map[string]circuit.Breaker)
	for _, provider := range []string{
		string(config.ProviderAnthropic),
		string(config.ProviderOpenAI),
		string(config.ProviderGoogle),
		string(config.ProviderOllama),
	} {
		circuitBreakers[provider] = circuit.New(circuit.Config{
			FailureThreshold: cfg.Agents.Resilience.CircuitBreaker.FailureThreshold,
			SuccessThreshold: cfg.Agents.Resilience.CircuitBreaker.SuccessThreshold,
			Timeout:          cfg.Agents.Resilience.CircuitBreaker.Timeout,
		})
	}

	// Initialize rate limit map with provider configs (real token bucket implementation)
	rateLimitConfigs := map[string]ratelimit.Config{
		string(config.ProviderAnthropic): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.Anthropic.TokensPerMinute,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.Anthropic.MaxConcurrency,
		},
		string(config.ProviderOpenAI): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.OpenAI.TokensPerMinute,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.OpenAI.MaxConcurrency,
		},
		string(config.ProviderGoogle): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.Google.TokensPerMinute,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.Google.MaxConcurrency,
		},
		string(config.ProviderOllama): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.Ollama.TokensPerMinute,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.Ollama.MaxConcurrency,
		},
	}

	// Create rate limiter map with background context for lifecycle management
	// Use request timeout from config for stale acquisition detection
	rateLimitMap := ratelimit.NewProviderLimiterMap(
		context.Background(),
		rateLimitConfigs,
		cfg.Agents.Resilience.Timeout,
	)

	return &LLMClientFactory{
		config:          *cfg,
		metricsRecorder: recorder,
		circuitBreakers: circuitBreakers,
		rateLimitMap:    rateLimitMap,
	}, nil
}

// Stop cleans up factory resources (stops rate limiter refill timers).
// Should be called on shutdown.
func (f *LLMClientFactory) Stop() {
	if f.rateLimitMap != nil {
		f.rateLimitMap.Stop()
	}
}

// GetRateLimitStats returns rate limiter statistics for all providers.
// Used by the web UI to display congestion metrics.
func (f *LLMClientFactory) GetRateLimitStats() map[string]ratelimit.LimiterStats {
	if f.rateLimitMap == nil {
		return make(map[string]ratelimit.LimiterStats)
	}
	return f.rateLimitMap.GetAllStats()
}

// CreateClient creates an LLM client for the specified agent type with full middleware chain.
// The API key is automatically retrieved from environment variables based on the model's provider.
func (f *LLMClientFactory) CreateClient(agentType Type) (LLMClient, error) {
	var modelName string
	switch agentType {
	case TypeCoder:
		modelName = config.GetEffectiveCoderModel()
	case TypeArchitect:
		modelName = config.GetEffectiveArchitectModel()
	case TypePM:
		modelName = config.GetEffectivePMModel()
	default:
		return nil, fmt.Errorf("unsupported agent type: %s", agentType)
	}

	return f.createClientWithMiddleware(modelName, agentType.String(), nil, nil)
}

// CreateClientWithContext creates an LLM client with StateProvider and logger for enhanced metrics.
func (f *LLMClientFactory) CreateClientWithContext(agentType Type, stateProvider metrics.StateProvider, logger *logx.Logger) (LLMClient, error) {
	var modelName string
	switch agentType {
	case TypeCoder:
		modelName = config.GetEffectiveCoderModel()
	case TypeArchitect:
		modelName = config.GetEffectiveArchitectModel()
	case TypePM:
		modelName = config.GetEffectivePMModel()
	default:
		return nil, fmt.Errorf("unsupported agent type: %s", agentType)
	}

	return f.createClientWithMiddleware(modelName, agentType.String(), stateProvider, logger)
}

// createClientWithMiddleware creates a client with the full middleware chain.
func (f *LLMClientFactory) createClientWithMiddleware(modelName, agentTypeStr string, stateProvider metrics.StateProvider, logger *logx.Logger) (LLMClient, error) {
	// Create the raw LLM client based on provider
	provider, err := config.GetModelProvider(modelName)
	if err != nil {
		return nil, fmt.Errorf("failed to determine provider for model %s: %w", modelName, err)
	}

	// Get the API key for this provider from environment variables
	apiKey, err := config.GetAPIKey(provider)
	if err != nil {
		return nil, fmt.Errorf("failed to get API key for provider %s: %w", provider, err)
	}

	var rawClient LLMClient
	switch provider {
	case config.ProviderAnthropic:
		rawClient = anthropic.NewClaudeClientWithModel(apiKey, modelName)
	case config.ProviderOpenAI:
		// Use official OpenAI SDK with Responses API for all OpenAI models
		// Supports tool calling via Responses API (o4-mini, gpt-4o, etc.)
		rawClient = openaiofficial.NewOfficialClientWithModel(apiKey, modelName)
	case config.ProviderGoogle:
		rawClient = google.NewGeminiClientWithModel(apiKey, modelName)
	case config.ProviderOllama:
		// For Ollama, apiKey contains the host URL (e.g., "http://localhost:11434")
		rawClient = ollama.NewOllamaClientWithModel(apiKey, modelName)
	default:
		return nil, fmt.Errorf("unsupported provider: %s", provider)
	}

	// Get the circuit breaker for this provider
	circuitBreaker, exists := f.circuitBreakers[provider]
	if !exists {
		return nil, fmt.Errorf("no circuit breaker found for provider %s", provider)
	}

	// Create retry policy
	retryConfig := retry.Config{
		MaxAttempts:   f.config.Agents.Resilience.Retry.MaxAttempts,
		InitialDelay:  f.config.Agents.Resilience.Retry.InitialDelay,
		MaxDelay:      f.config.Agents.Resilience.Retry.MaxDelay,
		BackoffFactor: f.config.Agents.Resilience.Retry.BackoffFactor,
		Jitter:        f.config.Agents.Resilience.Retry.Jitter,
	}
	retryPolicy := retry.NewPolicy(retryConfig, nil) // Use default classifier

	// Ensure logger is available for retry middleware
	retryLogger := logger
	if retryLogger == nil {
		retryLogger = logx.NewLogger("retry")
	}

	// Build the full middleware chain

	// Convert agentTypeStr to validation.AgentType
	var validationAgentType validation.AgentType
	switch Type(agentTypeStr) {
	case TypeArchitect:
		validationAgentType = validation.AgentTypeArchitect
	case TypeCoder:
		validationAgentType = validation.AgentTypeCoder
	case TypePM:
		validationAgentType = validation.AgentTypeArchitect // PM can respond with text (like architect)
	default:
		validationAgentType = validation.AgentTypeCoder // Default to coder (safer)
	}

	// Create agent-aware validator
	validator := validation.NewEmptyResponseValidator(validationAgentType)

	client := llm.Chain(rawClient,
		validator.Middleware(), // Agent-aware empty response validation
		metrics.Middleware(f.metricsRecorder, nil, stateProvider, logger),
		circuit.Middleware(circuitBreaker),
		retry.Middleware(retryPolicy, retryLogger),
		logging.EmptyResponseLoggingMiddleware(),                 // Log empty responses after retry exhaustion
		ratelimit.Middleware(f.rateLimitMap, nil, stateProvider), // Real token bucket with concurrency limiting
		timeout.Middleware(f.config.Agents.Resilience.Timeout),
	)

	return client, nil
}
