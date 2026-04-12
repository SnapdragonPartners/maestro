package architect

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/tools"
)

// handleDevChat processes pending development channel chat messages.
// Each message gets its own LLM turn with context scoped to the sending agent's story.
// Replies are posted back as threaded messages.
func (d *Driver) handleDevChat(ctx context.Context) error {
	if d.devChatService == nil {
		return fmt.Errorf("dev chat service not configured")
	}

	// Fetch unread development-channel messages
	resp, err := d.devChatService.GetNewForChannel(ctx, d.GetAgentID(), chat.ChannelDevelopment)
	if err != nil {
		return fmt.Errorf("failed to get dev-chat messages: %w", err)
	}

	if len(resp.Messages) == 0 {
		return nil // Nothing to process
	}

	d.logger.Info("Dev-chat: processing %d new development messages", len(resp.Messages))

	// Process each message individually
	for _, msg := range resp.Messages {
		if err := d.processDevChatMessage(ctx, msg); err != nil {
			d.logger.Error("Dev-chat: failed to process message %d from %s: %v", msg.ID, msg.Author, err)
			// Continue processing remaining messages even if one fails
		}
	}

	// Update development channel cursor (leave product cursor untouched)
	if err := d.devChatService.UpdateCursorForChannel(ctx, d.GetAgentID(), chat.ChannelDevelopment, resp.NewPointer); err != nil {
		d.logger.Error("Dev-chat: failed to update cursor: %v", err)
	}

	return nil
}

// processDevChatMessage handles a single development chat message with an LLM turn.
func (d *Driver) processDevChatMessage(ctx context.Context, msg *persistence.ChatMessage) error {
	// Extract agent ID from author (strip @ prefix)
	agentID := strings.TrimPrefix(msg.Author, "@")
	if agentID == "" || agentID == "human" {
		// Human messages get a simple acknowledgment — no workspace to inspect
		return d.postDevChatReply(ctx, msg.ID, "Acknowledged. Let me know if you need anything specific.")
	}

	d.logger.Info("Dev-chat: processing message %d from %s: %.100s", msg.ID, agentID, msg.Text)

	// Scope context to the agent's current story (if any).
	// On failure, reset to a clean architect prompt to avoid stale/uninitialized context.
	storyID := d.dispatcher.GetStoryForAgent(agentID)
	if storyID != "" {
		if _, err := d.ensureContextForStory(ctx, agentID, storyID); err != nil {
			d.logger.Warn("Dev-chat: failed to scope context for %s story %s: %v — resetting to clean state", agentID, storyID, err)
			cm := d.getContextForAgent(agentID)
			cm.ResetForNewTemplate("", "You are the architect agent. Respond to the following dev-chat message.")
			d.clearReviewStreaks(agentID)
		}
	} else {
		// No active story lease — ensure context has at least a minimal system prompt
		cm := d.getContextForAgent(agentID)
		cm.ResetForNewTemplate("", "You are the architect agent. Respond to the following dev-chat message.")
	}

	// Get agent-specific context (now guaranteed to have a system prompt)
	cm := d.getContextForAgent(agentID)

	// Build prompt from the chat message
	prompt := fmt.Sprintf(`A developer (%s) posted in the development chat:

%s

Read the message, inspect their workspace if needed, then provide a helpful response using submit_reply.
Keep your response concise and actionable.`, agentID, msg.Text)

	cm.AddMessage("dev-chat-message", prompt)

	// Create tool provider with read tools for this agent's workspace
	toolProvider := d.createQuestionToolProviderForCoder(agentID)

	// Get submit_reply as terminal tool
	submitReplyTool, err := toolProvider.Get(tools.ToolSubmitReply)
	if err != nil {
		return fmt.Errorf("failed to get submit_reply tool: %w", err)
	}

	// Get general read tools
	var generalTools []tools.Tool
	for _, toolName := range []string{tools.ToolReadFile, tools.ToolListFiles} {
		if tool, toolErr := toolProvider.Get(toolName); toolErr == nil {
			generalTools = append(generalTools, tool)
		}
	}

	// Run toolloop — shorter limits than questions since chat replies should be quick
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitReplyResult]{
		ContextManager:     cm,
		GeneralTools:       generalTools,
		TerminalTool:       submitReplyTool,
		MaxIterations:      10,
		MaxTokens:          agent.ArchitectMaxTokens,
		Temperature:        config.GetTemperature(config.TempRoleArchitect),
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		StoryID:            storyID,
	})

	// Extract response
	var replyText string
	if out.Kind == toolloop.OutcomeProcessEffect && out.Signal == tools.SignalReplySubmitted {
		if effectData, ok := out.EffectData.(map[string]any); ok {
			if r, ok := effectData["response"].(string); ok {
				replyText = r
			}
		}
	}

	if replyText == "" {
		// Fallback: be honest that we couldn't process the message
		replyText = "I received your message but wasn't able to fully process it. If you need help, please use the ask_question tool for a reliable response."
		d.logger.Warn("Dev-chat: no submit_reply produced for message %d, using honest fallback", msg.ID)
	}

	// Post threaded reply back to development chat
	return d.postDevChatReply(ctx, msg.ID, replyText)
}

// postDevChatReply posts a threaded reply to a development chat message.
func (d *Driver) postDevChatReply(ctx context.Context, replyToID int64, text string) error {
	_, err := d.devChatService.Post(ctx, &chat.PostRequest{
		Author:   chat.FormatAuthor(d.GetAgentID()),
		Text:     text,
		Channel:  chat.ChannelDevelopment,
		ReplyTo:  &replyToID,
		PostType: chat.PostTypeReply,
	})
	if err != nil {
		return fmt.Errorf("failed to post dev-chat reply: %w", err)
	}
	d.logger.Info("Dev-chat: posted reply to message %d", replyToID)
	return nil
}
