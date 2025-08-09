// Package ratelimit provides rate limiting functionality for LLM clients.
package ratelimit

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/utils"
)

// Limiter defines the interface for rate limiting implementations.
type Limiter interface {
	// Acquire attempts to acquire the specified number of tokens.
	// Returns an error if tokens cannot be acquired within the context deadline.
	Acquire(ctx context.Context, tokens int) error

	// TryAcquire attempts to acquire tokens immediately without blocking.
	// Returns true if tokens were acquired, false otherwise.
	TryAcquire(tokens int) bool
}

// TokenEstimator estimates the number of tokens needed for a request.
type TokenEstimator interface {
	// EstimatePrompt estimates the number of prompt tokens for a request.
	EstimatePrompt(req llm.CompletionRequest) int
}

// Config defines rate limiting configuration for a provider.
type Config struct {
	TokensPerMinute int `json:"tokens_per_minute"` // Rate limit in tokens per minute
	Burst           int `json:"burst"`             // Burst capacity
	MaxConcurrency  int `json:"max_concurrency"`   // Maximum concurrent requests
}

// DefaultTokenEstimator provides token estimation using TikToken.
type DefaultTokenEstimator struct{}

// NewDefaultTokenEstimator creates a new default token estimator.
func NewDefaultTokenEstimator() TokenEstimator {
	return &DefaultTokenEstimator{}
}

// EstimatePrompt estimates prompt tokens using TikToken-based counting.
func (e *DefaultTokenEstimator) EstimatePrompt(req llm.CompletionRequest) int {
	var promptText string
	for i := range req.Messages {
		promptText += req.Messages[i].Content + "\n"
	}
	return utils.CountTokensSimple(promptText)
}

// stubLimiter is a no-op implementation for MVP phase.
type stubLimiter struct{}

// NewStubLimiter creates a rate limiter that always allows requests (for MVP).
func NewStubLimiter(_ Config) Limiter {
	return &stubLimiter{}
}

// Acquire always succeeds immediately in the stub implementation.
func (s *stubLimiter) Acquire(_ context.Context, _ int) error {
	return nil
}

// TryAcquire always returns true in the stub implementation.
func (s *stubLimiter) TryAcquire(_ int) bool {
	return true
}

// ProviderLimiterMap manages rate limiters for different API providers.
type ProviderLimiterMap struct {
	limiters map[string]Limiter // provider -> limiter
}

// NewProviderLimiterMap creates a new provider limiter map.
func NewProviderLimiterMap(configs map[string]Config) *ProviderLimiterMap {
	limiters := make(map[string]Limiter)
	for provider, cfg := range configs {
		limiters[provider] = NewStubLimiter(cfg)
	}
	return &ProviderLimiterMap{limiters: limiters}
}

// GetLimiter returns the rate limiter for a specific model.
func (p *ProviderLimiterMap) GetLimiter(modelName string) (Limiter, error) {
	provider, err := config.GetModelProvider(modelName)
	if err != nil {
		return nil, fmt.Errorf("cannot determine provider for model %s: %w", modelName, err)
	}

	limiter, exists := p.limiters[provider]
	if !exists {
		return nil, fmt.Errorf("no rate limiter configured for provider %s", provider)
	}

	return limiter, nil
}
