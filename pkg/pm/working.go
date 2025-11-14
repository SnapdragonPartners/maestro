package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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
	expertise, _ := d.stateData[StateKeyUserExpertise].(string)
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

	// On first turn, set up the interview context with bootstrap awareness
	if turnCount == 0 {
		if setupErr := d.setupInterviewContext(); setupErr != nil {
			d.logger.Warn("Failed to set up interview context: %v", setupErr)
			// Continue anyway - non-fatal
		}
	}

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
	if signal == "SPEC_PREVIEW" {
		// Transition to PREVIEW state for user review
		d.logger.Info("📋 PM transitioning to PREVIEW state for user review")
		return StatePreview, nil
	}

	// Handle AWAIT_USER signal - transition to AWAIT_USER state
	if signal == string(StateAwaitUser) {
		d.logger.Info("⏸️  PM transitioning to AWAIT_USER state")
		return StateAwaitUser, nil
	}

	// Stay in WORKING - PM continues interviewing/drafting
	return StateWorking, nil
}

// setupInterviewContext renders the interview start template with bootstrap awareness.
func (d *Driver) setupInterviewContext() error {
	d.logger.Info("📝 Setting up interview context")

	// Get expertise level
	expertise, _ := d.stateData[StateKeyUserExpertise].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get bootstrap requirements
	bootstrapReqs := d.GetBootstrapRequirements()

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Expertise": expertise,
		},
	}

	// Add bootstrap context if requirements detected
	if bootstrapReqs != nil && len(bootstrapReqs.MissingComponents) > 0 {
		templateData.Extra["BootstrapRequired"] = true
		templateData.Extra["MissingComponents"] = bootstrapReqs.MissingComponents
		templateData.Extra["DetectedPlatform"] = bootstrapReqs.DetectedPlatform
		templateData.Extra["PlatformConfidence"] = int(bootstrapReqs.PlatformConfidence * 100)
		templateData.Extra["HasRepository"] = !bootstrapReqs.NeedsGitRepo
		templateData.Extra["NeedsDockerfile"] = bootstrapReqs.NeedsDockerfile
		templateData.Extra["NeedsMakefile"] = bootstrapReqs.NeedsMakefile
		templateData.Extra["NeedsKnowledgeGraph"] = bootstrapReqs.NeedsKnowledgeGraph

		d.logger.Info("📋 Bootstrap context included: %d missing components, platform: %s",
			len(bootstrapReqs.MissingComponents), bootstrapReqs.DetectedPlatform)
	}

	// Render interview start template
	interviewPrompt, err := d.renderer.Render(templates.PMInterviewStartTemplate, templateData)
	if err != nil {
		return fmt.Errorf("failed to render interview template: %w", err)
	}

	// Add as system message to guide the interview
	d.contextManager.AddMessage("system", interviewPrompt)

	d.logger.Info("✅ Interview context configured")
	return nil
}

// renderWorkingPrompt renders the PM working template with current state.
//
//nolint:unused // Reserved for future PM template rendering functionality
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
	// Use toolloop abstraction for LLM tool calling loop
	loop := toolloop.New(d.llmClient, d.logger)

	cfg := &toolloop.Config{
		ContextManager: d.contextManager,
		InitialPrompt:  prompt,
		ToolProvider:   d.toolProvider, // PM's tool provider
		MaxIterations:  10,
		MaxTokens:      agent.ArchitectMaxTokens, // TODO: Add PMMaxTokens constant to config
		AgentID:        d.pmID,                   // Agent ID for tool context
		DebugLogging:   true,                     // Enable for debugging: shows messages sent to LLM
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			// Process results and check for terminal signals
			return d.checkTerminalTools(ctx, calls, results)
		},
		OnIterationLimit: func(ctx context.Context) (string, error) {
			// Max iterations reached - ask LLM for update
			return d.handleIterationLimit(ctx)
		},
	}

	signal, err := loop.Run(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("toolloop execution failed: %w", err)
	}

	// Handle terminal signals from tool processing
	return signal, nil
}

// checkTerminalTools examines tool execution results for terminal signals.
// Returns non-empty signal to trigger state transition.
//
//nolint:cyclop // Multiple terminal conditions (bootstrap, spec_submit, await_user) adds complexity
func (d *Driver) checkTerminalTools(_ context.Context, calls []agent.ToolCall, results []any) string {
	// Track if we saw await_user signal
	sawAwaitUser := false

	for i := range calls {
		// Only process successful results
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			continue
		}

		// Check for errors in result
		if success, ok := resultMap["success"].(bool); ok && !success {
			continue // Skip error results
		}

		// Check for bootstrap_configured signal
		if bootstrapConfigured, ok := resultMap["bootstrap_configured"].(bool); ok && bootstrapConfigured {
			d.logger.Info("🔧 PM bootstrap tool succeeded, configuration saved")

			// Store bootstrap params in state for potential use
			bootstrapParams := make(map[string]string)
			if projectName, ok := resultMap["project_name"].(string); ok {
				bootstrapParams["project_name"] = projectName
			}
			if gitURL, ok := resultMap["git_url"].(string); ok {
				bootstrapParams["git_url"] = gitURL
			}
			if platform, ok := resultMap["platform"].(string); ok {
				bootstrapParams["platform"] = platform
			}
			d.stateData["bootstrap_params"] = bootstrapParams
			d.logger.Info("✅ Bootstrap params stored: project=%s, platform=%s, git=%s",
				bootstrapParams["project_name"], bootstrapParams["platform"], bootstrapParams["git_url"])

			// Don't return signal - continue loop for next tool call
		}

		// Check for spec_submit signal (PREVIEW flow)
		if previewReady, ok := resultMap["preview_ready"].(bool); ok && previewReady {
			d.logger.Info("📋 PM spec_submit succeeded, preparing for user preview")

			// Store draft spec and metadata for PREVIEW state
			if specMarkdown, ok := resultMap["spec_markdown"].(string); ok {
				d.stateData["draft_spec_markdown"] = specMarkdown
			}
			if metadata, ok := resultMap["metadata"].(map[string]any); ok {
				d.stateData["spec_metadata"] = metadata
			}

			// Return signal to transition to PREVIEW state
			return "SPEC_PREVIEW"
		}

		// Check for await_user signal
		if awaitUser, ok := resultMap["await_user"].(bool); ok && awaitUser {
			d.logger.Info("⏸️  PM await_user tool called")
			sawAwaitUser = true
		}
	}

	// If we saw await_user, return that signal
	if sawAwaitUser {
		return string(StateAwaitUser)
	}

	return "" // Continue loop
}

// handleIterationLimit is called when max iterations is reached.
// Asks LLM to provide update to user and returns AWAIT_USER signal.
func (d *Driver) handleIterationLimit(ctx context.Context) (string, error) {
	d.logger.Info("⚠️  PM reached max tool iterations, requesting update for user")

	// Add a system message prompting for user update
	d.contextManager.AddMessage("system-limit",
		"You've reached the tool call limit for this iteration. Please provide a brief update to the user about "+
			"what you've learned so far, and ask if they'd like you to continue gathering information "+
			"or if you have enough context to proceed. You can use more tools after the user responds.")

	// Flush and get final response
	if err := d.contextManager.FlushUserBuffer(ctx); err != nil {
		return "", fmt.Errorf("failed to flush buffer for limit message: %w", err)
	}

	messages := d.buildMessagesWithContext("")

	// Make one final call for the update (no tools)
	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: agent.ArchitectMaxTokens,
		Tools:     nil,
	}

	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to get user update after iteration limit: %w", err)
	}

	// Add response to context
	d.contextManager.AddAssistantMessage(resp.Content)

	// Post update to chat
	if resp.Content != "" && d.chatService != nil {
		d.logger.Info("📤 Posting PM iteration limit update to chat (%d chars)", len(resp.Content))
		postResp, postErr := d.chatService.Post(ctx, &chat.PostRequest{
			Author:  fmt.Sprintf("@%s", d.pmID),
			Text:    resp.Content,
			Channel: chat.ChannelProduct,
		})
		if postErr != nil {
			d.logger.Error("❌ Failed to post update to chat: %v", postErr)
		} else {
			d.logger.Info("✅ Posted iteration limit update to chat (id: %d)", postResp.ID)
		}
	}

	// Return AWAIT_USER to transition and wait for user response
	return string(StateAwaitUser), nil
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
