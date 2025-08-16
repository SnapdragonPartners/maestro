// Package agent provides LLM client factory with middleware chain construction.
package agent

import (
	"fmt"

	"orchestrator/pkg/agent/internal/llmimpl/anthropic"
	"orchestrator/pkg/agent/internal/llmimpl/openai"
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
	config          config.Config
	metricsRecorder metrics.Recorder
	circuitBreakers map[string]circuit.Breaker
	rateLimitMap    *ratelimit.ProviderLimiterMap
}

// NewLLMClientFactory creates a new LLM client factory with the given configuration.
func NewLLMClientFactory(cfg config.Config) (*LLMClientFactory, error) {
	// TODO: TEMPORARY FIX - use noop recorder to test if Prometheus is causing the hang
	// recorder := metrics.NewPrometheusRecorder()
	recorder := metrics.Nop()

	// Initialize circuit breakers for each provider
	circuitBreakers := make(map[string]circuit.Breaker)
	for _, provider := range []string{string(config.ProviderAnthropic), string(config.ProviderOpenAI), string(config.ProviderOpenAIOfficial)} {
		circuitBreakers[provider] = circuit.New(circuit.Config{
			FailureThreshold: cfg.Agents.Resilience.CircuitBreaker.FailureThreshold,
			SuccessThreshold: cfg.Agents.Resilience.CircuitBreaker.SuccessThreshold,
			Timeout:          cfg.Agents.Resilience.CircuitBreaker.Timeout,
		})
	}

	// Initialize rate limit map with provider configs
	rateLimitConfigs := map[string]ratelimit.Config{
		string(config.ProviderAnthropic): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.Anthropic.TokensPerMinute,
			Burst:           cfg.Agents.Resilience.RateLimit.Anthropic.Burst,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.Anthropic.MaxConcurrency,
		},
		string(config.ProviderOpenAI): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.OpenAI.TokensPerMinute,
			Burst:           cfg.Agents.Resilience.RateLimit.OpenAI.Burst,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.OpenAI.MaxConcurrency,
		},
		string(config.ProviderOpenAIOfficial): {
			TokensPerMinute: cfg.Agents.Resilience.RateLimit.OpenAIOfficial.TokensPerMinute,
			Burst:           cfg.Agents.Resilience.RateLimit.OpenAIOfficial.Burst,
			MaxConcurrency:  cfg.Agents.Resilience.RateLimit.OpenAIOfficial.MaxConcurrency,
		},
	}
	rateLimitMap := ratelimit.NewProviderLimiterMap(rateLimitConfigs)

	return &LLMClientFactory{
		config:          cfg,
		metricsRecorder: recorder,
		circuitBreakers: circuitBreakers,
		rateLimitMap:    rateLimitMap,
	}, nil
}

// CreateClient creates an LLM client for the specified agent type with full middleware chain.
// The API key is automatically retrieved from environment variables based on the model's provider.
func (f *LLMClientFactory) CreateClient(agentType Type) (LLMClient, error) {
	var modelName string
	switch agentType {
	case TypeCoder:
		modelName = f.config.Agents.CoderModel
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for agent model selection
		fmt.Printf("🏭 FACTORY: TypeCoder selected model: %s\n", modelName)
	case TypeArchitect:
		modelName = f.config.Agents.ArchitectModel
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for agent model selection
		fmt.Printf("🏭 FACTORY: TypeArchitect selected model: %s\n", modelName)
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
		modelName = f.config.Agents.CoderModel
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for agent model selection
		fmt.Printf("🏭 FACTORY: TypeCoder (with context) selected model: %s\n", modelName)
	case TypeArchitect:
		modelName = f.config.Agents.ArchitectModel
		// TODO: REMOVE DEBUG LOGGING - temporary debugging for agent model selection
		fmt.Printf("🏭 FACTORY: TypeArchitect (with context) selected model: %s\n", modelName)
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
		rawClient = anthropic.NewClaudeClient(apiKey)
	case config.ProviderOpenAI:
		rawClient = openai.NewO3ClientWithModel(apiKey, modelName)
	case config.ProviderOpenAIOfficial:
		rawClient = openaiofficial.NewOfficialClientWithModel(apiKey, modelName)
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

	// Build the full middleware chain
	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
	fmt.Printf("🏭 FACTORY: Building full middleware chain for %s\n", modelName)

	// Convert agentTypeStr to validation.AgentType
	var validationAgentType validation.AgentType
	switch Type(agentTypeStr) {
	case TypeArchitect:
		validationAgentType = validation.AgentTypeArchitect
	case TypeCoder:
		validationAgentType = validation.AgentTypeCoder
	default:
		validationAgentType = validation.AgentTypeCoder // Default to coder (safer)
	}

	// Create agent-aware validator
	validator := validation.NewEmptyResponseValidator(validationAgentType)

	client := llm.Chain(rawClient,
		validator.Middleware(), // Agent-aware empty response validation
		metrics.Middleware(f.metricsRecorder, nil, stateProvider, logger),
		circuit.Middleware(circuitBreaker),
		retry.Middleware(retryPolicy),
		logging.EmptyResponseLoggingMiddleware(),                     // Log empty responses after retry exhaustion
		ratelimit.Middleware(f.rateLimitMap, nil, f.metricsRecorder), // Uses default token estimator
		timeout.Middleware(f.config.Agents.Resilience.Timeout),
	)

	// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
	fmt.Printf("🏭 FACTORY: Middleware chain built successfully for %s\n", modelName)

	return client, nil
}

// CreateLLMClientForAgent creates a basic LLM client for an agent type with middleware.
// This is a helper function for agent constructors to avoid code duplication.
func CreateLLMClientForAgent(agentType Type) (LLMClient, error) {
	// TODO: REMOVE DEBUG LOGGING - aggressive validation
	fmt.Printf("🏭 CREATE_CLIENT: Creating LLM client for agentType=%s\n", agentType)

	// Get the current configuration to build LLM client with middleware
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Printf("🏭 CREATE_CLIENT: Failed to get config: %v\n", err)
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}
	fmt.Printf("🏭 CREATE_CLIENT: Got config successfully\n")

	// Create LLM client factory
	factory, err := NewLLMClientFactory(cfg)
	if err != nil {
		fmt.Printf("🏭 CREATE_CLIENT: Failed to create factory: %v\n", err)
		return nil, fmt.Errorf("failed to create LLM client factory: %w", err)
	}
	fmt.Printf("🏭 CREATE_CLIENT: Factory created successfully\n")

	// Create initial client without metrics context (circular dependency)
	fmt.Printf("🏭 CREATE_CLIENT: Creating client via factory.CreateClient()\n")
	llmClient, err := factory.CreateClient(agentType)
	if err != nil {
		fmt.Printf("🏭 CREATE_CLIENT: Failed to create client: %v\n", err)
		return nil, fmt.Errorf("failed to create %s LLM client: %w", agentType, err)
	}
	fmt.Printf("🏭 CREATE_CLIENT: Client created successfully: %p\n", llmClient)

	return llmClient, nil
}

// EnhanceLLMClientWithMetrics replaces a basic LLM client with an enhanced version that includes metrics context.
// This is called after the agent is created to break circular dependencies.
func EnhanceLLMClientWithMetrics(_ LLMClient, agentType Type, stateProvider metrics.StateProvider, logger *logx.Logger) (LLMClient, error) {
	// TODO: REMOVE DEBUG LOGGING - aggressive validation
	fmt.Printf("🏭 ENHANCE_CLIENT: Enhancing client for agentType=%s\n", agentType)
	fmt.Printf("🏭 ENHANCE_CLIENT: stateProvider=%p\n", stateProvider)
	fmt.Printf("🏭 ENHANCE_CLIENT: logger=%p\n", logger)

	// Get the current configuration
	cfg, err := config.GetConfig()
	if err != nil {
		fmt.Printf("🏭 ENHANCE_CLIENT: Failed to get config: %v\n", err)
		return nil, fmt.Errorf("failed to get configuration: %w", err)
	}
	fmt.Printf("🏭 ENHANCE_CLIENT: Got config successfully\n")

	// Create LLM client factory
	factory, err := NewLLMClientFactory(cfg)
	if err != nil {
		fmt.Printf("🏭 ENHANCE_CLIENT: Failed to create factory: %v\n", err)
		return nil, fmt.Errorf("failed to create LLM client factory: %w", err)
	}
	fmt.Printf("🏭 ENHANCE_CLIENT: Factory created successfully\n")

	// Create enhanced client with metrics context
	fmt.Printf("🏭 ENHANCE_CLIENT: Creating client with context via factory.CreateClientWithContext()\n")
	enhancedClient, err := factory.CreateClientWithContext(agentType, stateProvider, logger)
	if err != nil {
		fmt.Printf("🏭 ENHANCE_CLIENT: Failed to create enhanced client: %v\n", err)
		return nil, fmt.Errorf("failed to create enhanced %s LLM client: %w", agentType, err)
	}
	fmt.Printf("🏭 ENHANCE_CLIENT: Enhanced client created successfully: %p\n", enhancedClient)

	return enhancedClient, nil
}
