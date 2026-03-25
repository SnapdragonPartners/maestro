package architect

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/templates"
)

// TestGetContextForAgent verifies that getContextForAgent creates and retrieves agent-specific contexts correctly.
func TestGetContextForAgent(t *testing.T) {
	// Create minimal BaseStateMachine to satisfy embedded field
	baseSM := &agent.BaseStateMachine{}

	driver := &Driver{
		BaseStateMachine: baseSM,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-arch-1"),
	}

	// First call should create a new context
	cm1 := driver.getContextForAgent("coder-001")
	require.NotNil(t, cm1, "should create context for new agent")

	// Second call should return the same context
	cm2 := driver.getContextForAgent("coder-001")
	assert.Same(t, cm1, cm2, "should return same context for same agent")

	// Different agent should get different context
	cm3 := driver.getContextForAgent("coder-002")
	require.NotNil(t, cm3, "should create context for different agent")
	assert.NotSame(t, cm1, cm3, "different agents should have different contexts")
}

// TestGetContextForAgent_Concurrent verifies thread safety of context creation.
func TestGetContextForAgent_Concurrent(t *testing.T) {
	baseSM := &agent.BaseStateMachine{}

	driver := &Driver{
		BaseStateMachine: baseSM,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-arch-1"),
	}

	const numGoroutines = 10
	const agentID = "coder-001"

	var wg sync.WaitGroup
	contexts := make([]*contextmgr.ContextManager, numGoroutines)

	// Multiple goroutines try to get context for same agent simultaneously
	wg.Add(numGoroutines)
	for i := 0; i < numGoroutines; i++ {
		go func(idx int) {
			defer wg.Done()
			contexts[idx] = driver.getContextForAgent(agentID)
		}(i)
	}
	wg.Wait()

	// All goroutines should get the same context instance (no duplicate creation)
	firstContext := contexts[0]
	require.NotNil(t, firstContext, "first context should not be nil")

	for i := 1; i < numGoroutines; i++ {
		assert.Same(t, firstContext, contexts[i],
			"all concurrent calls should return same context instance (got different at index %d)", i)
	}

	// Verify only one entry in map
	assert.Equal(t, 1, len(driver.agentContexts), "should only have one context entry despite concurrent access")
}

// newTestDriverWithDispatcher creates a Driver with a real dispatcher and queue for testing
// ensureContextForStory and ResetAgentContext.
func newTestDriverWithDispatcher(t *testing.T) (*Driver, *dispatch.Dispatcher) {
	t.Helper()

	cfg := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders: 2,
		},
	}
	dispatcher, err := dispatch.NewDispatcher(cfg)
	require.NoError(t, err)

	queue := NewQueue(nil)
	renderer, err := templates.NewRenderer()
	require.NoError(t, err)

	driver := &Driver{
		BaseStateMachine: &agent.BaseStateMachine{},
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		reviewStreaks:    make(map[string]map[string]int),
		queue:            queue,
		renderer:         renderer,
		dispatcher:       dispatcher,
		logger:           logx.NewLogger("test-arch"),
	}

	return driver, dispatcher
}

// addTestStory adds a story to the driver's queue for testing.
func addTestStory(driver *Driver, id, title, content string) {
	story := &persistence.Story{
		ID:      id,
		SpecID:  "spec-test",
		Title:   title,
		Content: content,
	}
	driver.queue.stories[id] = NewQueuedStory(story)
}

// TestEnsureContextForStory_FirstCall verifies that the first call creates context with system prompt.
func TestEnsureContextForStory_FirstCall(t *testing.T) {
	driver, dispatcher := newTestDriverWithDispatcher(t)
	addTestStory(driver, "story-001", "Build Login Page", "Implement a login page with email/password")

	dispatcher.SetLease("coder-001", "story-001")

	cm, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-001")
	require.NoError(t, err)
	require.NotNil(t, cm)

	// Verify template name was set
	assert.Equal(t, "agent-coder-001-story-story-001", cm.GetCurrentTemplate())

	// Verify system prompt contains story content
	msgs := cm.GetMessages()
	require.GreaterOrEqual(t, len(msgs), 1)
	assert.Contains(t, msgs[0].Content, "Build Login Page")
	assert.Contains(t, msgs[0].Content, "login page with email/password")
}

// TestEnsureContextForStory_Idempotent verifies same-story calls are no-ops.
func TestEnsureContextForStory_Idempotent(t *testing.T) {
	driver, dispatcher := newTestDriverWithDispatcher(t)
	addTestStory(driver, "story-001", "Build Login Page", "Implement a login page")

	dispatcher.SetLease("coder-001", "story-001")

	// First call creates context
	cm1, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-001")
	require.NoError(t, err)

	// Add a conversation message to the context
	cm1.AddMessage("test", "some conversation content")

	// Second call should be a no-op — same context, conversation preserved
	cm2, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-001")
	require.NoError(t, err)
	assert.Same(t, cm1, cm2, "should return same context manager instance")

	// Verify conversation message was preserved (not reset)
	assert.Greater(t, cm2.CountTokens(), 0, "context should still have content")
}

// TestEnsureContextForStory_StoryTransition verifies that a story change resets context.
func TestEnsureContextForStory_StoryTransition(t *testing.T) {
	driver, dispatcher := newTestDriverWithDispatcher(t)
	addTestStory(driver, "story-001", "Build Login Page", "Implement a login page")
	addTestStory(driver, "story-002", "Build Dashboard", "Implement a dashboard with charts")

	dispatcher.SetLease("coder-001", "story-001")

	// First story
	cm, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-001")
	require.NoError(t, err)
	assert.Equal(t, "agent-coder-001-story-story-001", cm.GetCurrentTemplate())

	// Add conversation history
	cm.AddMessage("test", "old story conversation")

	// Transition to new story
	dispatcher.SetLease("coder-001", "story-002")
	cm2, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-002")
	require.NoError(t, err)
	assert.Same(t, cm, cm2, "should return same context manager instance (reused, not recreated)")

	// Verify template changed
	assert.Equal(t, "agent-coder-001-story-story-002", cm2.GetCurrentTemplate())

	// Verify system prompt now has new story content
	msgs := cm2.GetMessages()
	require.GreaterOrEqual(t, len(msgs), 1)
	assert.Contains(t, msgs[0].Content, "Build Dashboard")
	assert.Contains(t, msgs[0].Content, "dashboard with charts")
	assert.NotContains(t, msgs[0].Content, "Build Login Page")
}

// TestEnsureContextForStory_ClearsReviewStreaks verifies that story transition clears review streaks.
func TestEnsureContextForStory_ClearsReviewStreaks(t *testing.T) {
	driver, dispatcher := newTestDriverWithDispatcher(t)
	addTestStory(driver, "story-001", "Story One", "Content one")
	addTestStory(driver, "story-002", "Story Two", "Content two")

	dispatcher.SetLease("coder-001", "story-001")

	// Set up context for first story
	_, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-001")
	require.NoError(t, err)

	// Simulate review streaks
	driver.incrementReviewStreak("coder-001", ReviewTypeCode)
	driver.incrementReviewStreak("coder-001", ReviewTypeCode)
	driver.incrementReviewStreak("coder-001", ReviewTypePlan)
	assert.Equal(t, 2, driver.getReviewStreak("coder-001", ReviewTypeCode))
	assert.Equal(t, 1, driver.getReviewStreak("coder-001", ReviewTypePlan))

	// Transition to new story should clear streaks
	dispatcher.SetLease("coder-001", "story-002")
	_, err = driver.ensureContextForStory(context.Background(), "coder-001", "story-002")
	require.NoError(t, err)

	assert.Equal(t, 0, driver.getReviewStreak("coder-001", ReviewTypeCode), "code review streak should be cleared")
	assert.Equal(t, 0, driver.getReviewStreak("coder-001", ReviewTypePlan), "plan review streak should be cleared")
}

// TestEnsureContextForStory_MultiAgentIsolation verifies two agents' contexts never interfere.
func TestEnsureContextForStory_MultiAgentIsolation(t *testing.T) {
	driver, dispatcher := newTestDriverWithDispatcher(t)
	addTestStory(driver, "story-001", "Build Login Page", "Implement login")
	addTestStory(driver, "story-002", "Build Dashboard", "Implement dashboard")

	dispatcher.SetLease("coder-001", "story-001")
	dispatcher.SetLease("coder-002", "story-002")

	// Set up contexts for both agents on different stories
	cm1, err := driver.ensureContextForStory(context.Background(), "coder-001", "story-001")
	require.NoError(t, err)

	cm2, err := driver.ensureContextForStory(context.Background(), "coder-002", "story-002")
	require.NoError(t, err)

	assert.NotSame(t, cm1, cm2, "different agents should have different context managers")

	// Verify each has correct story
	assert.Equal(t, "agent-coder-001-story-story-001", cm1.GetCurrentTemplate())
	assert.Equal(t, "agent-coder-002-story-story-002", cm2.GetCurrentTemplate())

	msgs1 := cm1.GetMessages()
	msgs2 := cm2.GetMessages()
	require.GreaterOrEqual(t, len(msgs1), 1)
	require.GreaterOrEqual(t, len(msgs2), 1)
	assert.Contains(t, msgs1[0].Content, "Build Login Page")
	assert.Contains(t, msgs2[0].Content, "Build Dashboard")
	assert.NotContains(t, msgs1[0].Content, "Build Dashboard")
	assert.NotContains(t, msgs2[0].Content, "Build Login Page")

	// Now transition coder-001 to story-002 — should not affect coder-002
	dispatcher.SetLease("coder-001", "story-002")
	_, err = driver.ensureContextForStory(context.Background(), "coder-001", "story-002")
	require.NoError(t, err)

	// coder-002's context should be untouched
	assert.Equal(t, "agent-coder-002-story-story-002", cm2.GetCurrentTemplate())
	msgs2After := cm2.GetMessages()
	assert.Equal(t, len(msgs2), len(msgs2After), "coder-002 message count should be unchanged")
}

// TestResetAgentContext verifies the public ResetAgentContext delegates correctly.
func TestResetAgentContext(t *testing.T) {
	driver, dispatcher := newTestDriverWithDispatcher(t)
	addTestStory(driver, "story-001", "Build Login Page", "Implement login")

	dispatcher.SetLease("coder-001", "story-001")

	err := driver.ResetAgentContext("coder-001")
	require.NoError(t, err)

	cm := driver.getContextForAgent("coder-001")
	assert.Equal(t, "agent-coder-001-story-story-001", cm.GetCurrentTemplate())
}

// TestResetAgentContext_NoStory verifies error when no story is leased.
func TestResetAgentContext_NoStory(t *testing.T) {
	driver, _ := newTestDriverWithDispatcher(t)

	err := driver.ResetAgentContext("coder-001")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no story found")
}

// TestBuildSystemPrompt verifies system prompt generation with story context.
// Note: Knowledge packs are delivered via request content (templates), not via story records.
// See docs/TESTING_STRATEGY.md and docs/DOC_GRAPH.md for knowledge pack flow details.
func TestBuildSystemPrompt(t *testing.T) {
	queue := NewQueue(nil)
	testStory := &persistence.Story{
		ID:      "story-xyz",
		SpecID:  "spec-abc",
		Title:   "Implement Authentication",
		Content: "Add JWT-based authentication to the API",
		// Note: KnowledgePack field on story is not used - packs come via request content
	}
	queuedStory := NewQueuedStory(testStory)
	queue.stories["story-xyz"] = queuedStory

	renderer, err := templates.NewRenderer()
	require.NoError(t, err, "should create renderer")

	driver := &Driver{
		queue:    queue,
		renderer: renderer,
		logger:   logx.NewLogger("test-arch-1"),
	}

	prompt, err := driver.buildSystemPrompt(context.Background(), "coder-007", "story-xyz")
	require.NoError(t, err, "should build system prompt")

	// Verify prompt contains all expected elements
	assert.Contains(t, prompt, "coder-007", "should contain agent ID")
	assert.Contains(t, prompt, "story-xyz", "should contain story ID")
	assert.Contains(t, prompt, "spec-abc", "should contain spec ID")
	assert.Contains(t, prompt, "Implement Authentication", "should contain story title")
	assert.Contains(t, prompt, "JWT-based authentication", "should contain story content")
	assert.Contains(t, prompt, "architect", "should mention architect role")
	assert.Contains(t, prompt, "submit_reply", "should mention submit_reply tool")
	assert.Contains(t, prompt, "review_complete", "should mention review_complete tool")
}

// TestBuildSystemPrompt_MissingStory verifies error handling for missing story.
func TestBuildSystemPrompt_MissingStory(t *testing.T) {
	queue := NewQueue(nil)
	renderer, err := templates.NewRenderer()
	require.NoError(t, err, "should create renderer")

	driver := &Driver{
		queue:    queue,
		renderer: renderer,
		logger:   logx.NewLogger("test-arch-1"),
	}

	_, err = driver.buildSystemPrompt(context.Background(), "coder-001", "nonexistent-story")
	assert.Error(t, err, "should error for missing story")
	assert.Contains(t, err.Error(), "not found", "error should mention story not found")
}

// TestContextIsolation verifies that different agents have isolated contexts.
func TestContextIsolation(t *testing.T) {
	baseSM := &agent.BaseStateMachine{}

	driver := &Driver{
		BaseStateMachine: baseSM,
		agentContexts:    make(map[string]*contextmgr.ContextManager),
		contextMutex:     sync.RWMutex{},
		logger:           logx.NewLogger("test-arch-1"),
	}

	// Get contexts for two different agents
	cm1 := driver.getContextForAgent("coder-001")
	cm2 := driver.getContextForAgent("coder-002")

	// Set different system prompts for each context to simulate reset
	cm1.ResetForNewTemplate("template-001", "System prompt for coder-001")
	cm2.ResetForNewTemplate("template-002", "System prompt for coder-002")

	// Verify contexts remain isolated
	conv1 := cm1.GetMessages()
	conv2 := cm2.GetMessages()

	assert.Equal(t, 1, len(conv1), "coder-001 should have 1 message (system prompt)")
	assert.Equal(t, 1, len(conv2), "coder-002 should have 1 message (system prompt)")
	assert.Contains(t, conv1[0].Content, "coder-001", "coder-001 context should have its system prompt")
	assert.Contains(t, conv2[0].Content, "coder-002", "coder-002 context should have its system prompt")
	assert.NotContains(t, conv1[0].Content, "coder-002", "coder-001 should not see coder-002 system prompt")
	assert.NotContains(t, conv2[0].Content, "coder-001", "coder-002 should not see coder-001 system prompt")
}

// TestConvertContextMessages verifies the helper function for message conversion.
func TestConvertContextMessages(t *testing.T) {
	contextMessages := []contextmgr.Message{
		{Role: "user", Content: "Hello"},
		{Role: "assistant", Content: "Hi there"},
		{Role: "user", Content: "How are you?"},
	}

	agentMessages := convertContextMessages(contextMessages)

	assert.Equal(t, 3, len(agentMessages), "should convert all messages")
	assert.Equal(t, agent.CompletionRole("user"), agentMessages[0].Role, "first role should be user")
	assert.Equal(t, "Hello", agentMessages[0].Content, "first content should match")
	assert.Equal(t, agent.CompletionRole("assistant"), agentMessages[1].Role, "second role should be assistant")
	assert.Equal(t, "Hi there", agentMessages[1].Content, "second content should match")
}
