// Package ratelimit provides rate limiting middleware for LLM clients.
package ratelimit

import (
	"context"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/config"
)

// Middleware returns a middleware function that wraps an LLM client with rate limiting.
// It estimates token usage and acquires tokens before making requests.
func Middleware(limiterMap *ProviderLimiterMap, estimator TokenEstimator, recorder metrics.Recorder) llm.Middleware {
	if estimator == nil {
		estimator = NewDefaultTokenEstimator()
	}

	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with rate limiting
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				// TODO: REMOVE DEBUG LOGGING - temporary debugging for middleware hang
				// Get model configuration to determine provider
				modelConfig := next.GetDefaultConfig()

				// Get the appropriate rate limiter
				limiter, err := limiterMap.GetLimiter(modelConfig.Name)
				if err != nil {
					// If we can't get a limiter, record throttle and fail
					recorder.IncThrottle(modelConfig.Name, "no_limiter")
					return llm.CompletionResponse{}, err
				}

				// Estimate tokens needed (prompt + max output)
				promptTokens := estimator.EstimatePrompt(req)
				totalTokens := promptTokens + req.MaxTokens

				// Acquire tokens
				if err2 := limiter.Acquire(ctx, totalTokens); err2 != nil {
					recorder.IncThrottle(modelConfig.Name, "rate_limit")
					return llm.CompletionResponse{}, err2 //nolint:wrapcheck // Middleware should pass through errors unchanged
				}

				// Execute the request
				resp, err := next.Complete(ctx, req)

				return resp, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Stream implementation with rate limiting
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				// Get model configuration to determine provider
				modelConfig := next.GetDefaultConfig()

				// Get the appropriate rate limiter
				limiter, err := limiterMap.GetLimiter(modelConfig.Name)
				if err != nil {
					// If we can't get a limiter, record throttle and fail
					recorder.IncThrottle(modelConfig.Name, "no_limiter")
					return nil, err
				}

				// Estimate tokens needed (prompt + max output)
				promptTokens := estimator.EstimatePrompt(req)
				totalTokens := promptTokens + req.MaxTokens

				// Acquire tokens
				if err := limiter.Acquire(ctx, totalTokens); err != nil {
					recorder.IncThrottle(modelConfig.Name, "rate_limit")
					return nil, err //nolint:wrapcheck // Middleware should pass through errors unchanged
				}

				// Execute the request
				return next.Stream(ctx, req)
			},
			// Delegate GetDefaultConfig to the next client
			func() config.Model {
				return next.GetDefaultConfig()
			},
		)
	}
}
