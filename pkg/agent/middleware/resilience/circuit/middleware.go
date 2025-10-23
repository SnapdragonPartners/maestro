// Package circuit provides circuit breaker middleware for LLM clients.
package circuit

import (
	"context"

	"orchestrator/pkg/agent/llm"
)

// Middleware returns a middleware function that wraps an LLM client with circuit breaker logic.
// If the circuit is OPEN, requests are rejected immediately without calling the underlying client.
// This prevents cascading failures and gives the downstream service time to recover.
func Middleware(breaker Breaker) llm.Middleware {
	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				// Check if we can proceed
				if !breaker.Allow() {
					return llm.CompletionResponse{}, &Error{State: breaker.GetState()}
				}

				// Execute the request
				resp, err := next.Complete(ctx, req)

				// Record the result
				breaker.Record(err == nil)

				return resp, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Stream implementation
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				// Check if we can proceed
				if !breaker.Allow() {
					return nil, &Error{State: breaker.GetState()}
				}

				// Execute the request
				ch, err := next.Stream(ctx, req)

				// For streaming, we consider the initial establishment as success/failure.
				// Individual chunks are not tracked for circuit breaker state.
				breaker.Record(err == nil)

				return ch, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Delegate GetDefaultConfig to the next client
			func() string {
				return next.GetModelName()
			},
		)
	}
}
