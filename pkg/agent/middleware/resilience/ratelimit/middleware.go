// Package ratelimit provides rate limiting middleware for LLM clients.
package ratelimit

import (
	"context"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/proto"
)

// StateProvider provides access to agent state for rate limiting (agent ID for tracking).
type StateProvider interface {
	// GetID returns the agent ID.
	GetID() string
	// GetCurrentState returns the agent's current state (optional, for future use).
	GetCurrentState() proto.State
	// GetStoryID returns the current story ID (optional, for future use).
	GetStoryID() string
}

// Middleware returns a middleware function that wraps an LLM client with rate limiting.
// It estimates token usage and acquires tokens and concurrency slots before making requests.
func Middleware(limiterMap *ProviderLimiterMap, estimator TokenEstimator, stateProvider StateProvider) llm.Middleware {
	if estimator == nil {
		estimator = NewDefaultTokenEstimator()
	}

	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with rate limiting
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				// Get model configuration to determine provider
				modelName := next.GetModelName()

				// Get the appropriate rate limiter
				limiter, err := limiterMap.GetLimiter(modelName)
				if err != nil {
					return llm.CompletionResponse{}, err
				}

				// Estimate tokens needed (prompt + max output)
				promptTokens := estimator.EstimatePrompt(req)
				totalTokens := promptTokens + req.MaxTokens

				// Get agent ID for tracking
				agentID := stateProvider.GetID()

				// Acquire tokens and concurrency slot atomically
				release, err := limiter.Acquire(ctx, totalTokens, agentID)
				if err != nil {
					return llm.CompletionResponse{}, err //nolint:wrapcheck // Middleware should pass through errors unchanged
				}
				defer release() // Always release slot, even on panic

				// Execute the request
				resp, err := next.Complete(ctx, req)

				return resp, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Stream implementation with rate limiting
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				// Get model configuration to determine provider
				modelName := next.GetModelName()

				// Get the appropriate rate limiter
				limiter, err := limiterMap.GetLimiter(modelName)
				if err != nil {
					return nil, err
				}

				// Estimate tokens needed (prompt + max output)
				promptTokens := estimator.EstimatePrompt(req)
				totalTokens := promptTokens + req.MaxTokens

				// Get agent ID for tracking
				agentID := stateProvider.GetID()

				// Acquire tokens and concurrency slot atomically
				release, err := limiter.Acquire(ctx, totalTokens, agentID)
				if err != nil {
					return nil, err //nolint:wrapcheck // Middleware should pass through errors unchanged
				}
				defer release() // Always release slot, even on panic

				// Execute the request
				return next.Stream(ctx, req)
			},
			// Delegate GetDefaultConfig to the next client
			func() string {
				return next.GetModelName()
			},
		)
	}
}
