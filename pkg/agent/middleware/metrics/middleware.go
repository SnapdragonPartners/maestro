// Package metrics provides metrics middleware for LLM clients.
package metrics

import (
	"context"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

const (
	statusSuccess = "success"
	statusError   = "error"
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
func Middleware(recorder Recorder, usageExtractor UsageExtractor, stateProvider StateProvider, logger *logx.Logger) llm.Middleware {
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

				// Get current agent state for metrics
				storyID := stateProvider.GetStoryID()
				agentID := stateProvider.GetID()
				state := string(stateProvider.GetCurrentState())

				// Record metrics
				recorder.ObserveRequest(
					modelConfig.Name,
					storyID,
					agentID,
					state,
					promptTokens,
					completionTokens,
					err == nil,
					errorType,
					duration,
				)

				// Debug logging for token usage
				if logger != nil {
					status := statusSuccess
					if err != nil {
						status = statusError
					}
					totalTokens := promptTokens + completionTokens
					logger.Info("🎯 LLM Request: model=%s story=%s state=%s tokens=%d+%d=%d status=%s duration=%dms",
						modelConfig.Name, storyID, state, promptTokens, completionTokens, totalTokens, status, duration.Milliseconds())
				}

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

				// Get current agent state for metrics
				storyID := stateProvider.GetStoryID()
				agentID := stateProvider.GetID()
				state := string(stateProvider.GetCurrentState())

				// Record metrics (no token counts for streaming)
				recorder.ObserveRequest(
					modelConfig.Name,
					storyID,
					agentID,
					state,
					0, // No prompt token count for streaming
					0, // No completion token count for streaming
					err == nil,
					errorType,
					duration,
				)

				// Debug logging for stream requests
				if logger != nil {
					status := statusSuccess
					if err != nil {
						status = statusError
					}
					logger.Info("🎯 LLM Stream: model=%s story=%s state=%s tokens=streaming status=%s duration=%dms",
						modelConfig.Name, storyID, state, status, duration.Milliseconds())
				}

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
