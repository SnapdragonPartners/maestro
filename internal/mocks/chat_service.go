package mocks

import (
	"context"
	"time"
)

// ChatPostRequest mirrors architect.ChatPostRequest for use in mocks.
// This avoids import cycles between mocks and architect packages.
type ChatPostRequest struct {
	Author   string
	Text     string
	Channel  string
	ReplyTo  *int64
	PostType string
}

// ChatPostResponse mirrors architect.ChatPostResponse for use in mocks.
type ChatPostResponse struct {
	ID      int64
	Success bool
}

// ChatMessage mirrors architect.ChatMessage for use in mocks.
type ChatMessage struct {
	Timestamp string
	Author    string
	Text      string
}

// MockChatService provides a mock implementation for chat service testing.
// It provides configurable behavior for Post and WaitForReply operations.
//
// Note: This mock uses local type definitions that mirror architect.ChatServiceInterface types
// to avoid import cycles. Tests in the architect package should use the test-local mock
// in escalated_test.go which uses the real types.
type MockChatService struct {
	// PostFunc is called when Post is invoked. Override to customize behavior.
	PostFunc func(ctx context.Context, req *ChatPostRequest) (*ChatPostResponse, error)

	// WaitForReplyFunc is called when WaitForReply is invoked. Override to customize behavior.
	WaitForReplyFunc func(ctx context.Context, messageID int64, pollInterval time.Duration) (*ChatMessage, error)

	// PostCalls tracks all calls to Post for verification.
	PostCalls []*ChatPostRequest

	// WaitForReplyCalls tracks all calls to WaitForReply for verification.
	WaitForReplyCalls []WaitForReplyCall

	// nextMessageID is used to generate incrementing message IDs.
	nextMessageID int64
}

// WaitForReplyCall records the parameters of a WaitForReply call.
type WaitForReplyCall struct {
	MessageID    int64
	PollInterval time.Duration
}

// NewMockChatService creates a new mock chat service with default behavior.
// Default behavior: Post returns success, WaitForReply returns a generic reply.
func NewMockChatService() *MockChatService {
	m := &MockChatService{
		nextMessageID: 1,
	}

	// Default Post behavior: return success with incrementing ID
	m.PostFunc = func(_ context.Context, _ *ChatPostRequest) (*ChatPostResponse, error) {
		id := m.nextMessageID
		m.nextMessageID++
		return &ChatPostResponse{
			ID:      id,
			Success: true,
		}, nil
	}

	// Default WaitForReply behavior: return a generic reply immediately
	m.WaitForReplyFunc = func(_ context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		return &ChatMessage{
			Timestamp: time.Now().Format(time.RFC3339),
			Author:    "@human",
			Text:      "Default mock reply",
		}, nil
	}

	return m
}

// Post records the call and invokes the configured PostFunc.
func (m *MockChatService) Post(ctx context.Context, req *ChatPostRequest) (*ChatPostResponse, error) {
	m.PostCalls = append(m.PostCalls, req)
	return m.PostFunc(ctx, req)
}

// WaitForReply records the call and invokes the configured WaitForReplyFunc.
func (m *MockChatService) WaitForReply(ctx context.Context, messageID int64, pollInterval time.Duration) (*ChatMessage, error) {
	m.WaitForReplyCalls = append(m.WaitForReplyCalls, WaitForReplyCall{
		MessageID:    messageID,
		PollInterval: pollInterval,
	})
	return m.WaitForReplyFunc(ctx, messageID, pollInterval)
}

// OnPost sets a custom handler for Post calls.
func (m *MockChatService) OnPost(fn func(ctx context.Context, req *ChatPostRequest) (*ChatPostResponse, error)) {
	m.PostFunc = fn
}

// OnWaitForReply sets a custom handler for WaitForReply calls.
func (m *MockChatService) OnWaitForReply(fn func(ctx context.Context, messageID int64, pollInterval time.Duration) (*ChatMessage, error)) {
	m.WaitForReplyFunc = fn
}

// FailPostWith configures Post to return the specified error.
func (m *MockChatService) FailPostWith(err error) {
	m.PostFunc = func(_ context.Context, _ *ChatPostRequest) (*ChatPostResponse, error) {
		return nil, err
	}
}

// FailWaitForReplyWith configures WaitForReply to return the specified error.
func (m *MockChatService) FailWaitForReplyWith(err error) {
	m.WaitForReplyFunc = func(_ context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		return nil, err
	}
}

// ReplyWith configures WaitForReply to return a message with the specified text.
func (m *MockChatService) ReplyWith(text string) {
	m.WaitForReplyFunc = func(_ context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		return &ChatMessage{
			Timestamp: time.Now().Format(time.RFC3339),
			Author:    "@human",
			Text:      text,
		}, nil
	}
}

// ReplyWithDelay configures WaitForReply to wait for the specified duration before returning.
func (m *MockChatService) ReplyWithDelay(text string, delay time.Duration) {
	m.WaitForReplyFunc = func(ctx context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
			return &ChatMessage{
				Timestamp: time.Now().Format(time.RFC3339),
				Author:    "@human",
				Text:      text,
			}, nil
		}
	}
}

// NeverReply configures WaitForReply to block until the context is cancelled.
// Useful for testing timeout behavior.
func (m *MockChatService) NeverReply() {
	m.WaitForReplyFunc = func(ctx context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

// Reset clears all recorded calls and resets to default behavior.
func (m *MockChatService) Reset() {
	m.PostCalls = nil
	m.WaitForReplyCalls = nil
	m.nextMessageID = 1
}

// GetPostCallCount returns the number of times Post was called.
func (m *MockChatService) GetPostCallCount() int {
	return len(m.PostCalls)
}

// GetWaitForReplyCallCount returns the number of times WaitForReply was called.
func (m *MockChatService) GetWaitForReplyCallCount() int {
	return len(m.WaitForReplyCalls)
}

// LastPostCall returns the most recent Post call, or nil if none.
func (m *MockChatService) LastPostCall() *ChatPostRequest {
	if len(m.PostCalls) == 0 {
		return nil
	}
	return m.PostCalls[len(m.PostCalls)-1]
}

// AssertPostCalledWith verifies that Post was called with the expected author and contains the expected text.
func (m *MockChatService) AssertPostCalledWith(author, textSubstring string) bool {
	for _, call := range m.PostCalls {
		if call.Author == author && containsSubstring(call.Text, textSubstring) {
			return true
		}
	}
	return false
}

// containsSubstring is a helper for case-sensitive substring matching.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (substr == "" || findSubstring(s, substr) >= 0)
}

// findSubstring returns the index of substr in s, or -1 if not found.
func findSubstring(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}
