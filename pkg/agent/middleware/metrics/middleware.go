// Package metrics provides metrics middleware for LLM clients.
package metrics

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
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
func Middleware(recorder Recorder, usageExtractor UsageExtractor, stateProvider StateProvider, _ /* logger */ *logx.Logger) llm.Middleware {
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

				// Enhanced logging for LLM calls with detailed metrics
				if err == nil {
					logx.Infof("LLM call to model '%s': latency %.3gs, request tokens: %s, response tokens: %s, total tokens: %s (agent: %s, story: %s, state: %s)",
						modelConfig.Name, duration.Seconds(), formatWithCommas(promptTokens), formatWithCommas(completionTokens), formatWithCommas(promptTokens+completionTokens), agentID, storyID, state)
				} else {
					// Use defaultLogger.Error instead of logx.Errorf to avoid return value check
					defaultLogger := logx.NewLogger("metrics")
					defaultLogger.Error("LLM call to model '%s' failed: latency %.3gs, request tokens: %s, response tokens: %s, error: %s (agent: %s, story: %s, state: %s, error_type: %s)",
						modelConfig.Name, duration.Seconds(), formatWithCommas(promptTokens), formatWithCommas(completionTokens), err.Error(), agentID, storyID, state, errorType)
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

				// Enhanced logging for streaming LLM calls
				if err == nil {
					logx.Infof("LLM stream to model '%s' started: setup latency %.3gs (agent: %s, story: %s, state: %s)",
						modelConfig.Name, duration.Seconds(), agentID, storyID, state)
				} else {
					// Use defaultLogger.Error instead of logx.Errorf to avoid return value check
					defaultLogger := logx.NewLogger("metrics")
					defaultLogger.Error("LLM stream to model '%s' failed: setup latency %.3gs, error: %s (agent: %s, story: %s, state: %s, error_type: %s)",
						modelConfig.Name, duration.Seconds(), err.Error(), agentID, storyID, state, errorType)
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

// formatWithCommas adds thousands separators to numbers for readability.
func formatWithCommas(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}

	str := fmt.Sprintf("%d", n)
	result := ""

	for i, char := range str {
		if i > 0 && (len(str)-i)%3 == 0 {
			result += ","
		}
		result += string(char)
	}

	return result
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
