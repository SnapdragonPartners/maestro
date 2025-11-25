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
		Description: "Post a question to chat AND wait for user response. This tool both posts the message and blocks until user replies. Do NOT call chat_post before this - just call chat_ask_user with your question. For non-blocking status updates, use chat_post instead.",
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
	return `- **chat_ask_user** - Post a question to chat AND wait for user response
  - Parameters:
    - message (string, required): The question or request to post
  - This tool BOTH posts the message AND blocks until user responds
  - Do NOT call chat_post before this - just use chat_ask_user with your question
  - Use when you need user input to continue (clarification, decisions, more details)
  - For non-blocking status updates, use chat_post instead`
}

// Exec executes the chat_ask_user operation.
func (a *ChatAskUserTool) Exec(ctx context.Context, params map[string]any) (*ExecResult, error) {
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

	// Return ProcessEffect to pause the loop and transition to AWAIT_USER state
	// Similar to ask_question tool for coder, this pauses the PM loop for async user response
	return &ExecResult{
		Content: "Question posted to chat, waiting for user response",
		ProcessEffect: &ProcessEffect{
			Signal: SignalAwaitUser, // PM will handle this state transition
			Data: map[string]string{
				"message": message,
			},
		},
	}, nil
}
