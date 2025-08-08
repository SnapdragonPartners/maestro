// Package metrics provides metrics middleware for LLM clients.
package metrics

import (
	"context"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/utils"
)

// UsageExtractor is a function that extracts token usage from a request and response.
type UsageExtractor func(req llm.CompletionRequest, resp llm.CompletionResponse) (promptTokens, completionTokens int)

// DefaultUsageExtractor provides a default implementation using TikToken for token counting.
func DefaultUsageExtractor(req llm.CompletionRequest, resp llm.CompletionResponse) (promptTokens, completionTokens int) {
	// Count prompt tokens from all messages
	var promptText string
	for i := range req.Messages {
		promptText += req.Messages[i].Content + "\n"
	}
	promptTokens = utils.CountTokensSimple(promptText)

	// Count completion tokens from response content
	completionTokens = utils.CountTokensSimple(resp.Content)

	return promptTokens, completionTokens
}

// Middleware returns a middleware function that records metrics for LLM operations.
// It tracks request latency, token usage, success/failure rates, and error types.
func Middleware(recorder Recorder, usageExtractor UsageExtractor, agentType string) llm.Middleware {
	if usageExtractor == nil {
		usageExtractor = DefaultUsageExtractor
	}

	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with metrics
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				start := time.Now()

				// Get model name for metrics
				modelConfig := next.GetDefaultConfig()

				resp, err := next.Complete(ctx, req)
				duration := time.Since(start)

				// Extract token usage
				var promptTokens, completionTokens int
				if err == nil {
					promptTokens, completionTokens = usageExtractor(req, resp)
				}

				// Determine error type
				errorType := ""
				if err != nil {
					errorType = getErrorType(err)
				}

				// Record metrics
				recorder.ObserveRequest(
					modelConfig.Name,
					"complete",
					agentType,
					promptTokens,
					completionTokens,
					err == nil,
					errorType,
					duration,
				)

				return resp, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Stream implementation with metrics
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				start := time.Now()

				// Get model name for metrics
				modelConfig := next.GetDefaultConfig()

				ch, err := next.Stream(ctx, req)
				duration := time.Since(start)

				// For streaming, we only track the initial setup time and success/failure
				// Token counting for streams would require consuming the entire stream
				errorType := ""
				if err != nil {
					errorType = getErrorType(err)
				}

				// Record metrics (no token counts for streaming)
				recorder.ObserveRequest(
					modelConfig.Name,
					"stream",
					agentType,
					0, // No prompt token count for streaming
					0, // No completion token count for streaming
					err == nil,
					errorType,
					duration,
				)

				return ch, err //nolint:wrapcheck // Middleware should pass through errors unchanged
			},
			// Delegate GetDefaultConfig to the next client
			func() config.Model {
				return next.GetDefaultConfig()
			},
		)
	}
}

// getErrorType classifies errors for metrics labeling.
// This is a simple implementation - could be enhanced with more sophisticated error classification.
func getErrorType(err error) string {
	if err == nil {
		return ""
	}

	errStr := err.Error()
	switch {
	case errStr == "circuit breaker is OPEN" || errStr == "circuit breaker is HALF_OPEN":
		return "circuit_breaker"
	case errStr == "context deadline exceeded":
		return "timeout"
	case errStr == "context canceled":
		return "canceled"
	default:
		return "unknown"
	}
}
