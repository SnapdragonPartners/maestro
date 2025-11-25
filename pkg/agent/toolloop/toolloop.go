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

// TerminalTool represents a tool that signals loop completion.
// Every toolloop must have exactly one terminal tool that defines the exit condition.
// This enforces the "one goal, one exit" principle at compile time.
//
// Terminal tools SHOULD return ProcessEffect with Signal and Data in their Exec() method.
// If a terminal tool is called without returning ProcessEffect, toolloop will auto-wrap it
// with a generic "TERMINAL_COMPLETE" signal to avoid runtime errors (since we can't enforce
// ProcessEffect at compile time).
//
// The generic type TResult is no longer used for extraction - kept for backwards compatibility.
// TODO: Remove TResult generic parameter once all code migrated to ProcessEffect pattern.
type TerminalTool[TResult any] interface {
	tools.Tool
	// Terminal tools implement Exec() to return ProcessEffect with signal and structured data
}

// EscalationHandler is called when the hard iteration limit is reached.
// It should handle escalation (e.g., notify humans, post to chat) and return an error.
type EscalationHandler func(ctx context.Context, key string, count int) error

// IterationLimitError is returned when the hard iteration limit is exceeded.
// This is a normal termination condition (like io.EOF), not a failure.
// Callers should check for this error type and handle it as a control-flow branch.
type IterationLimitError struct {
	Key       string
	Limit     int
	Iteration int
}

func (e *IterationLimitError) Error() string {
	return fmt.Sprintf("iteration limit (%d) exceeded for key %q at iteration %d",
		e.Limit, e.Key, e.Iteration)
}

// EscalationConfig defines iteration limits and escalation behavior.
//
//nolint:govet // Function pointers are logically grouped with their limits
type EscalationConfig struct {
	// Key uniquely identifies this loop instance for iteration tracking.
	// Example: "approval_story-123" or "question_req-456".
	Key string

	// SoftLimit is the warning threshold (e.g., 8 iterations).
	// OnSoftLimit callback is invoked when this limit is reached.
	SoftLimit int

	// HardLimit is the escalation threshold (e.g., 16 iterations).
	// OnHardLimit callback is invoked when this limit is reached.
	HardLimit int

	// OnSoftLimit is called when SoftLimit is reached (optional).
	// Use this for logging warnings or metrics.
	OnSoftLimit func(count int)

	// OnHardLimit is called when HardLimit is reached (required).
	// Should handle escalation and return error to stop the loop.
	OnHardLimit EscalationHandler
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
// Generic over result type T for type-safe result extraction.
//
// The new architecture enforces "one goal, one exit" by requiring exactly one TerminalTool.
// GeneralTools can be called multiple times, but only the TerminalTool signals completion.
//
//nolint:govet // fieldalignment: struct fields ordered for clarity over memory alignment
type Config[T any] struct {
	// Context management (passed in, not owned by ToolLoop)
	// Agent maintains ownership and may use different contexts per call (architect pattern)
	ContextManager *contextmgr.ContextManager

	// Tool configuration
	GeneralTools []tools.Tool    // Non-terminal tools (can be called multiple times)
	TerminalTool TerminalTool[T] // Exactly one terminal tool (signals completion and extracts result)

	// Escalation configuration for iteration limit handling (optional but recommended)
	// When provided, enables soft/hard limit tracking with callbacks
	Escalation *EscalationConfig

	// Maximum tool call iterations
	MaxIterations int

	// Maximum tokens per LLM request
	MaxTokens int

	// Debug settings
	DebugLogging bool // Enable detailed debug logging for message formatting

	// Single-turn mode: Expect terminal tool call in first iteration (allows retry/nudge but no multi-turn iteration)
	// When true, terminal tool MUST be called after first LLM response
	// Used for reviews and approvals that should complete in one interaction
	SingleTurn bool

	// Initial prompt to add as user message (optional - may already be in context)
	// If empty, uses existing context
	InitialPrompt string

	// Agent identification (optional - required for tools that need agent context)
	// If set, will be added to context as tools.AgentIDContextKey when executing tools
	AgentID string
}

// Run executes the tool loop with ProcessEffect-based terminal signaling, returning an Outcome[T].
//
// The Outcome contains:
// - Kind: What happened (ProcessEffect, IterationLimit, LLMError, etc.)
// - Signal: State transition signal (e.g., "PLAN_REVIEW", "TESTING") when Kind == OutcomeProcessEffect
// - EffectData: Structured data from ProcessEffect.Data when Kind == OutcomeProcessEffect
// - Err: Underlying error for error outcomes (nil for OutcomeProcessEffect)
// - Iteration: 1-indexed iteration count when outcome occurred
//
// Callers should switch on out.Kind first, then examine Signal/EffectData inside OutcomeProcessEffect branch.
//
// Usage:
//
//	out := toolloop.Run[CodingResult](tl, ctx, cfg)
//	switch out.Kind {
//	case toolloop.OutcomeProcessEffect:
//	    // Handle out.Signal and extract data from out.EffectData
//	case toolloop.OutcomeIterationLimit:
//	    // Handle budget review escalation
//	// ...
//	}
//
//nolint:godot // Type parameter T in comment confuses godot linter
func Run[T any](tl *ToolLoop, ctx context.Context, cfg *Config[T]) Outcome[T] {
	// Validate configuration
	if cfg.ContextManager == nil {
		return Outcome[T]{Kind: OutcomeLLMError, Err: fmt.Errorf("ContextManager is required")}
	}
	if cfg.TerminalTool == nil {
		return Outcome[T]{Kind: OutcomeLLMError, Err: fmt.Errorf("TerminalTool is required - every toolloop must have exactly one terminal tool")}
	}

	// Build internal tool provider from GeneralTools + TerminalTool
	terminalToolName := cfg.TerminalTool.Name()
	allTools := make([]tools.Tool, 0, len(cfg.GeneralTools)+1)
	allTools = append(allTools, cfg.GeneralTools...)
	allTools = append(allTools, cfg.TerminalTool)
	toolProvider := newSimpleProvider(allTools)

	tl.logger.Info("Toolloop configured with %d general tools + 1 terminal tool (%s)", len(cfg.GeneralTools), terminalToolName)

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
	toolsList := toolProvider.List()
	if len(toolsList) == 0 {
		return Outcome[T]{Kind: OutcomeLLMError, Err: fmt.Errorf("toolloop requires at least one tool to function")}
	}
	toolDefs := make([]tools.ToolDefinition, len(toolsList))
	for i := range toolsList {
		toolDefs[i] = tools.ToolDefinition{
			Name:        toolsList[i].Name,
			Description: toolsList[i].Description,
			InputSchema: toolsList[i].InputSchema,
		}
	}

	// Track consecutive turns without tool use
	consecutiveNoToolTurns := 0

	// Main iteration loop
	for iteration := 0; iteration < cfg.MaxIterations; iteration++ {
		currentIteration := iteration + 1 // 1-indexed for user-facing logs

		// Flush user buffer before LLM request
		if err := cfg.ContextManager.FlushUserBuffer(ctx); err != nil {
			return Outcome[T]{
				Kind:      OutcomeLLMError,
				Err:       fmt.Errorf("failed to flush user buffer: %w", err),
				Iteration: currentIteration,
			}
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
			tl.llmClient.GetModelName(), len(messages), req.MaxTokens, len(toolDefs), currentIteration)

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
			return Outcome[T]{
				Kind:      OutcomeLLMError,
				Err:       fmt.Errorf("LLM completion failed: %w", err),
				Iteration: currentIteration,
			}
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

		// Handle no tool calls - this is problematic for unattended operation
		if len(resp.ToolCalls) == 0 {
			consecutiveNoToolTurns++
			tl.logger.Warn("⚠️  No tools used in LLM response (consecutive count: %d)", consecutiveNoToolTurns)

			if consecutiveNoToolTurns == 1 {
				// First time - remind LLM to use tools and continue
				tl.logger.Info("📝 Adding reminder to use tools")
				reminderMsg := "No tools were used in your last call. Reasoning explanations are welcome, but make sure to use tools in your next call to advance the work."
				cfg.ContextManager.AddMessage("user", reminderMsg)
				continue // Continue loop with reminder
			}

			// Second consecutive time - return OutcomeNoToolTwice
			tl.logger.Error("❌ LLM failed to use tools after reminder - OutcomeNoToolTwice")
			return Outcome[T]{
				Kind:      OutcomeNoToolTwice,
				Signal:    "ERROR", // Legacy compatibility - agents can check this
				Err:       fmt.Errorf("LLM did not use tools after reminder (consecutive no-tool turns: %d)", consecutiveNoToolTurns),
				Iteration: currentIteration,
			}
		}

		// Tools were used - reset counter
		if consecutiveNoToolTurns > 0 {
			tl.logger.Info("✅ Tools used again, resetting no-tool counter")
			consecutiveNoToolTurns = 0
		}

		// Execute ALL tools (API requirement: every tool_use must have tool_result)
		tl.logger.Info("Processing %d tool calls", len(resp.ToolCalls))
		var pendingEffect *tools.ProcessEffect

		for i := range resp.ToolCalls {
			toolCall := &resp.ToolCalls[i]
			tl.logger.Info("Executing tool: %s", toolCall.Name)

			// Get tool from provider
			tool, err := toolProvider.Get(toolCall.Name)
			if err != nil {
				tl.logger.Error("Failed to get tool %s: %v", toolCall.Name, err)
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
			execResult, err := tool.Exec(toolCtx, toolCall.Parameters)
			duration := time.Since(start)

			// Generate tool result content for LLM context
			var content string
			var isError bool
			if err != nil {
				tl.logger.Error("Tool %s failed after %.3fs: %v", toolCall.Name, duration.Seconds(), err)
				content = fmt.Sprintf("Tool failed: %v", err)
				isError = true
			} else {
				tl.logger.Info("Tool %s completed in %.3fs", toolCall.Name, duration.Seconds())

				// Use provided content or generate simple acknowledgment
				if execResult.Content != "" {
					content = execResult.Content
				} else {
					content = "Tool executed successfully"
				}
				isError = false

				// Check if this tool wants to exit the loop for async effect processing
				if execResult.ProcessEffect != nil && execResult.ProcessEffect.Signal != "" {
					tl.logger.Info("🔔 Tool %s returned ProcessEffect with signal: %s", toolCall.Name, execResult.ProcessEffect.Signal)
					pendingEffect = execResult.ProcessEffect
				}
			}

			// Add tool result to context
			cfg.ContextManager.AddToolResult(toolCall.ID, content, isError)
		}

		// Check if any tool requested loop exit for effect processing
		if pendingEffect != nil {
			tl.logger.Info("🛑 Exiting toolloop early - ProcessEffect signal: %s", pendingEffect.Signal)
			var zero T
			return Outcome[T]{
				Kind:       OutcomeProcessEffect,
				Signal:     pendingEffect.Signal,
				EffectData: pendingEffect.Data,
				Value:      zero,
				Iteration:  currentIteration,
			}
		}

		// Check if terminal tool was called
		// Terminal tools should use ProcessEffect pattern - they would have exited above
		// If terminal tool was called but didn't return ProcessEffect, auto-wrap with generic signal
		for i := range resp.ToolCalls {
			if resp.ToolCalls[i].Name == terminalToolName {
				// Terminal tool was called but didn't return ProcessEffect
				// Auto-wrap to avoid runtime errors (we can't enforce ProcessEffect at compile time)
				tl.logger.Warn("⚠️  Terminal tool '%s' was called but did not return ProcessEffect - auto-wrapping with TERMINAL_COMPLETE signal", terminalToolName)
				var zero T
				return Outcome[T]{
					Kind:       OutcomeProcessEffect,
					Signal:     "TERMINAL_COMPLETE",
					EffectData: map[string]any{"tool": terminalToolName},
					Value:      zero,
					Iteration:  currentIteration,
				}
			}
		}

		// SingleTurn mode: terminal tool must have been called
		if cfg.SingleTurn {
			tl.logger.Error("❌ SingleTurn mode: Terminal tool was not called after first tool execution")
			return Outcome[T]{
				Kind:      OutcomeExtractionError,
				Signal:    "ERROR",
				Err:       fmt.Errorf("single-turn mode requires terminal tool to be called in first iteration"),
				Iteration: currentIteration,
			}
		}

		// Check escalation limits before continuing iteration
		if cfg.Escalation != nil {
			// Check soft limit (warning only, continues execution)
			if cfg.Escalation.SoftLimit > 0 && currentIteration == cfg.Escalation.SoftLimit {
				tl.logger.Warn("⚠️  Soft iteration limit (%d) reached for key '%s'", cfg.Escalation.SoftLimit, cfg.Escalation.Key)
				if cfg.Escalation.OnSoftLimit != nil {
					cfg.Escalation.OnSoftLimit(currentIteration)
				}
			}

			// Check hard limit (stops execution immediately)
			if cfg.Escalation.HardLimit > 0 && currentIteration >= cfg.Escalation.HardLimit {
				tl.logger.Error("❌ Hard iteration limit (%d) reached for key '%s' - escalating", cfg.Escalation.HardLimit, cfg.Escalation.Key)
				if cfg.Escalation.OnHardLimit != nil {
					err := cfg.Escalation.OnHardLimit(ctx, cfg.Escalation.Key, currentIteration)
					if err != nil {
						return Outcome[T]{
							Kind:      OutcomeLLMError, // Handler failure is treated as system error
							Err:       fmt.Errorf("escalation handler failed: %w", err),
							Iteration: currentIteration,
						}
					}
				}
				// Return OutcomeIterationLimit with typed error preserved for backwards compatibility
				return Outcome[T]{
					Kind: OutcomeIterationLimit,
					Err: &IterationLimitError{
						Key:       cfg.Escalation.Key,
						Limit:     cfg.Escalation.HardLimit,
						Iteration: currentIteration,
					},
					Iteration: currentIteration,
				}
			}
		}

		// Continue iteration
		tl.logger.Info("🔄 Tools executed, continuing iteration")
	}

	// Iteration limit reached - no escalation configured or limits not reached yet
	tl.logger.Warn("⚠️  Maximum tool iterations (%d) reached", cfg.MaxIterations)

	return Outcome[T]{
		Kind:      OutcomeMaxIterations,
		Err:       fmt.Errorf("maximum tool iterations (%d) exceeded", cfg.MaxIterations),
		Iteration: cfg.MaxIterations,
	}
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

// simpleProvider is a simple in-memory tool provider built from a list of tools.
// Used by the new architecture to combine GeneralTools + TerminalTool into a single provider.
type simpleProvider struct {
	tools map[string]tools.Tool
	list  []tools.ToolMeta
}

// newSimpleProvider creates a provider from a list of tools.
func newSimpleProvider(toolList []tools.Tool) *simpleProvider {
	toolMap := make(map[string]tools.Tool, len(toolList))
	metaList := make([]tools.ToolMeta, len(toolList))

	for i, tool := range toolList {
		name := tool.Name()
		toolMap[name] = tool
		def := tool.Definition()
		metaList[i] = tools.ToolMeta{
			Name:        name,
			Description: def.Description,
			InputSchema: def.InputSchema,
		}
	}

	return &simpleProvider{
		tools: toolMap,
		list:  metaList,
	}
}

// Get retrieves a tool by name.
func (sp *simpleProvider) Get(name string) (tools.Tool, error) {
	tool, ok := sp.tools[name]
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}
	return tool, nil
}

// List returns metadata for all available tools.
func (sp *simpleProvider) List() []tools.ToolMeta {
	return sp.list
}
