// Package validation provides response validation middleware for LLM clients.
package validation

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/config"
	"orchestrator/pkg/tools"
)

// AgentType represents the type of agent for validation purposes.
type AgentType string

const (
	// AgentTypeArchitect represents an architect agent (doesn't use tools).
	AgentTypeArchitect AgentType = "architect"
	// AgentTypeCoder represents a coder agent (should always use tools).
	AgentTypeCoder AgentType = "coder"
)

// EmptyResponseValidator provides agent-aware validation and fallback guidance for LLM responses.
type EmptyResponseValidator struct {
	agentType AgentType
}

// NewEmptyResponseValidator creates a new validator configured for the specified agent type.
func NewEmptyResponseValidator(agentType AgentType) *EmptyResponseValidator {
	return &EmptyResponseValidator{
		agentType: agentType,
	}
}

// Middleware returns a middleware function that validates LLM responses and provides fallback guidance.
//
// For empty responses (retry pattern):
// - First occurrence: Adds guidance message to request, retries immediately
// - Second occurrence: Returns ErrorTypeEmptyResponse for state handler to process
//
// Agent-specific behavior:
// - Architect agents: Only truly empty content is considered invalid
// - Coder agents: Any response without tool calls is considered invalid.
func (v *EmptyResponseValidator) Middleware() llm.Middleware {
	return func(next llm.LLMClient) llm.LLMClient {
		return llm.WrapClient(
			// Complete implementation with agent-aware validation and retry with guidance
			func(ctx context.Context, req llm.CompletionRequest) (llm.CompletionResponse, error) {
				// Track empty response attempts (max 2: original + 1 retry with guidance)
				const maxEmptyAttempts = 2

				// Remove unused variables - we track success/failure in the loop directly

				for attempt := 1; attempt <= maxEmptyAttempts; attempt++ {
					// Make the request
					resp, err := next.Complete(ctx, req)

					// If there's a non-empty-response error, pass it through immediately
					if err != nil && !llmerrors.Is(err, llmerrors.ErrorTypeEmptyResponse) {
						//nolint:wrapcheck // Middleware intentionally passes through errors unchanged
						return resp, err
					}

					// Response stored in resp, error in err - no need to track separately

					// Check if this response should be considered empty
					// (either from ErrorTypeEmptyResponse error or from our validation)
					isEmpty := err != nil || v.isEmptyResponse(resp, req)

					if !isEmpty {
						// Good response, return it
						return resp, nil
					}

					// Empty response detected
					if attempt == 1 {
						// First attempt: add guidance and retry
						guidanceMessage := v.createGuidanceMessage(req)

						// Create a modified request with guidance as an additional user message
						modifiedReq := req
						modifiedReq.Messages = append(modifiedReq.Messages, llm.CompletionMessage{
							Role:    llm.RoleUser,
							Content: guidanceMessage,
						})

						// Update req for next iteration
						req = modifiedReq
						continue
					}

					// Second attempt failed - return error for state handler to process
					break
				}

				// Both attempts failed - return ErrorTypeEmptyResponse
				emptyErr := llmerrors.NewError(
					llmerrors.ErrorTypeEmptyResponse,
					"received inadequate response after guidance: no meaningful content or tool usage",
				)
				return llm.CompletionResponse{}, emptyErr
			},
			// Stream implementation - pass through unchanged (validation less applicable to streaming)
			func(ctx context.Context, req llm.CompletionRequest) (<-chan llm.StreamChunk, error) {
				return next.Stream(ctx, req)
			},
			// Delegate GetDefaultConfig to the next client
			func() config.Model {
				return next.GetDefaultConfig()
			},
		)
	}
}

// isEmptyResponse determines if a response should be considered "empty" based on agent type and content.
// Uses the logic: if len(toolCalls) == 0 { if !isArchitect || len(content) == 0 { return true } }.
func (v *EmptyResponseValidator) isEmptyResponse(resp llm.CompletionResponse, _ llm.CompletionRequest) bool {
	// If there are tool calls, response is not empty
	if len(resp.ToolCalls) > 0 {
		return false
	}

	// No tool calls - apply agent-aware logic
	isArchitect := v.agentType == AgentTypeArchitect
	contentEmpty := strings.TrimSpace(resp.Content) == ""

	// Clean logic: if len(toolCalls) == 0 { if !isArchitect || len(content) == 0 { return true } }
	return !isArchitect || contentEmpty
}

// createGuidanceMessage creates appropriate fallback guidance based on agent type and available tools.
func (v *EmptyResponseValidator) createGuidanceMessage(req llm.CompletionRequest) string {
	if v.agentType == AgentTypeArchitect {
		return "Your response wasn't understood. Please provide a clear response with your analysis and decision."
	}

	// For coder agents, provide tool-specific guidance
	fallback := fmt.Sprintf("Responses without tool usage are invalid. Use one of the available tools such as %s or %s.",
		tools.ToolShell, tools.ToolAskQuestion)

	// Extract tool names from the request
	toolNames := extractToolNames(req.Tools)

	// Add completion-specific guidance based on available tools
	if contains(toolNames, tools.ToolDone) {
		fallback += fmt.Sprintf(" If you are finished, use the %s tool to send your work for testing and review.", tools.ToolDone)
	} else if contains(toolNames, tools.ToolSubmitPlan) {
		fallback += fmt.Sprintf(" If you are finished planning, use %s to send your plan to the architect for review.", tools.ToolSubmitPlan)
	}

	return fallback
}

// extractToolNames extracts tool names from tool definitions.
func extractToolNames(toolDefs []tools.ToolDefinition) []string {
	names := make([]string, len(toolDefs))
	for i := range toolDefs {
		names[i] = toolDefs[i].Name
	}
	return names
}

// contains checks if a slice contains a specific string.
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
