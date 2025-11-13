// Package toolloop provides a reusable abstraction for LLM tool calling loops.
// This pattern is used across PM, Architect, and Coder agents.
package toolloop

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/tools"
)

// ToolProvider interface defines what toolloop needs from a tool provider.
type ToolProvider interface {
	Get(name string) (tools.Tool, error)
	List() []tools.ToolMeta
}

// ToolLoop manages LLM interactions with tool calling.
// It handles the iteration loop, tool execution, and context management.
type ToolLoop struct {
	llmClient agent.LLMClient
	logger    *logx.Logger
}

// New creates a new ToolLoop instance.
func New(llmClient agent.LLMClient, logger *logx.Logger) *ToolLoop {
	return &ToolLoop{
		llmClient: llmClient,
		logger:    logger,
	}
}

// Config defines how the tool loop behaves.
//
//nolint:govet // fieldalignment: struct fields ordered for clarity over memory alignment
type Config struct {
	// Context management (passed in, not owned by ToolLoop)
	// Agent maintains ownership and may use different contexts per call (architect pattern)
	ContextManager *contextmgr.ContextManager

	// Tool configuration
	ToolProvider ToolProvider // Provider for tool execution

	// Callbacks
	// CheckTerminal is called after ALL tools in current turn execute
	// Agent checks results and returns signal if state transition needed
	// Returns empty string to continue loop, non-empty signal to exit
	CheckTerminal func(calls []agent.ToolCall, results []any) string

	// OnIterationLimit is called when MaxIterations reached
	// Returns signal for state transition (e.g., "REQUEST_BUDGET")
	OnIterationLimit func(ctx context.Context) (string, error)

	// Maximum tool call iterations
	MaxIterations int

	// Maximum tokens per LLM request
	MaxTokens int

	// Debug settings
	DebugLogging bool // Enable detailed debug logging for message formatting

	// Initial prompt to add as user message (optional - may already be in context)
	// If empty, uses existing context
	InitialPrompt string

	// Agent identification (optional - required for tools that need agent context)
	// If set, will be added to context as tools.AgentIDContextKey when executing tools
	AgentID string
}

// Run executes the tool loop with the given configuration.
// Returns a signal string that the caller uses for state transitions.
// Empty signal = normal completion, non-empty = state transition requested.
func (tl *ToolLoop) Run(ctx context.Context, cfg *Config) (string, error) {
	// Validate configuration
	if cfg.ContextManager == nil {
		return "", fmt.Errorf("ContextManager is required")
	}
	if cfg.ToolProvider == nil {
		return "", fmt.Errorf("ToolProvider is required")
	}
	if cfg.MaxIterations <= 0 {
		cfg.MaxIterations = 10 // Default
	}
	if cfg.MaxTokens <= 0 {
		cfg.MaxTokens = 4096 // Default
	}

	// Add initial prompt if provided
	if cfg.InitialPrompt != "" {
		cfg.ContextManager.AddMessage("user", cfg.InitialPrompt)
	}

	// Get tool definitions
	toolsList := cfg.ToolProvider.List()
	toolDefs := make([]tools.ToolDefinition, len(toolsList))
	for i := range toolsList {
		toolDefs[i] = tools.ToolDefinition{
			Name:        toolsList[i].Name,
			Description: toolsList[i].Description,
			InputSchema: toolsList[i].InputSchema,
		}
	}

	// Main iteration loop
	for iteration := 0; iteration < cfg.MaxIterations; iteration++ {
		// Flush user buffer before LLM request
		if err := cfg.ContextManager.FlushUserBuffer(ctx); err != nil {
			return "", fmt.Errorf("failed to flush user buffer: %w", err)
		}

		// Build messages from context
		messages := buildMessages(cfg.ContextManager)

		// Create LLM request
		req := agent.CompletionRequest{
			Messages:  messages,
			MaxTokens: cfg.MaxTokens,
			Tools:     toolDefs,
		}

		// Log request details
		tl.logger.Info("🔄 Starting LLM call to model '%s' with %d messages, %d max tokens, %d tools (iteration %d)",
			tl.llmClient.GetModelName(), len(messages), req.MaxTokens, len(toolDefs), iteration+1)

		// DEBUG: Log the actual messages being sent to LLM if debug logging enabled
		if cfg.DebugLogging {
			tl.logMessages(messages)
		}

		// Call LLM
		start := time.Now()
		resp, err := tl.llmClient.Complete(ctx, req)
		duration := time.Since(start)

		if err != nil {
			tl.logger.Error("❌ LLM call failed after %.3gs: %v", duration.Seconds(), err)
			return "", fmt.Errorf("LLM completion failed: %w", err)
		}

		tl.logger.Info("✅ LLM call completed in %.3gs, response length: %d chars, tool calls: %d",
			duration.Seconds(), len(resp.Content), len(resp.ToolCalls))

		// Add assistant response to context with structured tool calls
		if len(resp.ToolCalls) > 0 {
			// Convert agent.ToolCall to contextmgr.ToolCall
			toolCalls := make([]contextmgr.ToolCall, len(resp.ToolCalls))
			for i := range resp.ToolCalls {
				toolCalls[i] = contextmgr.ToolCall{
					ID:         resp.ToolCalls[i].ID,
					Name:       resp.ToolCalls[i].Name,
					Parameters: resp.ToolCalls[i].Parameters,
				}
			}
			cfg.ContextManager.AddAssistantMessageWithTools(resp.Content, toolCalls)
		} else {
			// No tool calls - just content
			cfg.ContextManager.AddAssistantMessage(resp.Content)
		}

		// If no tool calls, return content as signal
		if len(resp.ToolCalls) == 0 {
			return resp.Content, nil
		}

		// Execute ALL tools (API requirement: every tool_use must have tool_result)
		tl.logger.Info("Processing %d tool calls", len(resp.ToolCalls))
		results := make([]any, len(resp.ToolCalls))
		for i := range resp.ToolCalls {
			toolCall := &resp.ToolCalls[i]
			tl.logger.Info("Executing tool: %s", toolCall.Name)

			// Get tool from provider
			tool, err := cfg.ToolProvider.Get(toolCall.Name)
			if err != nil {
				tl.logger.Error("Failed to get tool %s: %v", toolCall.Name, err)
				results[i] = map[string]any{
					"success": false,
					"error":   err.Error(),
				}
				// Add error result to context
				cfg.ContextManager.AddToolResult(toolCall.ID, err.Error(), true)
				continue
			}

			// Execute tool with agent context if provided
			toolCtx := ctx
			if cfg.AgentID != "" {
				toolCtx = context.WithValue(ctx, tools.AgentIDContextKey, cfg.AgentID)
			}

			start := time.Now()
			result, err := tool.Exec(toolCtx, toolCall.Parameters)
			duration := time.Since(start)

			if err != nil {
				tl.logger.Error("Tool %s failed after %.3fs: %v", toolCall.Name, duration.Seconds(), err)
				results[i] = map[string]any{
					"success": false,
					"error":   err.Error(),
				}
			} else {
				tl.logger.Info("Tool %s completed in %.3fs", toolCall.Name, duration.Seconds())
				results[i] = result
			}

			// Add tool result to context
			resultStr, isError := formatToolResult(result, err)
			cfg.ContextManager.AddToolResult(toolCall.ID, resultStr, isError)
		}

		// Check if any tool signals state transition
		var signal string
		if cfg.CheckTerminal != nil {
			signal = cfg.CheckTerminal(resp.ToolCalls, results)
		}

		// If signal returned, exit loop and return signal
		if signal != "" {
			tl.logger.Info("✅ Tool execution signaled state transition: %s", signal)
			return signal, nil
		}

		// Continue iteration
		tl.logger.Info("🔄 Tools executed, continuing iteration")
	}

	// Iteration limit reached
	tl.logger.Warn("⚠️  Maximum tool iterations (%d) reached", cfg.MaxIterations)
	if cfg.OnIterationLimit != nil {
		return cfg.OnIterationLimit(ctx)
	}

	return "", fmt.Errorf("maximum tool iterations (%d) exceeded", cfg.MaxIterations)
}

// buildMessages converts context manager messages to agent.CompletionMessage format.
func buildMessages(cm *contextmgr.ContextManager) []agent.CompletionMessage {
	contextMessages := cm.GetMessages()

	messages := make([]agent.CompletionMessage, 0, len(contextMessages))
	for i := range contextMessages {
		msg := &contextMessages[i]

		// Convert contextmgr.ToolCall to agent.ToolCall
		var agentToolCalls []agent.ToolCall
		if len(msg.ToolCalls) > 0 {
			agentToolCalls = make([]agent.ToolCall, len(msg.ToolCalls))
			for j := range msg.ToolCalls {
				agentToolCalls[j] = agent.ToolCall{
					ID:         msg.ToolCalls[j].ID,
					Name:       msg.ToolCalls[j].Name,
					Parameters: msg.ToolCalls[j].Parameters,
				}
			}
		}

		// Convert contextmgr.ToolResult to agent.ToolResult
		var agentToolResults []agent.ToolResult
		if len(msg.ToolResults) > 0 {
			agentToolResults = make([]agent.ToolResult, len(msg.ToolResults))
			for j := range msg.ToolResults {
				agentToolResults[j] = agent.ToolResult{
					ToolCallID: msg.ToolResults[j].ToolCallID,
					Content:    msg.ToolResults[j].Content,
					IsError:    msg.ToolResults[j].IsError,
				}
			}
		}

		messages = append(messages, agent.CompletionMessage{
			Role:        agent.CompletionRole(msg.Role),
			Content:     msg.Content,
			ToolCalls:   agentToolCalls,
			ToolResults: agentToolResults,
		})
	}

	return messages
}

// formatToolResult converts tool execution result to string format for context.
func formatToolResult(result any, err error) (string, bool) {
	if err != nil {
		return fmt.Sprintf("Tool failed: %v", err), true
	}

	// Check if result is a map with success field
	if resultMap, ok := result.(map[string]any); ok {
		if success, ok := resultMap["success"].(bool); ok && !success {
			// Error result
			if errMsg, ok := resultMap["error"].(string); ok {
				return errMsg, true
			}
			return fmt.Sprintf("Tool failed: %v", result), true
		}
	}

	// Success - convert to string
	return fmt.Sprintf("%v", result), false
}

// logMessages logs detailed message information for debugging.
func (tl *ToolLoop) logMessages(messages []agent.CompletionMessage) {
	tl.logger.Info("📝 DEBUG - Messages sent to LLM:")
	for i := range messages {
		msg := &messages[i]
		contentPreview := msg.Content
		if len(contentPreview) > 100 {
			contentPreview = contentPreview[:100] + "..."
		}

		// Show tool calls and results in addition to content
		toolInfo := ""
		if len(msg.ToolCalls) > 0 {
			toolInfo = fmt.Sprintf(", ToolCalls: %d", len(msg.ToolCalls))
		}
		if len(msg.ToolResults) > 0 {
			toolInfo += fmt.Sprintf(", ToolResults: %d", len(msg.ToolResults))
		}

		tl.logger.Info("  [%d] Role: %s, Content: %q%s", i, msg.Role, contentPreview, toolInfo)

		// Log tool calls inline with assistant messages
		if len(msg.ToolCalls) > 0 {
			for j := range msg.ToolCalls {
				tc := &msg.ToolCalls[j]
				tl.logger.Info("    ToolCall[%d] ID=%s Name=%s Params=%v", j, tc.ID, tc.Name, tc.Parameters)
			}
		}

		// Log tool results inline with user messages
		if len(msg.ToolResults) > 0 {
			for j := range msg.ToolResults {
				tr := &msg.ToolResults[j]
				resultPreview := tr.Content
				if len(resultPreview) > 200 {
					resultPreview = resultPreview[:200] + "..."
				}
				tl.logger.Info("    ToolResult[%d] ID=%s IsError=%v Content=%q", j, tr.ToolCallID, tr.IsError, resultPreview)
			}
		}
	}
}
