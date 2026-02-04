package architect

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
)

// mockChatService is a test-local mock for ChatServiceInterface.
// This avoids import cycles with the shared mocks package.
type mockChatService struct {
	postFunc         func(ctx context.Context, req *ChatPostRequest) (*ChatPostResponse, error)
	waitForReplyFunc func(ctx context.Context, messageID int64, pollInterval time.Duration) (*ChatMessage, error)
	postCalls        []*ChatPostRequest
	nextMessageID    int64
}

func newMockChatService() *mockChatService {
	m := &mockChatService{nextMessageID: 1}

	// Default: return success
	m.postFunc = func(_ context.Context, _ *ChatPostRequest) (*ChatPostResponse, error) {
		id := m.nextMessageID
		m.nextMessageID++
		return &ChatPostResponse{ID: id, Success: true}, nil
	}

	// Default: return a generic reply
	m.waitForReplyFunc = func(_ context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		return &ChatMessage{
			Timestamp: time.Now().Format(time.RFC3339),
			Author:    "@human",
			Text:      "Default mock reply",
		}, nil
	}

	return m
}

func (m *mockChatService) Post(ctx context.Context, req *ChatPostRequest) (*ChatPostResponse, error) {
	m.postCalls = append(m.postCalls, req)
	return m.postFunc(ctx, req)
}

func (m *mockChatService) WaitForReply(ctx context.Context, messageID int64, pollInterval time.Duration) (*ChatMessage, error) {
	return m.waitForReplyFunc(ctx, messageID, pollInterval)
}

func (m *mockChatService) failPostWith(err error) {
	m.postFunc = func(_ context.Context, _ *ChatPostRequest) (*ChatPostResponse, error) {
		return nil, err
	}
}

func (m *mockChatService) replyWith(text string) {
	m.waitForReplyFunc = func(_ context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		return &ChatMessage{
			Timestamp: time.Now().Format(time.RFC3339),
			Author:    "@human",
			Text:      text,
		}, nil
	}
}

func (m *mockChatService) neverReply() {
	m.waitForReplyFunc = func(ctx context.Context, _ int64, _ time.Duration) (*ChatMessage, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
}

func (m *mockChatService) getPostCallCount() int {
	return len(m.postCalls)
}

func (m *mockChatService) lastPostCall() *ChatPostRequest {
	if len(m.postCalls) == 0 {
		return nil
	}
	return m.postCalls[len(m.postCalls)-1]
}

// TestHandleEscalated_NoChatService verifies error when chat service is not available.
func TestHandleEscalated_NoChatService(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateEscalated, nil, nil)
	baseSM.SetStateData(StateKeyEscalationOriginState, "REQUEST")
	baseSM.SetStateData(StateKeyEscalationIterationCount, 10)

	driver := &Driver{
		BaseStateMachine: baseSM,
		chatService:      nil, // No chat service
		logger:           logx.NewLogger("test-escalated"),
	}

	state, err := driver.handleEscalated(context.Background())

	assert.Equal(t, StateError, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "chat service not available")
}

// TestHandleEscalated_MissingOriginState verifies error when origin state is missing.
func TestHandleEscalated_MissingOriginState(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateEscalated, nil, nil)
	// Don't set origin state
	baseSM.SetStateData(StateKeyEscalationIterationCount, 10)

	mockChat := newMockChatService()

	driver := &Driver{
		BaseStateMachine: baseSM,
		chatService:      mockChat,
		logger:           logx.NewLogger("test-escalated"),
	}

	state, err := driver.handleEscalated(context.Background())

	assert.Equal(t, StateError, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "escalation_origin_state not found")
}

// TestHandleEscalated_PostsEscalationMessage verifies escalation message is posted.
func TestHandleEscalated_PostsEscalationMessage(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateEscalated, nil, nil)
	baseSM.SetStateData(StateKeyEscalationOriginState, "REQUEST")
	baseSM.SetStateData(StateKeyEscalationIterationCount, 16)
	baseSM.SetStateData(StateKeyEscalationRequestID, "req-123")
	baseSM.SetStateData(StateKeyEscalationStoryID, "story-456")
	baseSM.SetStateData(StateKeyEscalationAgentID, "coder-001")

	mockChat := newMockChatService()
	// Configure mock to timeout waiting for reply (simulates no reply yet)
	mockChat.neverReply()

	driver := &Driver{
		BaseStateMachine: baseSM,
		chatService:      mockChat,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-escalated"),
	}

	// Use context with short timeout to avoid hanging
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	state, err := driver.handleEscalated(ctx)

	// Should stay in ESCALATED state (waiting for reply)
	assert.Equal(t, StateEscalated, state)
	assert.NoError(t, err)

	// Verify escalation message was posted
	require.Equal(t, 1, mockChat.getPostCallCount())

	lastPost := mockChat.lastPostCall()
	assert.Equal(t, "test-architect", lastPost.Author)
	assert.Equal(t, "product", lastPost.Channel) // Escalations go to product channel for human attention
	assert.Equal(t, "escalate", lastPost.PostType)
	assert.Contains(t, lastPost.Text, "ESCALATION")
	assert.Contains(t, lastPost.Text, "16 iterations")
	assert.Contains(t, lastPost.Text, "req-123")
	assert.Contains(t, lastPost.Text, "story-456")
}

// TestHandleEscalated_ReceivesReply verifies transition back to REQUEST after receiving reply.
func TestHandleEscalated_ReceivesReply(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateEscalated, nil, nil)
	baseSM.SetStateData(StateKeyEscalationOriginState, "REQUEST")
	baseSM.SetStateData(StateKeyEscalationIterationCount, 16)
	baseSM.SetStateData(StateKeyEscalationRequestID, "req-123")
	baseSM.SetStateData(StateKeyEscalationStoryID, "story-456")
	baseSM.SetStateData(StateKeyEscalationAgentID, "coder-001")
	// Simulate already having posted the escalation message
	baseSM.SetStateData(StateKeyEscalationMessageID, int64(42))
	baseSM.SetStateData(StateKeyEscalatedAt, time.Now())

	mockChat := newMockChatService()
	// Configure mock to return a human reply
	mockChat.replyWith("Please try a different approach: focus on the config file first.")

	driver := &Driver{
		BaseStateMachine: baseSM,
		chatService:      mockChat,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-escalated"),
	}

	state, err := driver.handleEscalated(context.Background())

	// Should transition back to REQUEST state
	assert.Equal(t, StateRequest, state)
	assert.NoError(t, err)

	// Should not have posted a new message (already had one)
	assert.Equal(t, 0, mockChat.getPostCallCount())

	// Verify escalation state was cleared (set to nil)
	originVal, _ := baseSM.GetStateValue(StateKeyEscalationOriginState)
	assert.Nil(t, originVal, "escalation origin state should be cleared (nil)")

	msgIDVal, _ := baseSM.GetStateValue(StateKeyEscalationMessageID)
	assert.Nil(t, msgIDVal, "escalation message ID should be cleared (nil)")

	// Verify iteration counter was reset
	originIterKey := "REQUEST_iterations"
	val, _ := baseSM.GetStateValue(originIterKey)
	assert.Equal(t, 0, val, "iteration counter should be reset")

	// Note: Full context manager integration (guidance injection) is tested via integration tests.
	// Here we just verify the state transition and state data clearing works correctly.
}

// TestHandleEscalated_PostError verifies error handling when post fails.
func TestHandleEscalated_PostError(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateEscalated, nil, nil)
	baseSM.SetStateData(StateKeyEscalationOriginState, "REQUEST")
	baseSM.SetStateData(StateKeyEscalationIterationCount, 16)

	mockChat := newMockChatService()
	mockChat.failPostWith(assert.AnError)

	driver := &Driver{
		BaseStateMachine: baseSM,
		chatService:      mockChat,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-escalated"),
	}

	state, err := driver.handleEscalated(context.Background())

	assert.Equal(t, StateError, state)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to post escalation")
}

// TestHandleEscalated_NoReplyYet verifies staying in ESCALATED when no reply received.
func TestHandleEscalated_NoReplyYet(t *testing.T) {
	baseSM := agent.NewBaseStateMachine("test-architect", StateEscalated, nil, nil)
	baseSM.SetStateData(StateKeyEscalationOriginState, "REQUEST")
	baseSM.SetStateData(StateKeyEscalationIterationCount, 16)
	// Simulate already having posted the escalation message
	baseSM.SetStateData(StateKeyEscalationMessageID, int64(42))
	baseSM.SetStateData(StateKeyEscalatedAt, time.Now())

	mockChat := newMockChatService()
	// Configure mock to never reply (simulates waiting)
	mockChat.neverReply()

	driver := &Driver{
		BaseStateMachine: baseSM,
		chatService:      mockChat,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-escalated"),
	}

	// Use context with timeout that will expire
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	state, err := driver.handleEscalated(ctx)

	// Should stay in ESCALATED state (still waiting)
	assert.Equal(t, StateEscalated, state)
	assert.NoError(t, err)

	// Should not have posted another message
	assert.Equal(t, 0, mockChat.getPostCallCount())
}

// TestBuildEscalationMessage verifies escalation message content.
func TestBuildEscalationMessage(t *testing.T) {
	driver := &Driver{
		logger: logx.NewLogger("test-escalated"),
	}

	msg := driver.buildEscalationMessage("REQUEST", 16, "req-123", "story-456")

	assert.Contains(t, msg, "ESCALATION")
	assert.Contains(t, msg, "16 iterations")
	assert.Contains(t, msg, "REQUEST")
	assert.Contains(t, msg, "req-123")
	assert.Contains(t, msg, "story-456")
	assert.Contains(t, msg, "MCP read tools")
	assert.Contains(t, msg, "guidance")
}

// TestBuildEscalationMessage_NoStoryID verifies message without story ID.
func TestBuildEscalationMessage_NoStoryID(t *testing.T) {
	driver := &Driver{
		logger: logx.NewLogger("test-escalated"),
	}

	msg := driver.buildEscalationMessage("REQUEST", 10, "req-456", "")

	assert.Contains(t, msg, "ESCALATION")
	assert.Contains(t, msg, "10 iterations")
	assert.Contains(t, msg, "req-456")
	assert.NotContains(t, msg, "Story ID") // Should not mention story when empty
}
