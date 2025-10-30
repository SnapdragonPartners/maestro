// Package logging provides logging middleware for LLM clients.
package logging

import (
	"context"
	"strings"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// EmptyResponseLoggingMiddleware returns a middleware function that logs comprehensive
// debugging information when empty responses are encountered, then passes the error through unchanged.
// This helps debug empty response issues across all agent types and states without affecting behavior.
func EmptyResponseLoggingMiddleware() llm.Middleware {
	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with empty response logging
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				resp, err := next.Complete(ctx, req)

				// If this is an empty response error, log comprehensive debugging info
				if err != nil && llmerrors.Is(err, llmerrors.ErrorTypeEmptyResponse) {
					logEmptyResponseDebugInfo(req)
				}

				//nolint:wrapcheck // Middleware intentionally passes through errors unchanged
				return resp, err
			},
			// Stream implementation - pass through unchanged (empty responses less common in streaming)
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			// Delegate GetDefaultConfig to the next client
			func() string {
				return next.GetModelName()
			},
		)
	}
}

// logEmptyResponseDebugInfo logs comprehensive debugging information for empty LLM responses.
//
//nolint:gocritic // 80 bytes is reasonable for logging function
func logEmptyResponseDebugInfo(req llm.CompletionRequest) {
	logger := logx.NewLogger("llm-middleware") // Create logger for middleware

	logger.Error("🚨 EMPTY RESPONSE FROM LLM - DEBUGGING INFO:")

	// Log the complete prompt/messages sent to LLM
	logger.Error("📝 Complete prompt sent to LLM:")
	logger.Error("================================================================================")

	for i := range req.Messages {
		msg := &req.Messages[i]
		// Limit extremely long messages but show substantial content
		content := msg.Content
		if len(content) > 10000 {
			content = content[:10000] + "\n\n[... message truncated after 10000 characters for log readability ...]"
		}
		logger.Error("Message [%d] Role: %s, Content: %s", i, msg.Role, content)
	}

	logger.Error("================================================================================")

	// Log request details
	logger.Error("🔍 Request Details:")
	logger.Error("  - Temperature: %v", req.Temperature)
	logger.Error("  - Max Tokens: %d", req.MaxTokens)
	logger.Error("  - Tools Count: %d", len(req.Tools))

	if len(req.Tools) > 0 {
		logger.Error("  - Available Tools: %s", strings.Join(getToolNames(req.Tools), ", "))
	}

	logger.Error("🚨 END EMPTY RESPONSE DEBUG")
}

// getToolNames extracts tool names from tool definitions for logging.
func getToolNames(toolDefs []tools.ToolDefinition) []string {
	names := make([]string, len(toolDefs))
	for i := range toolDefs {
		names[i] = toolDefs[i].Name
	}
	return names
}
