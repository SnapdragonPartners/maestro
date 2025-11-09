package tools

import (
	"context"
	"fmt"

	"orchestrator/pkg/chat"
)

// ContextKeyAgentID is the context key type for agent ID.
type ContextKeyAgentID string

// AgentIDContextKey is the context key for storing agent ID.
const AgentIDContextKey ContextKeyAgentID = "agent_id"

// ChatPostTool posts a message to the agent chat channel.
type ChatPostTool struct {
	chatService *chat.Service
}

// NewChatPostTool creates a new ChatPostTool instance.
func NewChatPostTool(chatService *chat.Service) *ChatPostTool {
	return &ChatPostTool{
		chatService: chatService,
	}
}

// Definition returns the tool definition for chat_post.
func (c *ChatPostTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "chat_post",
		Description: "Post a message to the agent chat channel. Use this to communicate with other agents or ask questions in the shared chat.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"text": {
					Type:        "string",
					Description: "The message text to post to the chat channel",
				},
			},
			Required: []string{"text"},
		},
	}
}

// Name returns the tool name.
func (c *ChatPostTool) Name() string {
	return "chat_post"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ChatPostTool) PromptDocumentation() string {
	return `- **chat_post** - Post a message to the agent chat channel
  - Parameters: text (required)
  - Use to communicate with other agents or ask questions in shared chat
  - Messages are visible to all agents in the current session`
}

// Exec posts a message to the chat channel.
func (c *ChatPostTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract text parameter
	text, ok := args["text"]
	if !ok {
		return nil, fmt.Errorf("text parameter is required")
	}

	textStr, ok := text.(string)
	if !ok {
		return nil, fmt.Errorf("text must be a string")
	}

	if textStr == "" {
		return nil, fmt.Errorf("text cannot be empty")
	}

	// Get agent ID from context (set by the agent when calling tools)
	agentID, ok := ctx.Value(AgentIDContextKey).(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("agent_id not found in context")
	}

	// Format author with @ prefix
	author := chat.FormatAuthor(agentID)

	// Determine channel based on agent type (simple heuristic for now)
	// PM agents use 'product', all others use 'development'
	channel := "development"
	// TODO: This should be configured more robustly - perhaps store channel in context or agent config

	// Post the message
	req := &chat.PostRequest{
		Author:  author,
		Text:    textStr,
		Channel: channel,
	}

	resp, err := c.chatService.Post(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to post chat message: %w", err)
	}

	return map[string]any{
		"success": true,
		"message": fmt.Sprintf("Message posted successfully (id: %d)", resp.ID),
		"id":      resp.ID,
	}, nil
}

// ChatReadTool retrieves new messages from the agent chat channel.
type ChatReadTool struct {
	chatService *chat.Service
}

// NewChatReadTool creates a new ChatReadTool instance.
func NewChatReadTool(chatService *chat.Service) *ChatReadTool {
	return &ChatReadTool{
		chatService: chatService,
	}
}

// Definition returns the tool definition for chat_read.
func (c *ChatReadTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "chat_read",
		Description: "Read new messages from the agent chat channel since your last read. Returns messages and updates your read cursor automatically.",
		InputSchema: InputSchema{
			Type:       "object",
			Properties: map[string]Property{},
			Required:   []string{},
		},
	}
}

// Name returns the tool name.
func (c *ChatReadTool) Name() string {
	return "chat_read"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ChatReadTool) PromptDocumentation() string {
	return `- **chat_read** - Read new messages from the agent chat channel
  - No parameters required
  - Returns messages since your last read, automatically updates cursor
  - Use to check for messages from other agents or the architect`
}

// Exec retrieves new messages and updates the cursor.
func (c *ChatReadTool) Exec(ctx context.Context, _ map[string]any) (any, error) {
	// Get agent ID from context
	agentID, ok := ctx.Value(AgentIDContextKey).(string)
	if !ok || agentID == "" {
		return nil, fmt.Errorf("agent_id not found in context")
	}

	// Get new messages
	req := &chat.GetNewRequest{
		AgentID: agentID,
	}

	resp, err := c.chatService.GetNew(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("failed to get new messages: %w", err)
	}

	// Update cursor to new pointer
	if err := c.chatService.UpdateCursor(ctx, agentID, resp.NewPointer); err != nil {
		return nil, fmt.Errorf("failed to update cursor: %w", err)
	}

	// Format response
	if len(resp.Messages) == 0 {
		return map[string]any{
			"success": true,
			"message": "No new messages",
			"count":   0,
		}, nil
	}

	// Build message list
	messages := make([]map[string]any, len(resp.Messages))
	for i, msg := range resp.Messages {
		messages[i] = map[string]any{
			"timestamp": msg.Timestamp,
			"author":    msg.Author,
			"text":      msg.Text,
		}
	}

	return map[string]any{
		"success":  true,
		"message":  fmt.Sprintf("Found %d new message(s)", len(resp.Messages)),
		"count":    len(resp.Messages),
		"messages": messages,
	}, nil
}
