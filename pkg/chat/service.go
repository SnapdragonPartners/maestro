package chat

import (
	"context"
	"fmt"
	"strings"
	"sync"
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

	// ChannelProduct is the channel for PM interviews and product discussions.
	ChannelProduct = "product"
	// ChannelDevelopment is the channel for general development chat.
	ChannelDevelopment = "development"

	// PostTypeChat is a regular chat message.
	PostTypeChat = "chat"
	// PostTypeReply is a reply to another message.
	PostTypeReply = "reply"
	// PostTypeEscalate is an escalation message requiring human attention.
	PostTypeEscalate = "escalate"
	// PostTypeConfirmationRequest is a request for user confirmation.
	PostTypeConfirmationRequest = "confirmation_request"
	// PostTypeConfirmationContinue is a user response to continue.
	PostTypeConfirmationContinue = "confirmation_continue"
	// PostTypeConfirmationCancel is a user response to cancel/provide guidance.
	PostTypeConfirmationCancel = "confirmation_cancel"
)

// IsConfirmationPostType returns true if the post type is any confirmation-related type.
// These are operational messages that should not be injected into LLM context.
func IsConfirmationPostType(postType string) bool {
	return postType == PostTypeConfirmationRequest ||
		postType == PostTypeConfirmationContinue ||
		postType == PostTypeConfirmationCancel
}

// Service provides chat functionality with secret scanning and cursor management.
// Architecture: In-memory canonical state with database as append-only log.
//
//nolint:govet // fieldalignment not critical for singleton service
type Service struct {
	dbOps   *persistence.DatabaseOperations
	scanner SecretScanner
	config  *config.ChatConfig
	logger  *logx.Logger

	// In-memory canonical state
	messages     []*persistence.ChatMessage  // All messages (canonical source of truth)
	agentCursors map[string]map[string]int64 // agent_id -> channel -> cursor

	nextID int64 // Next message ID to assign
	mu     sync.RWMutex

	// Confirmation waiters - message ID -> channel to signal when reply arrives
	confirmationWaiters map[int64]chan *persistence.ChatMessage
	waitersMu           sync.Mutex
}

// NewService creates a new chat service with in-memory canonical state.
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
		dbOps:               dbOps,
		scanner:             scanner,
		config:              cfg,
		logger:              logger,
		messages:            make([]*persistence.ChatMessage, 0),
		agentCursors:        make(map[string]map[string]int64),
		nextID:              1, // Start at 1 (0 reserved for "no messages")
		confirmationWaiters: make(map[int64]chan *persistence.ChatMessage),
	}
}

// RegisterAgent initializes per-channel cursors for an agent.
// This establishes which channels the agent has access to.
// Access control is implicit: no cursor = no access to channel.
func (s *Service) RegisterAgent(agentID string, channels []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.agentCursors[agentID] == nil {
		s.agentCursors[agentID] = make(map[string]int64)
	}

	for _, channel := range channels {
		s.agentCursors[agentID][channel] = 0
		s.logger.Debug("Registered %s to channel %s", agentID, channel)
	}
}

// PostRequest represents a chat post request.
type PostRequest struct {
	Author   string
	Text     string
	Channel  string // Required: 'development', 'product', etc.
	ReplyTo  *int64 // Optional: ID of message being replied to
	PostType string // Optional: 'chat', 'reply', or 'escalate' (defaults to 'chat')
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
// Messages are appended to in-memory slice first (canonical state), then persisted async to DB.
func (s *Service) Post(ctx context.Context, req *PostRequest) (*PostResponse, error) {
	if req.Author == "" {
		return nil, fmt.Errorf("author is required")
	}
	if req.Channel == "" {
		return nil, fmt.Errorf("channel is required")
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

	// 3. Determine post type (default to 'chat')
	postType := req.PostType
	if postType == "" {
		postType = "chat"
	}

	// 4. Get session ID from config
	sessionID := ""
	if s.config != nil {
		// TODO: Get session_id from global config once available
		sessionID = "default" // Placeholder until config provides session_id
	}

	// 5. Assign ID and append to in-memory slice (canonical state)
	timestamp := time.Now().UTC().Format(time.RFC3339)
	s.mu.Lock()
	msgID := s.nextID
	s.nextID++

	msg := &persistence.ChatMessage{
		ID:        msgID,
		SessionID: sessionID,
		Channel:   req.Channel,
		Author:    req.Author,
		Text:      text,
		Timestamp: timestamp,
		ReplyTo:   req.ReplyTo,
		PostType:  postType,
	}

	s.messages = append(s.messages, msg)
	s.mu.Unlock()

	s.logger.Debug("Posted chat message id=%d author=%s channel=%s type=%s length=%d", msgID, req.Author, req.Channel, postType, len(text))

	// 6. Signal any waiters if this is a reply
	if req.ReplyTo != nil {
		s.signalWaiter(*req.ReplyTo, msg)
	}

	// 7. Async persist to database (fire-and-forget)
	go func() {
		_, err := s.dbOps.PostChatMessageWithType(req.Author, text, timestamp, req.Channel, req.ReplyTo, postType)
		if err != nil {
			s.logger.Warn("Failed to persist chat message to DB (id=%d): %v", msgID, err)
		}
	}()

	return &PostResponse{
		ID:      msgID,
		Success: true,
	}, nil
}

// GetNew retrieves new messages for an agent since their last cursor position.
// Returns all messages from all channels the agent has access to (filter in-memory).
func (s *Service) GetNew(_ context.Context, req *GetNewRequest) (*GetNewResponse, error) {
	if req.AgentID == "" {
		return nil, fmt.Errorf("agent_id is required")
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Get agent's channel cursors
	channelCursors, ok := s.agentCursors[req.AgentID]
	if !ok {
		// Agent not registered - no access to any channels
		return &GetNewResponse{
			Messages:   []*persistence.ChatMessage{},
			NewPointer: 0,
		}, nil
	}

	// 2. Filter in-memory messages by agent's channels and cursors
	var newMessages []*persistence.ChatMessage
	maxCursor := int64(0)

	// Format expected author string for this agent
	expectedAuthor := FormatAuthor(req.AgentID)

	for _, msg := range s.messages {
		// Check if agent has access to this channel
		cursor, hasAccess := channelCursors[msg.Channel]
		if !hasAccess {
			continue
		}

		// Skip messages from the agent itself (don't echo own messages)
		if msg.Author == expectedAuthor {
			// Still track cursor even for own messages
			if msg.ID > maxCursor {
				maxCursor = msg.ID
			}
			continue
		}

		// Check if message is newer than cursor
		if msg.ID > cursor {
			newMessages = append(newMessages, msg)
			if msg.ID > maxCursor {
				maxCursor = msg.ID
			}
		}
	}

	// 3. Calculate new pointer (highest message ID seen across all channels)
	newPointer := maxCursor
	if newPointer == 0 {
		// No new messages - use highest cursor value
		for _, cursor := range channelCursors {
			if cursor > newPointer {
				newPointer = cursor
			}
		}
	}

	s.logger.Debug("GetNew for %s: %d new messages (pointer: %d)", req.AgentID, len(newMessages), newPointer)

	return &GetNewResponse{
		Messages:   newMessages,
		NewPointer: newPointer,
	}, nil
}

// HaveNewMessages checks if an agent has new messages without retrieving them or updating cursors.
// This is useful for polling to decide whether to make an LLM call.
func (s *Service) HaveNewMessages(agentID string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 1. Get agent's channel cursors
	channelCursors, ok := s.agentCursors[agentID]
	if !ok {
		// Agent not registered - no access to any channels
		return false
	}

	// 2. Check if any message exists that is newer than agent's cursor for accessible channels
	// Format expected author string for this agent to exclude own messages
	expectedAuthor := FormatAuthor(agentID)

	for _, msg := range s.messages {
		// Check if agent has access to this channel
		cursor, hasAccess := channelCursors[msg.Channel]
		if !hasAccess {
			continue
		}

		// Skip messages from the agent itself (don't count own messages)
		if msg.Author == expectedAuthor {
			continue
		}

		// Check if message is newer than cursor
		if msg.ID > cursor {
			return true
		}
	}

	return false
}

// UpdateCursor updates an agent's cursor to a new position across all channels.
// This updates the cursor for ALL channels the agent has access to.
func (s *Service) UpdateCursor(_ context.Context, agentID string, newPointer int64) error {
	if agentID == "" {
		return fmt.Errorf("agent_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Update in-memory cursors for all channels
	channelCursors, ok := s.agentCursors[agentID]
	if !ok {
		return fmt.Errorf("agent %s not registered", agentID)
	}

	for channel := range channelCursors {
		channelCursors[channel] = newPointer
	}

	s.logger.Debug("Updated cursor for %s to %d (all channels)", agentID, newPointer)

	// Async persist to database (fire-and-forget)
	go func() {
		for channel := range channelCursors {
			err := s.dbOps.UpdateChatCursor(agentID, channel, newPointer)
			if err != nil {
				s.logger.Warn("Failed to persist cursor for %s channel %s: %v", agentID, channel, err)
			}
		}
	}()

	return nil
}

// GetAllMessages returns all in-memory messages (for WebUI display).
// This provides real-time access to the canonical message state.
func (s *Service) GetAllMessages() []*persistence.ChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return a copy to prevent external modification
	messagesCopy := make([]*persistence.ChatMessage, len(s.messages))
	copy(messagesCopy, s.messages)
	return messagesCopy
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

// WaitForReply waits for a reply to the specified message ID.
// Returns the first message where reply_to matches messageID.
// Uses channel-based signaling for immediate response when reply arrives.
// The pollInterval parameter is kept for API compatibility but is no longer used for polling.
func (s *Service) WaitForReply(ctx context.Context, messageID int64, _ time.Duration) (*persistence.ChatMessage, error) {
	s.logger.Info("Waiting for reply to message %d", messageID)

	// Check if reply already exists
	if reply := s.findReplyInMemory(messageID); reply != nil {
		s.logger.Info("Found existing reply (id=%d, post_type=%s) to message %d", reply.ID, reply.PostType, messageID)
		return reply, nil
	}

	// Register waiter channel
	waitCh := s.registerWaiter(messageID)
	defer s.unregisterWaiter(messageID)

	// Wait for reply or context cancellation
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("context canceled while waiting for reply: %w", ctx.Err())
	case reply := <-waitCh:
		s.logger.Info("Received reply (id=%d, post_type=%s) to message %d", reply.ID, reply.PostType, messageID)
		return reply, nil
	}
}

// findReplyInMemory searches in-memory messages for a reply to the given message ID.
func (s *Service) findReplyInMemory(messageID int64) *persistence.ChatMessage {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, msg := range s.messages {
		if msg.ReplyTo != nil && *msg.ReplyTo == messageID {
			return msg
		}
	}
	return nil
}

// registerWaiter creates a channel to receive notification when a reply arrives.
func (s *Service) registerWaiter(messageID int64) chan *persistence.ChatMessage {
	s.waitersMu.Lock()
	defer s.waitersMu.Unlock()

	ch := make(chan *persistence.ChatMessage, 1)
	s.confirmationWaiters[messageID] = ch
	return ch
}

// unregisterWaiter removes the waiter channel for a message ID.
func (s *Service) unregisterWaiter(messageID int64) {
	s.waitersMu.Lock()
	defer s.waitersMu.Unlock()

	delete(s.confirmationWaiters, messageID)
}

// signalWaiter sends the reply to any registered waiter for the original message.
func (s *Service) signalWaiter(originalMessageID int64, reply *persistence.ChatMessage) {
	s.waitersMu.Lock()
	defer s.waitersMu.Unlock()

	if ch, ok := s.confirmationWaiters[originalMessageID]; ok {
		select {
		case ch <- reply:
			s.logger.Debug("Signaled waiter for message %d with reply %d", originalMessageID, reply.ID)
		default:
			// Channel full or no receiver - that's ok
		}
	}
}

// ConfirmationAction represents the user's response to a confirmation request.
type ConfirmationAction string

const (
	// ConfirmationContinue indicates the user wants to continue.
	ConfirmationContinue ConfirmationAction = "continue"
	// ConfirmationCancel indicates the user wants to cancel and provide guidance.
	ConfirmationCancel ConfirmationAction = "cancel"
)

// AskUserConfirmation posts a confirmation request and waits for user response.
// Returns the action the user selected (continue or cancel).
// The channel parameter specifies which chat channel to post to (e.g., "product" for PM, "development" for coders).
// The pollInterval determines how often to check for a reply.
func (s *Service) AskUserConfirmation(ctx context.Context, author, message, channel string, pollInterval time.Duration) (ConfirmationAction, error) {
	s.logger.Info("Asking user for confirmation on channel %s: %s", channel, message)

	// Post the confirmation request
	resp, err := s.Post(ctx, &PostRequest{
		Author:   author,
		Text:     message,
		Channel:  channel,
		PostType: PostTypeConfirmationRequest,
	})
	if err != nil {
		return "", fmt.Errorf("failed to post confirmation request: %w", err)
	}

	s.logger.Info("Posted confirmation request (id=%d), waiting for user response...", resp.ID)

	// Wait for user reply
	reply, err := s.WaitForReply(ctx, resp.ID, pollInterval)
	if err != nil {
		// Context cancelled or other error
		return "", err
	}

	// Determine action from reply's post_type
	var action ConfirmationAction
	switch reply.PostType {
	case PostTypeConfirmationContinue:
		action = ConfirmationContinue
	case PostTypeConfirmationCancel:
		action = ConfirmationCancel
	default:
		// Legacy fallback: check text content for backwards compatibility
		if reply.Text == "Continue" {
			action = ConfirmationContinue
		} else {
			action = ConfirmationCancel
		}
	}

	s.logger.Info("User response to confirmation: post_type=%q action=%s", reply.PostType, action)

	return action, nil
}
