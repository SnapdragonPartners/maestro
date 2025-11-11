package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

// handleWorking manages PM's active work: interviewing, drafting, and submitting.
// PM has access to all tools (chat_post, read_file, list_files, spec_submit) and decides
// when to transition back to WAITING by successfully calling submit_spec.
//
//nolint:revive,unparam // ctx will be used for LLM calls and cancellation handling
func (d *Driver) handleWorking(ctx context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM working (interviewing/drafting/submitting)")

	// Check for non-blocking architect feedback
	select {
	case resultMsg := <-d.replyCh:
		// Architect provided feedback asynchronously
		if resultMsg != nil {
			d.logger.Info("📨 Received async feedback from architect")
			// Store feedback in context for next LLM call
			d.stateData["architect_feedback"] = resultMsg
		}
	default:
		// No feedback yet, continue working
	}

	// Get conversation state
	turnCount, _ := d.stateData["turn_count"].(int)
	expertise, _ := d.stateData["expertise"].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get max turns from config
	cfg, err := config.GetConfig()
	if err != nil {
		return proto.StateError, fmt.Errorf("failed to get config: %w", err)
	}
	maxTurns := cfg.PM.MaxInterviewTurns

	d.logger.Info("PM working (turn %d/%d, expertise: %s)", turnCount, maxTurns, expertise)

	// System prompt was set at interview start - just use context manager's conversation
	// No need to render a new prompt every turn
	signal, err := d.callLLMWithTools(ctx, "")
	if err != nil {
		d.logger.Error("❌ PM LLM call failed: %v", err)
		return proto.StateError, fmt.Errorf("LLM call failed: %w", err)
	}

	// Increment turn count
	d.stateData["turn_count"] = turnCount + 1

	// Handle terminal signals from tool processing
	if signal == "SPEC_SUBMITTED" {
		// Send approval REQUEST to architect
		err := d.sendSpecApprovalRequest(ctx)
		if err != nil {
			d.logger.Error("❌ Failed to send spec approval request: %v", err)
			return proto.StateError, fmt.Errorf("failed to send approval request: %w", err)
		}

		// Store pending request ID and transition to WAITING
		// (Actual request ID will be set by sendSpecApprovalRequest)
		return StateWaiting, nil
	}

	// Handle AWAIT_USER signal - transition to AWAIT_USER state
	if signal == "AWAIT_USER" {
		d.logger.Info("⏸️  PM transitioning to AWAIT_USER state")
		return StateAwaitUser, nil
	}

	// Stay in WORKING - PM continues interviewing/drafting
	return StateWorking, nil
}

// renderWorkingPrompt renders the PM working template with current state.
func (d *Driver) renderWorkingPrompt() (string, error) {
	// Get conversation history from state
	conversationHistory, _ := d.stateData["conversation"].([]map[string]string)
	expertise, _ := d.stateData["expertise"].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get architect feedback if present
	architectFeedback, _ := d.stateData["architect_feedback"].(string)

	// Get draft spec if present
	draftSpec, _ := d.stateData["draft_spec"].(string)

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Expertise":           expertise,
			"ConversationHistory": conversationHistory,
			"ArchitectFeedback":   architectFeedback,
			"DraftSpec":           draftSpec,
		},
	}

	// Render template with user instructions
	prompt, err := d.renderer.RenderWithUserInstructions(
		templates.PMWorkingTemplate,
		templateData,
		d.workDir,
		"PM",
	)
	if err != nil {
		return "", fmt.Errorf("failed to render PM working template: %w", err)
	}

	return prompt, nil
}

// callLLMWithTools calls the LLM with PM tools in an iteration loop.
// Similar to architect's callLLMWithTools but with PM-specific tool handling.
//
//nolint:cyclop,maintidx // Complex tool iteration logic, refactoring would reduce readability
func (d *Driver) callLLMWithTools(ctx context.Context, prompt string) (string, error) {
	// Flush user buffer before LLM request
	if err := d.contextManager.FlushUserBuffer(ctx); err != nil {
		return "", fmt.Errorf("failed to flush user buffer: %w", err)
	}

	// Build messages with context
	messages := d.buildMessagesWithContext(prompt)

	// Get tool definitions from toolProvider
	toolsList := d.toolProvider.List()
	toolDefs := make([]tools.ToolDefinition, len(toolsList))
	for i := range toolsList {
		toolDefs[i] = tools.ToolDefinition(toolsList[i])
	}

	// Tool call iteration loop
	maxIterations := 10
	for iteration := 0; iteration < maxIterations; iteration++ {
		req := agent.CompletionRequest{
			Messages:  messages,
			MaxTokens: agent.ArchitectMaxTokens, // TODO: Add PMMaxTokens constant to config
			Tools:     toolDefs,
		}

		// Get LLM response
		d.logger.Info("🔄 Starting PM LLM call with %d messages, %d tools (iteration %d)",
			len(messages), len(toolDefs), iteration+1)

		// DEBUG: Log the actual messages being sent to LLM
		d.logger.Info("📝 DEBUG - Messages sent to LLM:")
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

			d.logger.Info("  [%d] Role: %s, Content: %q%s", i, msg.Role, contentPreview, toolInfo)

			// DEBUG: Log tool calls inline with assistant messages
			if len(msg.ToolCalls) > 0 {
				for j := range msg.ToolCalls {
					tc := &msg.ToolCalls[j]
					d.logger.Info("    ToolCall[%d] ID=%s Name=%s Params=%v", j, tc.ID, tc.Name, tc.Parameters)
				}
			}

			// DEBUG: Log tool results inline with user messages
			if len(msg.ToolResults) > 0 {
				for j := range msg.ToolResults {
					tr := &msg.ToolResults[j]
					resultPreview := tr.Content
					if len(resultPreview) > 200 {
						resultPreview = resultPreview[:200] + "..."
					}
					d.logger.Info("    ToolResult[%d] ID=%s IsError=%v Content=%q", j, tr.ToolCallID, tr.IsError, resultPreview)
				}
			}
		}

		start := time.Now()
		resp, err := d.llmClient.Complete(ctx, req)
		duration := time.Since(start)

		if err != nil {
			d.logger.Error("❌ PM LLM call failed after %.3gs: %v", duration.Seconds(), err)
			return "", fmt.Errorf("LLM completion failed: %w", err)
		}

		d.logger.Info("✅ PM LLM call completed in %.3gs, response length: %d chars, tool calls: %d",
			duration.Seconds(), len(resp.Content), len(resp.ToolCalls))

		// DEBUG: Log response details
		if resp.Content != "" {
			contentPreview := resp.Content
			if len(contentPreview) > 100 {
				contentPreview = contentPreview[:100] + "..."
			}
			d.logger.Info("📝 DEBUG - Response content: %q", contentPreview)
		}
		if len(resp.ToolCalls) > 0 {
			d.logger.Info("📝 DEBUG - Tool calls:")
			for i := range resp.ToolCalls {
				tc := &resp.ToolCalls[i]
				d.logger.Info("  [%d] Tool: %s, Params: %+v", i, tc.Name, tc.Parameters)
			}
		}

		// Add assistant response to context with structured tool calls
		if len(resp.ToolCalls) > 0 {
			// Use structured tool call tracking
			// Convert agent.ToolCall to contextmgr.ToolCall
			toolCalls := make([]contextmgr.ToolCall, len(resp.ToolCalls))
			for i := range resp.ToolCalls {
				toolCalls[i] = contextmgr.ToolCall{
					ID:         resp.ToolCalls[i].ID,
					Name:       resp.ToolCalls[i].Name,
					Parameters: resp.ToolCalls[i].Parameters,
				}
			}
			d.contextManager.AddAssistantMessageWithTools(resp.Content, toolCalls)
		} else {
			// No tool calls - just content
			d.contextManager.AddAssistantMessage(resp.Content)
		}

		// If no tool calls, post content to chat and transition to AWAIT_USER
		// (treat as implicit chat_ask_user)
		if len(resp.ToolCalls) == 0 {
			if resp.Content != "" && d.chatService != nil {
				d.logger.Info("📤 Posting PM response to chat (%d chars)", len(resp.Content))
				// Post PM's message to chat in product channel
				postResp, postErr := d.chatService.Post(ctx, &chat.PostRequest{
					Author:  fmt.Sprintf("@%s", d.pmID),
					Text:    resp.Content,
					Channel: chat.ChannelProduct, // PM interviews happen in product channel
				})
				if postErr != nil {
					d.logger.Error("❌ Failed to post PM response to chat: %v", postErr)
					// Continue anyway - chat posting is best-effort
				} else {
					d.logger.Info("✅ Posted PM message to chat (id: %d)", postResp.ID)
				}
			} else if resp.Content == "" {
				d.logger.Warn("⚠️  PM response has no content, skipping chat post")
			} else if d.chatService == nil {
				d.logger.Warn("⚠️  Chat service not configured, skipping chat post")
			}
			// Return AWAIT_USER signal to transition state
			return string(StateAwaitUser), nil
		}

		// Process tool calls
		signal, err := d.processPMToolCalls(ctx, resp.ToolCalls)
		if err != nil {
			return "", fmt.Errorf("tool processing failed: %w", err)
		}

		// If terminal tool was called, return signal
		if signal != "" {
			return signal, nil
		}

		// Flush tool results into user message before next iteration
		if err := d.contextManager.FlushUserBuffer(ctx); err != nil {
			return "", fmt.Errorf("failed to flush tool results: %w", err)
		}

		// Rebuild messages for next iteration
		messages = d.buildMessagesWithContext("")
	}

	// Max iterations reached - ask LLM to provide update to user
	d.logger.Info("⚠️  PM reached max tool iterations (%d), requesting update for user", maxIterations)

	// Add a system message prompting for user update
	d.contextManager.AddMessage("system-limit",
		"You've reached the tool call limit for this iteration. Please provide a brief update to the user about "+
			"what you've learned so far, and ask if they'd like you to continue gathering information "+
			"or if you have enough context to proceed. You can use more tools after the user responds.")

	// Flush and get final response
	if err := d.contextManager.FlushUserBuffer(ctx); err != nil {
		return "", fmt.Errorf("failed to flush buffer for limit message: %w", err)
	}

	messages = d.buildMessagesWithContext("")

	// Make one final call for the update
	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: agent.ArchitectMaxTokens,
		Tools:     nil, // No tools for this final update call
	}

	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get user update after iteration limit: %w", err)
	}

	// Add response to context
	d.contextManager.AddAssistantMessage(resp.Content)

	// Return the update content - PM will stay in WORKING state
	return resp.Content, nil
}

// processPMToolCalls processes tool calls from the LLM response.
// Returns a signal string if a terminal tool was called (e.g., "SPEC_SUBMITTED").
//
//nolint:unparam,cyclop // error return for consistency, complexity from tool result handling
func (d *Driver) processPMToolCalls(ctx context.Context, toolCalls []agent.ToolCall) (string, error) {
	for i := range toolCalls {
		toolCall := &toolCalls[i]
		d.logger.Info("🔧 PM executing tool: %s", toolCall.Name)

		// Get tool from provider
		tool, err := d.toolProvider.Get(toolCall.Name)
		if err != nil {
			d.logger.Error("❌ Failed to get tool %s: %v", toolCall.Name, err)
			// Add structured error result
			d.contextManager.AddToolResult(toolCall.ID, fmt.Sprintf("Tool %s not found: %v", toolCall.Name, err), true)
			continue
		}

		// Add agent_id to context for tool execution
		toolCtx := context.WithValue(ctx, tools.AgentIDContextKey, d.pmID)

		// Log tool parameters for debugging
		d.logger.Debug("Tool %s parameters: %+v", toolCall.Name, toolCall.Parameters)

		// Execute tool
		result, err := tool.Exec(toolCtx, toolCall.Parameters)
		if err != nil {
			d.logger.Error("❌ Tool %s execution failed: %v", toolCall.Name, err)
			// Add structured error result
			d.contextManager.AddToolResult(toolCall.ID, fmt.Sprintf("Tool %s error: %v", toolCall.Name, err), true)
			continue
		}

		// Check if result indicates an error (MCP tools return errors in result map)
		var resultStr string
		var isError bool
		if resultMap, ok := result.(map[string]any); ok {
			// Check for success field
			if success, ok := resultMap["success"].(bool); ok && !success {
				isError = true
				// Extract error message if present
				if errMsg, ok := resultMap["error"].(string); ok {
					resultStr = errMsg
				} else {
					resultStr = fmt.Sprintf("Tool failed: %v", result)
				}
			} else {
				// Success - convert entire result to string
				resultStr = fmt.Sprintf("%v", result)
			}
		} else {
			// Non-map result - convert to string
			resultStr = fmt.Sprintf("%v", result)
		}

		d.logger.Debug("Tool %s result (isError=%v): %s", toolCall.Name, isError, resultStr)
		// Add structured tool result with proper error flag
		d.contextManager.AddToolResult(toolCall.ID, resultStr, isError)

		// Log completion status
		if isError {
			d.logger.Error("❌ Tool %s failed: %s", toolCall.Name, resultStr)
		} else {
			d.logger.Info("✅ Tool %s completed successfully", toolCall.Name)
		}

		// Handle special tool signals (only for successful results)
		if !isError {
			if resultMap, ok := result.(map[string]any); ok {
				// Check for await_user signal
				if awaitUser, ok := resultMap["await_user"].(bool); ok && awaitUser {
					d.logger.Info("⏸️  PM await_user tool called")
					// Set flag to return AWAIT_USER after processing all tools
					d.stateData["pending_await_user"] = true
				}

				// Check for spec_submit signal
				if sendRequest, ok := resultMap["send_request"].(bool); ok && sendRequest {
					d.logger.Info("📤 PM spec_submit succeeded, storing spec_markdown")

					// Store spec_markdown for later use (architect feedback scenario)
					if specMarkdown, ok := resultMap["spec_markdown"].(string); ok {
						d.stateData["spec_markdown"] = specMarkdown
					}

					// Return signal to indicate spec was submitted
					return "SPEC_SUBMITTED", nil
				}
			}
		}
	}

	// After processing all tools, check if we should transition to AWAIT_USER
	if pendingAwait, ok := d.stateData["pending_await_user"].(bool); ok && pendingAwait {
		delete(d.stateData, "pending_await_user")
		return "AWAIT_USER", nil
	}

	return "", nil
}

// buildMessagesWithContext builds LLM messages with conversation history and context.
func (d *Driver) buildMessagesWithContext(prompt string) []agent.CompletionMessage {
	// Get conversation history from context manager
	// This includes chat messages injected by middleware
	contextMessages := d.contextManager.GetMessages()

	// Convert to CompletionMessage format
	messages := make([]agent.CompletionMessage, 0, len(contextMessages)+1)
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

	// Add the new prompt as a user message
	if prompt != "" {
		messages = append(messages, agent.CompletionMessage{
			Role:    agent.RoleUser,
			Content: prompt,
		})
	}

	return messages
}

// sendSpecApprovalRequest sends an approval REQUEST message to the architect.
func (d *Driver) sendSpecApprovalRequest(_ context.Context) error {
	// Get spec markdown from state
	specMarkdown, ok := d.stateData["spec_markdown"].(string)
	if !ok || specMarkdown == "" {
		return fmt.Errorf("no spec_markdown found in state")
	}

	// Create approval request payload
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeSpec,
		Content:      specMarkdown,
		Reason:       "PM has completed specification and requests architect review",
		Context:      "Specification ready for validation and story generation",
		Confidence:   proto.ConfidenceHigh,
	}

	// Create REQUEST message
	requestMsg := &proto.AgentMsg{
		ID:        fmt.Sprintf("pm-spec-req-%d", time.Now().UnixNano()),
		Type:      proto.MsgTypeREQUEST,
		FromAgent: d.pmID,
		ToAgent:   "architect-001", // TODO: Get architect ID from config or dispatcher
		Payload:   proto.NewApprovalRequestPayload(approvalPayload),
	}

	// Send via dispatcher
	if err := d.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch REQUEST: %w", err)
	}

	// Store pending request ID for tracking
	d.stateData["pending_request_id"] = requestMsg.ID
	d.logger.Info("📤 Sent spec approval REQUEST to architect (id: %s)", requestMsg.ID)

	return nil
}
