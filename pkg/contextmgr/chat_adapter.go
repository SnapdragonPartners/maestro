package contextmgr

import (
	"context"
	"fmt"

	"orchestrator/pkg/chat"
	"orchestrator/pkg/persistence"
)

// ChatServiceAdapter adapts pkg/chat.Service to contextmgr.ChatService interface.
type ChatServiceAdapter struct {
	service *chat.Service
}

// NewChatServiceAdapter creates an adapter that wraps a chat.Service.
func NewChatServiceAdapter(service *chat.Service) ChatService {
	if service == nil {
		return nil
	}
	return &ChatServiceAdapter{service: service}
}

// GetNew fetches new chat messages for an agent.
func (a *ChatServiceAdapter) GetNew(ctx context.Context, req *GetNewRequest) (*GetNewResponse, error) {
	// Convert contextmgr request to chat request
	chatReq := &chat.GetNewRequest{
		AgentID: req.AgentID,
	}

	// Call underlying chat service
	chatResp, err := a.service.GetNew(ctx, chatReq)
	if err != nil {
		return nil, fmt.Errorf("chat service GetNew failed: %w", err)
	}

	// Convert chat response to contextmgr response
	messages := make([]*ChatMessage, len(chatResp.Messages))
	for i, msg := range chatResp.Messages {
		messages[i] = convertChatMessage(msg)
	}

	return &GetNewResponse{
		Messages:   messages,
		NewPointer: chatResp.NewPointer,
	}, nil
}

// UpdateCursor updates an agent's chat cursor.
func (a *ChatServiceAdapter) UpdateCursor(ctx context.Context, agentID string, newPointer int64) error {
	if err := a.service.UpdateCursor(ctx, agentID, newPointer); err != nil {
		return fmt.Errorf("chat service UpdateCursor failed: %w", err)
	}
	return nil
}

// convertChatMessage converts persistence.ChatMessage to contextmgr.ChatMessage.
func convertChatMessage(msg *persistence.ChatMessage) *ChatMessage {
	return &ChatMessage{
		ID:        msg.ID,
		Author:    msg.Author,
		Text:      msg.Text,
		Channel:   msg.Channel,
		Timestamp: msg.Timestamp,
	}
}
