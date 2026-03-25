// Package retry provides retry middleware for LLM clients.
package retry

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/logx"
)

// Middleware returns a middleware function that wraps an LLM client with retry logic.
// It will retry failed requests according to the configured policy, with exponential backoff.
func Middleware(policy *Policy, logger *logx.Logger) llm.Middleware {
	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with retry
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				var lastErr error

				for attempt := 1; attempt <= policy.Config.MaxAttempts; attempt++ {
					// Wait for backoff delay (except on first attempt)
					if attempt > 1 {
						delay := policy.CalculateDelay(attempt)
						logger.Warn("LLM retry %d/%d (backoff %v): %v", attempt, policy.Config.MaxAttempts, delay, lastErr)
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

				// If we exhausted retries on a retryable error, emit ServiceUnavailable
				// to signal the agent should enter SUSPEND state
				if policy.ShouldRetry(lastErr) {
					logger.Error("LLM retries exhausted (%d attempts): %v", policy.Config.MaxAttempts, lastErr)
					return llm.CompletionResponse{}, llmerrors.NewServiceUnavailableError(lastErr, policy.Config.MaxAttempts)
				}
				return llm.CompletionResponse{}, lastErr
			},
			// Stream implementation with retry
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				var lastErr error

				for attempt := 1; attempt <= policy.Config.MaxAttempts; attempt++ {
					// Wait for backoff delay (except on first attempt)
					if attempt > 1 {
						delay := policy.CalculateDelay(attempt)
						logger.Warn("LLM stream retry %d/%d (backoff %v): %v", attempt, policy.Config.MaxAttempts, delay, lastErr)
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

				// If we exhausted retries on a retryable error, emit ServiceUnavailable
				// to signal the agent should enter SUSPEND state
				if policy.ShouldRetry(lastErr) {
					logger.Error("LLM stream retries exhausted (%d attempts): %v", policy.Config.MaxAttempts, lastErr)
					return nil, llmerrors.NewServiceUnavailableError(lastErr, policy.Config.MaxAttempts)
				}
				return nil, lastErr
			},
			// Delegate GetDefaultConfig to the next client
			func() string {
				return next.GetModelName()
			},
		)
	}
}
