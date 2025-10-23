// Package timeout provides timeout middleware for LLM clients.
package timeout

import (
	"context"
	"time"

	"orchestrator/pkg/agent/llm"
)

// Middleware returns a middleware function that wraps an LLM client with per-request timeout logic.
// Each request gets a timeout context to prevent hanging requests.
func Middleware(duration time.Duration) llm.Middleware {
	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with timeout
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				// Create timeout context for this request
				timeoutCtx, cancel := context.WithTimeout(ctx, duration)
				defer cancel()

				// Execute the request with timeout context
				resp, err := next.Complete(timeoutCtx, req)

				return resp, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Stream implementation with timeout
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				// Create timeout context for this request
				timeoutCtx, cancel := context.WithTimeout(ctx, duration)
				defer cancel()

				// Execute the request with timeout context
				return next.Stream(timeoutCtx, req)
			},
			// Delegate GetDefaultConfig to the next client
			func() string {
				return next.GetModelName()
			},
		)
	}
}
