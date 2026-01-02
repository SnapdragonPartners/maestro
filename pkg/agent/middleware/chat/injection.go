// Package chat provides middleware for injecting chat messages into LLM requests.
package chat

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

// Service interface defines what we need from the chat service.
type Service interface {
	GetNew(ctx context.Context, req *chat.GetNewRequest) (*chat.GetNewResponse, error)
	UpdateCursor(ctx context.Context, agentID string, newPointer int64) error
}

// ContextManager interface defines what we need from the context manager.
type ContextManager interface {
	AddMessage(provenance, content string)
}

// WrapWithChatInjection wraps an existing LLM client with chat injection middleware.
// This is used to add chat injection to already-constructed clients with full middleware chains.
// If contextManager is provided, injected messages will be persisted to maintain conversation history.
func WrapWithChatInjection(client agent.LLMClient, chatService Service, agentID string, logger *logx.Logger, contextManager ContextManager) agent.LLMClient {
	if chatService == nil {
		// No chat service - return client unchanged
		return client
	}

	// Apply chat injection middleware
	return llm.Chain(client, InjectionMiddleware(chatService, agentID, logger, contextManager))
}

// InjectionMiddleware creates middleware that injects new chat messages into LLM requests.
// It fetches new messages before each LLM call and prepends them to the conversation context.
// After fetching messages, it updates the cursor so messages aren't repeatedly injected.
// If contextManager is provided, injected messages are also added to it for persistence.
//
//nolint:funlen // Middleware requires both Complete and Stream implementations
func InjectionMiddleware(chatService Service, agentID string, logger *logx.Logger, contextManager ContextManager) func(agent.LLMClient) agent.LLMClient {
	// Shared logic for fetching and formatting chat messages
	fetchAndFormatMessages := func(ctx context.Context) (*chat.GetNewResponse, agent.CompletionMessage, bool, error) {
		// Get configuration to check if chat is enabled and get limits
		cfg, err := config.GetConfig()
		if err != nil {
			logger.Warn("Failed to get config for chat injection: %v", err)
			return nil, agent.CompletionMessage{}, false, fmt.Errorf("failed to get config: %w", err)
		}

		// Check if chat is enabled
		if cfg.Chat == nil || !cfg.Chat.Enabled {
			return nil, agent.CompletionMessage{}, false, nil
		}

		// Fetch new messages
		maxMessages := cfg.Chat.MaxNewMessages
		if maxMessages <= 0 {
			maxMessages = 100 // Default if not configured
		}

		resp, err := chatService.GetNew(ctx, &chat.GetNewRequest{AgentID: agentID})
		if err != nil {
			logger.Warn("Failed to fetch new chat messages for %s: %v", agentID, err)
			return nil, agent.CompletionMessage{}, false, fmt.Errorf("failed to fetch messages: %w", err)
		}

		newMessages := resp.Messages

		// If no new messages, skip injection
		if len(newMessages) == 0 {
			return resp, agent.CompletionMessage{}, false, nil
		}

		// Update cursor so these messages won't be injected again
		if err := chatService.UpdateCursor(ctx, agentID, resp.NewPointer); err != nil {
			logger.Warn("Failed to update chat cursor for %s: %v", agentID, err)
			// Continue anyway - message injection is best-effort
		}

		// Limit to MaxNewMessages (most recent)
		if len(newMessages) > maxMessages {
			newMessages = newMessages[len(newMessages)-maxMessages:]
		}

		// Filter out confirmation-related messages (these are operational, not conversational)
		filteredMessages := make([]*persistence.ChatMessage, 0, len(newMessages))
		for _, msg := range newMessages {
			if chat.IsConfirmationPostType(msg.PostType) {
				continue
			}
			filteredMessages = append(filteredMessages, msg)
		}

		// If all messages were filtered out, skip injection
		if len(filteredMessages) == 0 {
			return resp, agent.CompletionMessage{}, false, nil
		}

		// Format chat messages for injection
		var chatContent strings.Builder
		chatContent.WriteString("## Recent Chat Messages\n\n")
		chatContent.WriteString("The following messages were posted to the agent chat system:\n\n")

		for _, msg := range filteredMessages {
			chatContent.WriteString(fmt.Sprintf("**%s**: %s\n\n", msg.Author, msg.Text))
		}

		chatContent.WriteString("You may respond to these messages using the `chat_send` tool if appropriate.\n")

		// Create injected message
		injectedMessage := agent.CompletionMessage{
			Role:    agent.RoleUser, // Use user role for system instructions
			Content: chatContent.String(),
		}

		// Persist to context manager if provided (so messages persist across turns)
		if contextManager != nil {
			contextManager.AddMessage("chat", chatContent.String())
			logger.Debug("💾 Persisted %d chat messages to context manager for %s", len(newMessages), agentID)
		}

		return resp, injectedMessage, true, nil
	}

	return func(next agent.LLMClient) agent.LLMClient {
		return llm.WrapClient(
			// Complete function with chat injection
			func(ctx context.Context, req agent.CompletionRequest) (agent.CompletionResponse, error) {
				// Check if chat service is available
				if chatService == nil {
					return next.Complete(ctx, req)
				}

				resp, injectedMessage, shouldInject, err := fetchAndFormatMessages(ctx)
				if err != nil || !shouldInject {
					// Continue without chat injection on error or no messages
					return next.Complete(ctx, req)
				}

				// Prepend to existing messages
				modifiedReq := req
				modifiedReq.Messages = append([]agent.CompletionMessage{injectedMessage}, req.Messages...)

				logger.Info("💬 Injected %d chat messages into LLM request for %s",
					len(resp.Messages), agentID)

				// Call next middleware with modified request
				return next.Complete(ctx, modifiedReq)
			},
			// Stream function with chat injection
			func(ctx context.Context, req agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
				// Check if chat service is available
				if chatService == nil {
					return next.Stream(ctx, req)
				}

				resp, injectedMessage, shouldInject, err := fetchAndFormatMessages(ctx)
				if err != nil || !shouldInject {
					// Continue without chat injection on error or no messages
					return next.Stream(ctx, req)
				}

				// Prepend to existing messages
				modifiedReq := req
				modifiedReq.Messages = append([]agent.CompletionMessage{injectedMessage}, req.Messages...)

				logger.Info("💬 Injected %d chat messages into LLM stream for %s",
					len(resp.Messages), agentID)

				// Call next middleware with modified request
				return next.Stream(ctx, modifiedReq)
			},
			// GetDefaultConfig passes through
			func() string {
				return next.GetModelName()
			},
		)
	}
}
