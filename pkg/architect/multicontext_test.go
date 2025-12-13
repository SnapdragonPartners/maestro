package architect

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
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

// TestResetAgentContext is skipped - requires real dispatcher with complex dependencies.
// The functionality is tested through buildSystemPrompt which is the core logic.

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

	prompt, err := driver.buildSystemPrompt("coder-007", "story-xyz")
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

	_, err = driver.buildSystemPrompt("coder-001", "nonexistent-story")
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
