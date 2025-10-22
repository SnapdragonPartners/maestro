package chat

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

const (
	// DefaultMaxMessageChars is the default maximum length for a chat message.
	DefaultMaxMessageChars = 4096

	// TruncationSuffix is appended to messages that exceed the max length.
	TruncationSuffix = " … [truncated]"
)

// Service provides chat functionality with secret scanning and cursor management.
type Service struct {
	dbOps   *persistence.DatabaseOperations
	scanner SecretScanner
	config  *config.ChatConfig
	logger  *logx.Logger
}

// NewService creates a new chat service.
func NewService(dbOps *persistence.DatabaseOperations, cfg *config.ChatConfig) *Service {
	logger := logx.NewLogger("chat")

	// Create scanner if enabled
	var scanner SecretScanner
	if cfg != nil && cfg.Scanner.Enabled {
		scanner = NewPatternScanner(cfg.Scanner.TimeoutMs)
		logger.Info("Chat secret scanner enabled (timeout: %dms)", cfg.Scanner.TimeoutMs)
	} else {
		logger.Warn("Chat secret scanner disabled")
	}

	return &Service{
		dbOps:   dbOps,
		scanner: scanner,
		config:  cfg,
		logger:  logger,
	}
}

// PostRequest represents a chat post request.
type PostRequest struct {
	Author string
	Text   string
}

// PostResponse represents a chat post response.
type PostResponse struct {
	ID      int64 `json:"id"`
	Success bool  `json:"success"`
}

// GetNewRequest represents a request to get new messages for an agent.
type GetNewRequest struct {
	AgentID string
}

// GetNewResponse represents the response with new messages.
type GetNewResponse struct {
	Messages   []*persistence.ChatMessage `json:"messages"`
	NewPointer int64                      `json:"newPointer"`
}

// Post creates a new chat message with size enforcement and secret redaction.
func (s *Service) Post(ctx context.Context, req *PostRequest) (*PostResponse, error) {
	if req.Author == "" {
		return nil, fmt.Errorf("author is required")
	}

	text := req.Text

	// 1. Enforce size limit
	maxChars := DefaultMaxMessageChars
	if s.config != nil && s.config.Limits.MaxMessageChars > 0 {
		maxChars = s.config.Limits.MaxMessageChars
	}

	if len(text) > maxChars {
		text = text[:maxChars-len(TruncationSuffix)] + TruncationSuffix
		s.logger.Debug("Truncated message from %s (original: %d chars, max: %d)", req.Author, len(req.Text), maxChars)
	}

	// 2. Apply secret scanning if enabled
	if s.scanner != nil {
		redacted, err := RedactSecrets(ctx, s.scanner, text)
		if err != nil {
			// Fail-open: log error and continue with original text
			s.logger.Error("Secret scanner failed for message from %s: %v (using original text)", req.Author, err)
		} else {
			text = redacted
		}
	}

	// 3. Persist to database
	timestamp := time.Now().UTC().Format(time.RFC3339)
	id, err := s.dbOps.PostChatMessage(req.Author, text, timestamp)
	if err != nil {
		return nil, fmt.Errorf("failed to persist chat message: %w", err)
	}

	s.logger.Debug("Posted chat message id=%d author=%s length=%d", id, req.Author, len(text))

	return &PostResponse{
		ID:      id,
		Success: true,
	}, nil
}

// GetNew retrieves new messages for an agent since their last cursor position.
func (s *Service) GetNew(_ context.Context, req *GetNewRequest) (*GetNewResponse, error) {
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	// 1. Get current cursor position
	cursor, err := s.dbOps.GetChatCursor(req.AgentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cursor: %w", err)
	}

	// 2. Retrieve messages since cursor
	messages, err := s.dbOps.GetChatMessages(cursor)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	// 3. Calculate new pointer
	newPointer := cursor
	if len(messages) > 0 {
		newPointer = messages[len(messages)-1].ID
	}

	s.logger.Debug("GetNew for %s: cursor=%d, messages=%d, newPointer=%d", req.AgentID, cursor, len(messages), newPointer)

	return &GetNewResponse{
		Messages:   messages,
		NewPointer: newPointer,
	}, nil
}

// UpdateCursor updates an agent's cursor to a new position.
func (s *Service) UpdateCursor(_ context.Context, agentID string, newPointer int64) error {
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}

	err := s.dbOps.UpdateChatCursor(agentID, newPointer)
	if err != nil {
		return fmt.Errorf("failed to update cursor: %w", err)
	}

	s.logger.Debug("Updated cursor for %s to %d", agentID, newPointer)
	return nil
}

// FormatAuthor ensures the author is in the correct format (@agent-id or @human).
func FormatAuthor(agentID string) string {
	if agentID == "human" {
		return "@human"
	}
	if strings.HasPrefix(agentID, "@") {
		return agentID
	}
	return "@" + agentID
}
