package architect

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// MockLLMClient is a minimal mock for testing.
type MockLLMClient struct{}

func (m *MockLLMClient) Complete(_ context.Context, _ agent.CompletionRequest) (agent.CompletionResponse, error) {
	return agent.CompletionResponse{}, nil
}

func (m *MockLLMClient) Stream(_ context.Context, _ agent.CompletionRequest) (<-chan agent.StreamChunk, error) {
	ch := make(chan agent.StreamChunk)
	close(ch)
	return ch, nil
}

func (m *MockLLMClient) GetModelName() string {
	return "mock-model"
}

// Helper to create a minimal test driver without a full LLM client or dispatcher.
func newTestDriver() *Driver {
	// Create a minimal persistence channel (won't be used)
	persistCh := make(chan<- *persistence.Request, 10)

	baseSM := agent.NewBaseStateMachine("test-arch", StateWaiting, nil, architectTransitions)

	return &Driver{
		BaseStateMachine:   baseSM,
		agentContexts:      make(map[string]*contextmgr.ContextManager),
		contextMutex:       sync.RWMutex{},
		logger:             logx.NewLogger("test-arch"),
		queue:              NewQueue(nil),
		escalationHandler:  NewEscalationHandler(NewQueue(nil)),
		persistenceChannel: persistCh,
		workDir:            "/tmp/test-workspace",
	}
}

// TestGetID verifies GetID returns the agent ID.
func TestGetID(t *testing.T) {
	driver := newTestDriver()
	assert.Equal(t, "test-arch", driver.GetID())
}

// TestGetStoryID verifies architects return empty story ID (they coordinate, not implement).
func TestGetStoryID(t *testing.T) {
	driver := newTestDriver()
	assert.Equal(t, "", driver.GetStoryID())
}

// TestGetAgentType verifies GetAgentType returns TypeArchitect.
func TestGetAgentType(t *testing.T) {
	driver := newTestDriver()
	assert.Equal(t, agent.TypeArchitect, driver.GetAgentType())
}

// TestGetQueue verifies GetQueue returns the queue manager.
func TestGetQueue(t *testing.T) {
	driver := newTestDriver()
	assert.NotNil(t, driver.GetQueue())
	assert.Same(t, driver.queue, driver.GetQueue())
}

// TestGetStoryList verifies GetStoryList returns all stories (empty for new driver).
func TestGetStoryList(t *testing.T) {
	driver := newTestDriver()

	// Empty queue
	stories := driver.GetStoryList()
	assert.Empty(t, stories)

	// Add a story and verify it appears
	driver.queue.AddStory("story-1", "spec-1", "Test Story", "Content", "app", nil, 2)
	stories = driver.GetStoryList()
	assert.Len(t, stories, 1)
	assert.Equal(t, "story-1", stories[0].ID)
}

// TestGetStoryList_NilQueue verifies GetStoryList handles nil queue gracefully.
func TestGetStoryList_NilQueue(t *testing.T) {
	driver := newTestDriver()
	driver.queue = nil

	stories := driver.GetStoryList()
	assert.Empty(t, stories)
	assert.NotNil(t, stories) // Should return empty slice, not nil
}

// TestGetEscalationHandler verifies GetEscalationHandler returns the handler.
func TestGetEscalationHandler(t *testing.T) {
	driver := newTestDriver()
	assert.NotNil(t, driver.GetEscalationHandler())
	assert.Same(t, driver.escalationHandler, driver.GetEscalationHandler())
}

// TestSetChannels verifies SetChannels sets the communication channels.
func TestSetChannels(t *testing.T) {
	driver := newTestDriver()

	questionsCh := make(chan *proto.AgentMsg, 1)
	replyCh := make(chan *proto.AgentMsg, 1)

	driver.SetChannels(questionsCh, nil, replyCh)

	assert.NotNil(t, driver.questionsCh, "questionsCh should be set")
	assert.NotNil(t, driver.replyCh, "replyCh should be set")

	// Test that channels are functional
	testMsg := &proto.AgentMsg{ID: "test-id"}
	go func() { questionsCh <- testMsg }()
	received := <-driver.questionsCh
	assert.Equal(t, "test-id", received.ID, "should receive message through questionsCh")
}

// TestSetDispatcher verifies SetDispatcher updates the dispatcher.
func TestSetDispatcher(t *testing.T) {
	driver := newTestDriver()

	mockDispatcher := &dispatch.Dispatcher{}
	driver.SetDispatcher(mockDispatcher)

	assert.Same(t, mockDispatcher, driver.dispatcher)
}

// TestSetLLMClient verifies SetLLMClient sets both the client and initializes toolLoop.
func TestSetLLMClient(t *testing.T) {
	driver := newTestDriver()

	mockClient := &MockLLMClient{}
	driver.SetLLMClient(mockClient)

	// Should set client on BaseStateMachine
	assert.Same(t, mockClient, driver.BaseStateMachine.LLMClient)
	// Should initialize toolLoop
	assert.NotNil(t, driver.toolLoop)
}

// TestValidateState verifies ValidateState accepts valid states and rejects invalid ones.
func TestValidateState(t *testing.T) {
	driver := newTestDriver()

	// Valid architect states
	validStates := []proto.State{
		StateWaiting,
		StateDispatching,
		StateMonitoring,
		StateRequest,
		StateEscalated,
		StateDone,
		StateError,
	}

	for _, state := range validStates {
		err := driver.ValidateState(state)
		assert.NoError(t, err, "state %s should be valid", state)
	}

	// Invalid states (coder-only)
	invalidStates := []proto.State{
		proto.State("PLANNING"),
		proto.State("CODING"),
		proto.State("TESTING"),
		proto.State("INVALID"),
	}

	for _, state := range invalidStates {
		err := driver.ValidateState(state)
		assert.Error(t, err, "state %s should be invalid for architect", state)
	}
}

// TestGetValidStates verifies GetValidStates returns all architect states.
func TestGetValidStates(t *testing.T) {
	driver := newTestDriver()

	states := driver.GetValidStates()

	// Should contain all architect states
	assert.Contains(t, states, StateWaiting)
	assert.Contains(t, states, StateDispatching)
	assert.Contains(t, states, StateMonitoring)
	assert.Contains(t, states, StateRequest)
	assert.Contains(t, states, StateEscalated)
	assert.Contains(t, states, StateDone)
	assert.Contains(t, states, StateError)
}

// TestShutdown verifies Shutdown cancels background context and stops executor.
func TestShutdown(t *testing.T) {
	driver := newTestDriver()

	// Create shutdown context
	ctx, cancel := context.WithCancel(context.Background())
	driver.shutdownCtx = ctx
	driver.shutdownCancel = cancel

	// Verify context is not cancelled before shutdown
	select {
	case <-driver.shutdownCtx.Done():
		t.Fatal("shutdown context should not be cancelled yet")
	default:
		// Expected
	}

	// Shutdown should cancel the context
	err := driver.Shutdown(context.Background())
	assert.NoError(t, err)

	// Verify context is now cancelled
	select {
	case <-driver.shutdownCtx.Done():
		// Expected
	default:
		t.Fatal("shutdown context should be cancelled after Shutdown")
	}
}

// TestInitialize_MissingChannels verifies Initialize fails without channels.
func TestInitialize_MissingChannels(t *testing.T) {
	driver := newTestDriver()

	err := driver.Initialize(context.Background())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "channel not set")
}

// TestInitialize_Success verifies Initialize succeeds with channels set.
func TestInitialize_Success(t *testing.T) {
	driver := newTestDriver()

	// Set required channels
	questionsCh := make(chan *proto.AgentMsg, 1)
	replyCh := make(chan *proto.AgentMsg, 1)
	driver.SetChannels(questionsCh, nil, replyCh)

	err := driver.Initialize(context.Background())
	assert.NoError(t, err)
}

// TestPersistQueueState verifies queue state serialization.
func TestPersistQueueState(t *testing.T) {
	driver := newTestDriver()

	// Add some stories to queue
	driver.queue.AddStory("story-1", "spec-1", "Story One", "Content 1", "app", nil, 2)
	driver.queue.AddStory("story-2", "spec-1", "Story Two", "Content 2", "app", []string{"story-1"}, 3)

	err := driver.persistQueueState()
	require.NoError(t, err)

	// Verify queue_json was set in state data
	stateData := driver.GetStateData()
	queueJSON, exists := stateData[StateKeyQueueJSON]
	assert.True(t, exists, "queue_json should be set in state data")
	assert.NotEmpty(t, queueJSON, "queue_json should not be empty")

	// Verify it's valid JSON containing our stories
	jsonStr, ok := queueJSON.(string)
	require.True(t, ok, "queue_json should be a string")
	assert.Contains(t, jsonStr, "story-1")
	assert.Contains(t, jsonStr, "story-2")
	assert.Contains(t, jsonStr, "Story One")
}

// TestDetectDeadlock_NoDeadlock verifies normal cases don't trigger deadlock detection.
func TestDetectDeadlock_NoDeadlock(t *testing.T) {
	driver := newTestDriver()

	// Case 1: Empty queue - no deadlock
	assert.False(t, driver.detectDeadlock(), "empty queue should not be deadlock")

	// Case 2: All stories completed - no deadlock
	driver.queue.AddStory("story-1", "spec-1", "Story", "Content", "app", nil, 2)
	_ = driver.queue.UpdateStoryStatus("story-1", StatusDone)
	assert.False(t, driver.detectDeadlock(), "all completed should not be deadlock")

	// Case 3: Story ready to dispatch - no deadlock
	driver.queue.AddStory("story-2", "spec-1", "Ready Story", "Content", "app", nil, 2)
	assert.False(t, driver.detectDeadlock(), "stories ready should not be deadlock")
}

// TestDetectDeadlock_CircularDependency verifies circular dependencies trigger deadlock.
func TestDetectDeadlock_CircularDependency(t *testing.T) {
	driver := newTestDriver()

	// Create circular dependency: A depends on B, B depends on A
	driver.queue.AddStory("story-a", "spec-1", "Story A", "Content A", "app", []string{"story-b"}, 2)
	driver.queue.AddStory("story-b", "spec-1", "Story B", "Content B", "app", []string{"story-a"}, 2)

	assert.True(t, driver.detectDeadlock(), "circular dependency should be deadlock")
}

// TestDetectDeadlock_MissingDependency verifies missing dependencies trigger deadlock.
func TestDetectDeadlock_MissingDependency(t *testing.T) {
	driver := newTestDriver()

	// Story depends on non-existent story
	driver.queue.AddStory("story-1", "spec-1", "Story 1", "Content", "app", []string{"nonexistent"}, 2)

	assert.True(t, driver.detectDeadlock(), "missing dependency should be deadlock")
}

// TestDetectDeadlock_NilQueue verifies nil queue returns false (not a crash).
func TestDetectDeadlock_NilQueue(t *testing.T) {
	driver := newTestDriver()
	driver.queue = nil

	assert.False(t, driver.detectDeadlock(), "nil queue should return false")
}
