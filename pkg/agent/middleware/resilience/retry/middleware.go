// Package retry provides retry middleware for LLM clients.
package retry

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
)

// Middleware returns a middleware function that wraps an LLM client with retry logic.
// It will retry failed requests according to the configured policy, with exponential backoff.
func Middleware(policy *Policy) llm.Middleware {
	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with retry
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				var lastErr error

				for attempt := 1; attempt <= policy.Config.MaxAttempts; attempt++ {
					// Wait for backoff delay (except on first attempt)
					if attempt > 1 {
						delay := policy.CalculateDelay(attempt)
						if delay > 0 {
							select {
							case <-ctx.Done():
								return llm.CompletionResponse{}, fmt.Errorf("retry cancelled: %w", ctx.Err())
							case <-time.After(delay):
								// Continue with retry
							}
						}
					}

					// Attempt the request
					resp, err := next.Complete(ctx, req)
					if err == nil {
						return resp, nil
					}

					lastErr = err

					// Check if we should retry this error
					if !policy.ShouldRetry(err) {
						break
					}

					// If this is the last attempt, don't sleep
					if attempt >= policy.Config.MaxAttempts {
						break
					}
				}

				return llm.CompletionResponse{}, fmt.Errorf("failed after %d attempts: %w",
					policy.Config.MaxAttempts, lastErr)
			},
			// Stream implementation with retry
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				var lastErr error

				for attempt := 1; attempt <= policy.Config.MaxAttempts; attempt++ {
					// Wait for backoff delay (except on first attempt)
					if attempt > 1 {
						delay := policy.CalculateDelay(attempt)
						if delay > 0 {
							select {
							case <-ctx.Done():
								return nil, fmt.Errorf("stream retry cancelled: %w", ctx.Err())
							case <-time.After(delay):
								// Continue with retry
							}
						}
					}

					// Attempt the request
					ch, err := next.Stream(ctx, req)
					if err == nil {
						return ch, nil
					}

					lastErr = err

					// Check if we should retry this error
					if !policy.ShouldRetry(err) {
						break
					}

					// If this is the last attempt, don't sleep
					if attempt >= policy.Config.MaxAttempts {
						break
					}
				}

				return nil, fmt.Errorf("failed to establish stream after %d attempts: %w",
					policy.Config.MaxAttempts, lastErr)
			},
			// Delegate GetDefaultConfig to the next client
			func() config.Model {
				return next.GetDefaultConfig()
			},
		)
	}
}
