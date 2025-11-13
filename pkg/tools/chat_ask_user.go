package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/chat"
)

// ChatAskUserTool allows PM agent to post a question to chat and wait for user response.
// This combines posting a message with blocking for user input.
type ChatAskUserTool struct {
	chatService *chat.Service
	agentID     string
}

// NewChatAskUserTool creates a new chat_ask_user tool instance.
func NewChatAskUserTool(chatService *chat.Service, agentID string) *ChatAskUserTool {
	return &ChatAskUserTool{
		chatService: chatService,
		agentID:     agentID,
	}
}

// Definition returns the tool's definition in Claude API format.
func (a *ChatAskUserTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "chat_ask_user",
		Description: "Post a question to chat and wait for user response. Use when you need user input before proceeding. For status updates that don't need a response, use chat_post instead.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"message": {
					Type:        "string",
					Description: "The question or request to post to chat",
				},
			},
			Required: []string{"message"},
		},
	}
}

// Name returns the tool identifier.
func (a *ChatAskUserTool) Name() string {
	return "chat_ask_user"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (a *ChatAskUserTool) PromptDocumentation() string {
	return `- **chat_ask_user** - Post a question to chat and wait for user response
  - Parameters:
    - message (string, required): The question or request to post
  - Use when you need user input to continue (clarification, decisions, more details)
  - Blocks until user responds
  - For non-blocking updates, use chat_post instead`
}

// Exec executes the chat_ask_user operation.
func (a *ChatAskUserTool) Exec(ctx context.Context, params map[string]any) (any, error) {
	// Extract message parameter
	message, ok := params["message"].(string)
	if !ok || message == "" {
		return nil, fmt.Errorf("message parameter is required")
	}

	// Post message to chat
	if a.chatService != nil {
		author := chat.FormatAuthor(a.agentID)
		req := &chat.PostRequest{
			Author:  author,
			Text:    message,
			Channel: "product",
		}
		_, err := a.chatService.Post(ctx, req)
		if err != nil {
			return nil, fmt.Errorf("failed to post message: %w", err)
		}
	}

	// Signal to PM driver to transition to AWAIT_USER state
	return map[string]any{
		"success":    true,
		"message":    "Question posted, waiting for user response",
		"await_user": true, // Signal to PM driver to pause until user responds
	}, nil
}
