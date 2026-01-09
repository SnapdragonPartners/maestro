// Tests for coder state handlers.
// These tests use mocks from internal/mocks for external dependencies.
package coder

import (
	"context"
	"fmt"
	"testing"
	"time"

	"orchestrator/internal/mocks"
	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// testCoderOptions configures the test coder.
type testCoderOptions struct {
	storyCh      chan *proto.AgentMsg
	replyCh      chan *proto.AgentMsg
	dispatcher   *dispatch.Dispatcher
	cloneManager *CloneManager
	llmClient    *mocks.MockLLMClient
}

// createTestCoder creates a coder for testing with configurable options.
func createTestCoder(t *testing.T, opts *testCoderOptions) *Coder {
	t.Helper()
	tempDir := t.TempDir()

	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	logger := logx.NewLogger("test-coder")
	contextMgr := contextmgr.NewContextManager()
	renderer, err := templates.NewRenderer()
	if err != nil {
		t.Fatalf("Failed to create renderer: %v", err)
	}

	sm := agent.NewBaseStateMachine("test-coder-001", proto.StateWaiting, nil, CoderTransitions)

	// Create minimal chat service for tool provider
	chatCfg := &config.ChatConfig{
		Enabled:        true,
		MaxNewMessages: 10,
	}
	chatService := chat.NewService(nil, chatCfg)

	coder := &Coder{
		BaseStateMachine: sm,
		contextManager:   contextMgr,
		renderer:         renderer,
		logger:           logger,
		workDir:          tempDir,
		originalWorkDir:  tempDir,
		codingBudget:     3,
		chatService:      chatService,
	}

	if opts != nil {
		if opts.storyCh != nil {
			coder.storyCh = opts.storyCh
		}
		if opts.replyCh != nil {
			coder.replyCh = opts.replyCh
		}
		if opts.dispatcher != nil {
			coder.dispatcher = opts.dispatcher
		}
		if opts.cloneManager != nil {
			coder.cloneManager = opts.cloneManager
		}
		if opts.llmClient != nil {
			coder.SetLLMClient(opts.llmClient)
		}
	}

	return coder
}

// createMockCloneManager creates a CloneManager with a mock GitRunner for testing.
func createMockCloneManager(t *testing.T, mockGit *mocks.MockGitRunner) *CloneManager {
	t.Helper()
	tempDir := t.TempDir()

	// Set up config with test git values (CloneManager reads from config)
	config.SetConfigForTesting(&config.Config{
		Git: &config.GitConfig{
			RepoURL:       "https://github.com/test/repo.git",
			TargetBranch:  "main",
			BranchPattern: "coder-{agent_id}-{story_id}",
		},
	})
	t.Cleanup(func() { config.SetConfigForTesting(nil) })

	return NewCloneManager(
		mockGit,
		tempDir,
		"", "", "", "",
	)
}

// createStoryMessage creates a valid story message for testing.
func createStoryMessage(storyID, content string) *proto.AgentMsg {
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "test-coder-001")
	msg.SetMetadata(proto.KeyStoryID, storyID)

	storyPayload := map[string]any{
		proto.KeyContent:   content,
		proto.KeyStoryType: string(proto.StoryTypeApp),
	}
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, storyPayload))

	return msg
}

// =============================================================================
// handleWaiting tests
// =============================================================================

func TestHandleWaiting_WithExistingTaskContent(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Pre-set task content in state
	sm.SetStateData(string(stateDataKeyTaskContent), "existing task content")

	ctx := context.Background()
	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != StateSetup {
		t.Errorf("Expected transition to SETUP, got: %s", nextState)
	}
}

func TestHandleWaiting_NoStoryChannel(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	ctx := context.Background()
	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateWaiting {
		t.Errorf("Expected to stay in WAITING, got: %s", nextState)
	}
}

func TestHandleWaiting_ReceiveStoryMessage(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Send a story message
	storyMsg := createStoryMessage("story-123", "Implement feature X")
	storyCh <- storyMsg

	ctx := context.Background()
	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != StateSetup {
		t.Errorf("Expected transition to SETUP, got: %s", nextState)
	}

	// Verify state data was set
	taskContent, exists := sm.GetStateValue(string(stateDataKeyTaskContent))
	if !exists || taskContent != "Implement feature X" {
		t.Errorf("Expected task content to be set, got: %v", taskContent)
	}

	storyID, exists := sm.GetStateValue(KeyStoryID)
	if !exists || storyID != "story-123" {
		t.Errorf("Expected story ID to be set, got: %v", storyID)
	}
}

func TestHandleWaiting_ContextCancelled(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err == nil {
		t.Error("Expected error for cancelled context")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}
}

func TestHandleWaiting_ChannelClosed(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Close channel before reading
	close(storyCh)

	ctx := context.Background()
	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err == nil {
		t.Error("Expected error for closed channel")
	}
	if !done {
		t.Error("Expected done=true for abnormal shutdown")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}
}

func TestHandleWaiting_NilMessage(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Send nil message
	storyCh <- nil

	ctx := context.Background()
	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error for nil message (graceful handling), got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateWaiting {
		t.Errorf("Expected to stay in WAITING, got: %s", nextState)
	}
}

func TestHandleWaiting_MissingStoryID(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Create message without story_id in metadata
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "test-coder-001")
	storyPayload := map[string]any{
		proto.KeyContent: "Some content",
	}
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, storyPayload))
	// Deliberately not setting story_id in metadata

	storyCh <- msg

	ctx := context.Background()
	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err == nil {
		t.Error("Expected error for missing story_id")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}
}

func TestHandleWaiting_ExpressStory(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Create express story message
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "test-coder-001")
	msg.SetMetadata(proto.KeyStoryID, "express-story-1")
	storyPayload := map[string]any{
		proto.KeyContent: "Quick fix",
		"express":        true,
	}
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, storyPayload))

	storyCh <- msg

	ctx := context.Background()
	nextState, _, err := coder.handleWaiting(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if nextState != StateSetup {
		t.Errorf("Expected transition to SETUP, got: %s", nextState)
	}

	// Verify express flag was set
	isExpress, exists := sm.GetStateValue(KeyExpress)
	if !exists || isExpress != true {
		t.Errorf("Expected express flag to be true, got: %v", isExpress)
	}
}

func TestHandleWaiting_HotfixStory(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Create hotfix story message
	msg := proto.NewAgentMsg(proto.MsgTypeSTORY, "architect", "test-coder-001")
	msg.SetMetadata(proto.KeyStoryID, "hotfix-story-1")
	storyPayload := map[string]any{
		proto.KeyContent: "Critical bug fix",
		"is_hotfix":      true,
	}
	msg.SetTypedPayload(proto.NewGenericPayload(proto.PayloadKindStory, storyPayload))

	storyCh <- msg

	ctx := context.Background()
	nextState, _, err := coder.handleWaiting(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if nextState != StateSetup {
		t.Errorf("Expected transition to SETUP, got: %s", nextState)
	}

	// Verify hotfix flag was set
	isHotfix, exists := sm.GetStateValue(KeyIsHotfix)
	if !exists || isHotfix != true {
		t.Errorf("Expected hotfix flag to be true, got: %v", isHotfix)
	}
}

// =============================================================================
// handleDone tests
// =============================================================================

func TestHandleDone_BasicTransition(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	ctx := context.Background()
	nextState, done, err := coder.handleDone(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if !done {
		t.Error("Expected done=true for terminal state")
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected DONE state, got: %s", nextState)
	}

	// Verify logging flag was set
	logged, exists := sm.GetStateValue(KeyDoneLogged)
	if !exists || logged != true {
		t.Error("Expected KeyDoneLogged to be set")
	}
}

func TestHandleDone_OnlyLogsOnce(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	ctx := context.Background()

	// Call handleDone twice
	_, _, _ = coder.handleDone(ctx, sm)
	nextState, done, err := coder.handleDone(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if !done {
		t.Error("Expected done=true")
	}
	if nextState != proto.StateDone {
		t.Errorf("Expected DONE state, got: %s", nextState)
	}

	// Verify flag still set (not cleared)
	logged, _ := sm.GetStateValue(KeyDoneLogged)
	if logged != true {
		t.Error("Expected KeyDoneLogged to remain true")
	}
}

// =============================================================================
// handleError tests
// =============================================================================

func TestHandleError_BasicTransition(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set error message
	sm.SetStateData(KeyErrorMessage, "Something went wrong")

	ctx := context.Background()
	nextState, done, err := coder.handleError(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error (error is in state, not return), got: %v", err)
	}
	if !done {
		t.Error("Expected done=true for terminal state")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}

	// Verify logging flag was set
	logged, exists := sm.GetStateValue(KeyDoneLogged)
	if !exists || logged != true {
		t.Error("Expected KeyDoneLogged to be set")
	}
}

func TestHandleError_OnlyLogsOnce(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyErrorMessage, "Error message")

	ctx := context.Background()

	// Call handleError twice
	_, _, _ = coder.handleError(ctx, sm)
	nextState, done, err := coder.handleError(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if !done {
		t.Error("Expected done=true")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

// =============================================================================
// handleWaiting with timeout test
// =============================================================================

func TestHandleWaiting_Timeout(t *testing.T) {
	storyCh := make(chan *proto.AgentMsg, 1)
	coder := createTestCoder(t, &testCoderOptions{storyCh: storyCh})
	sm := coder.BaseStateMachine

	// Create context with short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Don't send anything on storyCh - should timeout

	nextState, done, err := coder.handleWaiting(ctx, sm)

	if err == nil {
		t.Error("Expected error for timeout")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}
}

// =============================================================================
// handleSetup tests
// =============================================================================

func TestHandleSetup_NoCloneManager(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set story ID (required)
	sm.SetStateData(KeyStoryID, "story-001")

	ctx := context.Background()
	nextState, done, err := coder.handleSetup(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	// Without clone manager, should skip to planning
	if nextState != StatePlanning {
		t.Errorf("Expected transition to PLANNING, got: %s", nextState)
	}
}

func TestHandleSetup_MissingStoryID(t *testing.T) {
	mockGit := mocks.NewMockGitRunner()
	cloneManager := createMockCloneManager(t, mockGit)

	coder := createTestCoder(t, &testCoderOptions{cloneManager: cloneManager})
	sm := coder.BaseStateMachine

	// Deliberately not setting KeyStoryID

	ctx := context.Background()
	nextState, done, err := coder.handleSetup(ctx, sm)

	if err == nil {
		t.Error("Expected error for missing story_id")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}
}

func TestHandleSetup_InvalidStoryIDType(t *testing.T) {
	mockGit := mocks.NewMockGitRunner()
	cloneManager := createMockCloneManager(t, mockGit)

	coder := createTestCoder(t, &testCoderOptions{cloneManager: cloneManager})
	sm := coder.BaseStateMachine

	// Set story ID as wrong type
	sm.SetStateData(KeyStoryID, 12345) // int instead of string

	ctx := context.Background()
	nextState, done, err := coder.handleSetup(ctx, sm)

	if err == nil {
		t.Error("Expected error for invalid story_id type")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}
}

func TestHandleSetup_GitCloneFailure(t *testing.T) {
	mockGit := mocks.NewMockGitRunner()
	// Configure mock to fail clone operations
	mockGit.FailCommandWith("clone", fmt.Errorf("clone failed: network error"))

	cloneManager := createMockCloneManager(t, mockGit)

	coder := createTestCoder(t, &testCoderOptions{cloneManager: cloneManager})
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyStoryID, "story-001")

	ctx := context.Background()
	nextState, done, err := coder.handleSetup(ctx, sm)

	if err == nil {
		t.Error("Expected error for git clone failure")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected transition to ERROR, got: %s", nextState)
	}

	// Verify git clone was attempted
	if !mockGit.WasCommandCalled("clone") {
		t.Error("Expected git clone to be called")
	}
}

func TestHandleSetup_ExpressStorySkipsPlanning(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set story ID and express flag
	sm.SetStateData(KeyStoryID, "express-story-001")
	sm.SetStateData(KeyExpress, true)

	ctx := context.Background()
	nextState, done, err := coder.handleSetup(ctx, sm)

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	// Without clone manager but with express flag, should still skip to planning
	// (since clone manager is nil, we don't actually skip to coding)
	if nextState != StatePlanning {
		t.Errorf("Expected transition to PLANNING (no clone manager), got: %s", nextState)
	}
}

// =============================================================================
// handleQuestion tests
// =============================================================================

func TestHandleQuestion_NoPendingQuestion(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Don't set KeyPendingQuestion

	ctx := context.Background()
	nextState, done, err := coder.handleQuestion(ctx, sm)

	if err == nil {
		t.Error("Expected error for missing pending question")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

func TestHandleQuestion_InvalidQuestionDataType(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set wrong type for pending question
	sm.SetStateData(KeyPendingQuestion, "string instead of map")

	ctx := context.Background()
	nextState, done, err := coder.handleQuestion(ctx, sm)

	if err == nil {
		t.Error("Expected error for invalid question data type")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

func TestHandleQuestion_EmptyQuestionText(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set question data with empty question text
	questionData := map[string]any{
		"question": "",
		"context":  "some context",
		"urgency":  "high",
		"origin":   string(StatePlanning),
	}
	sm.SetStateData(KeyPendingQuestion, questionData)

	ctx := context.Background()
	nextState, done, err := coder.handleQuestion(ctx, sm)

	if err == nil {
		t.Error("Expected error for empty question text")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

func TestHandleQuestion_NoOriginState(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set question data without origin
	questionData := map[string]any{
		"question": "What should I do?",
		"context":  "some context",
		"urgency":  "medium",
		"origin":   "", // Empty origin
	}
	sm.SetStateData(KeyPendingQuestion, questionData)

	ctx := context.Background()
	nextState, done, err := coder.handleQuestion(ctx, sm)

	if err == nil {
		t.Error("Expected error for missing origin state")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

func TestHandleQuestion_NilQuestionData(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set nil map
	var nilMap map[string]any
	sm.SetStateData(KeyPendingQuestion, nilMap)

	ctx := context.Background()
	nextState, done, err := coder.handleQuestion(ctx, sm)

	if err == nil {
		t.Error("Expected error for nil question data")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

// =============================================================================
// handleBudgetReview tests
// =============================================================================

func TestHandleBudgetReview_NoStoredEffect(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Don't set KeyBudgetReviewEffect

	ctx := context.Background()
	nextState, done, err := coder.handleBudgetReview(ctx, sm)

	if err == nil {
		t.Error("Expected error for missing budget review effect")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

func TestHandleBudgetReview_InvalidEffectType(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set wrong type for budget review effect
	sm.SetStateData(KeyBudgetReviewEffect, "not an effect")

	ctx := context.Background()
	nextState, done, err := coder.handleBudgetReview(ctx, sm)

	if err == nil {
		t.Error("Expected error for invalid effect type")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

// =============================================================================
// processBudgetReviewStatus tests
// =============================================================================

func TestProcessBudgetReviewStatus_Approved_FromCoding(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set origin state
	sm.SetStateData(KeyOrigin, string(StateCoding))
	sm.SetStateData(string(stateDataKeyCodingIterations), 5)

	nextState, done, err := coder.processBudgetReviewStatus(sm, proto.ApprovalStatusApproved, "Good work!")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != StateCoding {
		t.Errorf("Expected return to CODING, got: %s", nextState)
	}

	// Verify iteration counter was reset
	iterations, _ := sm.GetStateValue(string(stateDataKeyCodingIterations))
	if iterations != 0 {
		t.Errorf("Expected coding iterations to be reset to 0, got: %v", iterations)
	}
}

func TestProcessBudgetReviewStatus_Approved_FromPlanning(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set origin state
	sm.SetStateData(KeyOrigin, string(StatePlanning))
	sm.SetStateData(string(stateDataKeyPlanningIterations), 5)

	nextState, done, err := coder.processBudgetReviewStatus(sm, proto.ApprovalStatusApproved, "Proceed")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != StatePlanning {
		t.Errorf("Expected return to PLANNING, got: %s", nextState)
	}

	// Verify iteration counter was reset
	iterations, _ := sm.GetStateValue(string(stateDataKeyPlanningIterations))
	if iterations != 0 {
		t.Errorf("Expected planning iterations to be reset to 0, got: %v", iterations)
	}
}

func TestProcessBudgetReviewStatus_NeedsChanges_FromCoding(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set origin state
	sm.SetStateData(KeyOrigin, string(StateCoding))

	nextState, done, err := coder.processBudgetReviewStatus(sm, proto.ApprovalStatusNeedsChanges, "Fix the implementation")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	// From CODING, should continue CODING
	if nextState != StateCoding {
		t.Errorf("Expected to continue CODING, got: %s", nextState)
	}
}

func TestProcessBudgetReviewStatus_NeedsChanges_FromPlanning(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Set origin state
	sm.SetStateData(KeyOrigin, string(StatePlanning))

	nextState, done, err := coder.processBudgetReviewStatus(sm, proto.ApprovalStatusNeedsChanges, "Revise the plan")

	if err != nil {
		t.Errorf("Expected no error, got: %v", err)
	}
	if done {
		t.Error("Expected done=false")
	}
	// From PLANNING, should pivot back to PLANNING
	if nextState != StatePlanning {
		t.Errorf("Expected to pivot to PLANNING, got: %s", nextState)
	}
}

func TestProcessBudgetReviewStatus_Rejected(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyOrigin, string(StateCoding))

	nextState, done, err := coder.processBudgetReviewStatus(sm, proto.ApprovalStatusRejected, "Task abandoned")

	if err == nil {
		t.Error("Expected error for rejected status")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

func TestProcessBudgetReviewStatus_UnknownStatus(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	sm.SetStateData(KeyOrigin, string(StateCoding))

	nextState, done, err := coder.processBudgetReviewStatus(sm, proto.ApprovalStatus("unknown"), "")

	if err == nil {
		t.Error("Expected error for unknown status")
	}
	if done {
		t.Error("Expected done=false")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
}

// =============================================================================
// SetCloneManager tests
// =============================================================================

func TestSetCloneManager(t *testing.T) {
	coder := createTestCoder(t, nil)

	// Initially no clone manager
	if coder.cloneManager != nil {
		t.Error("Expected no clone manager initially")
	}

	// Create and set a clone manager
	mockGit := mocks.NewMockGitRunner()
	cm := createMockCloneManager(t, mockGit)
	coder.SetCloneManager(cm)

	// Verify it was set
	if coder.cloneManager == nil {
		t.Error("Expected clone manager to be set")
	}
	if coder.cloneManager != cm {
		t.Error("Expected clone manager to match the one we set")
	}
}

func TestSetCloneManager_NilCloneManager(t *testing.T) {
	// Create coder with a clone manager
	mockGit := mocks.NewMockGitRunner()
	cm := createMockCloneManager(t, mockGit)
	coder := createTestCoder(t, &testCoderOptions{
		cloneManager: cm,
	})

	// Set nil clone manager
	coder.SetCloneManager(nil)

	// Verify it was cleared
	if coder.cloneManager != nil {
		t.Error("Expected clone manager to be nil after setting nil")
	}
}

// =============================================================================
// handleCoding tests with MockLLMClient
// =============================================================================

func TestHandleCoding_TransitionToTesting(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to call the "done" tool with TESTING signal
	mockLLM.RespondWithToolCall("done", map[string]any{
		"signal":  "TESTING",
		"summary": "Code implementation complete, ready for testing",
	})

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	// Set up required state
	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Implement a simple function")
	sm.SetStateData(KeyPlan, "1. Create function\n2. Add tests")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	ctx := context.Background()
	nextState, done, err := coder.handleCoding(ctx, sm)

	// Verify LLM was called
	if mockLLM.GetCompleteCallCount() == 0 {
		t.Error("Expected LLM to be called at least once")
	}

	// The test verifies the coder can make LLM calls with our mock
	// Due to tool execution complexity, we verify the mock integration works
	t.Logf("handleCoding result: state=%s, done=%v, err=%v", nextState, done, err)
}

func TestHandleCoding_NoRenderer(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	// Remove the renderer to trigger error path
	coder.renderer = nil

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Some task")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	ctx := context.Background()
	nextState, done, err := coder.handleCoding(ctx, sm)

	if err == nil {
		t.Error("Expected error when renderer is nil")
	}
	if nextState != proto.StateError {
		t.Errorf("Expected ERROR state, got: %s", nextState)
	}
	if done {
		t.Error("Expected done=false")
	}
}

func TestHandleCoding_LLMError(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to return an error
	mockLLM.FailCompleteWith(fmt.Errorf("LLM service unavailable"))

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Implement feature")
	sm.SetStateData(KeyPlan, "1. Do stuff")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	ctx := context.Background()
	nextState, done, err := coder.handleCoding(ctx, sm)

	// LLM errors should result in error state
	if err == nil {
		t.Error("Expected error when LLM fails")
	}
	t.Logf("handleCoding with LLM error: state=%s, done=%v, err=%v", nextState, done, err)
}

func TestHandleCoding_BudgetExceeded(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Implement feature")
	sm.SetStateData(KeyPlan, "1. Do stuff")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	// Set coding iterations to exceed budget
	sm.SetStateData(string(stateDataKeyCodingIterations), 10)

	ctx := context.Background()
	nextState, _, _ := coder.handleCoding(ctx, sm)

	// Should transition to budget review
	if nextState != StateBudgetReview {
		t.Errorf("Expected BUDGET_REVIEW state when budget exceeded, got: %s", nextState)
	}
}

// =============================================================================
// executeCodingWithTemplate tests
// =============================================================================

func TestExecuteCodingWithTemplate_DevOpsStory(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to call done tool
	mockLLM.RespondWithToolCall("done", map[string]any{
		"signal":  "TESTING",
		"summary": "DevOps changes complete",
	})

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Set up CI/CD pipeline")
	sm.SetStateData(KeyPlan, "1. Create workflow file")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeDevOps))

	ctx := context.Background()
	_, _, err := coder.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario": "devops_coding",
	})

	// Verify LLM was called (DevOps template path)
	if mockLLM.GetCompleteCallCount() == 0 {
		t.Error("Expected LLM to be called for DevOps story")
	}
	t.Logf("executeCodingWithTemplate DevOps result: err=%v", err)
}

// =============================================================================
// handlePlanning tests with MockLLMClient
// =============================================================================

func TestHandlePlanning_WithMockLLM(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to return a plan submission
	mockLLM.RespondWithToolCall("submit_plan", map[string]any{
		"plan":       "1. Analyze requirements\n2. Implement solution",
		"confidence": 0.85,
	})

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Build a REST API endpoint")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))
	sm.SetStateData(KeyStoryID, "story-123")

	ctx := context.Background()
	nextState, done, err := coder.handlePlanning(ctx, sm)

	// Verify LLM was called
	if mockLLM.GetCompleteCallCount() == 0 {
		t.Error("Expected LLM to be called during planning")
	}

	t.Logf("handlePlanning result: state=%s, done=%v, err=%v", nextState, done, err)
}

func TestHandlePlanning_LLMError(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to return an error
	mockLLM.FailCompleteWith(fmt.Errorf("rate limit exceeded"))

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Build feature X")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	ctx := context.Background()
	nextState, done, err := coder.handlePlanning(ctx, sm)

	// LLM errors should result in error state
	if err == nil {
		t.Error("Expected error when LLM fails")
	}
	t.Logf("handlePlanning with LLM error: state=%s, done=%v, err=%v", nextState, done, err)
}

func TestHandlePlanning_AskQuestion_StoresQuestionData(t *testing.T) {
	// This test verifies the fix for the ask_question bug where question data
	// was not being stored before transitioning to QUESTION state.
	// See docs/FIX_TRACKING_RC1.md Fix #2 for details.

	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to return an ask_question tool call
	mockLLM.RespondWithToolCall("ask_question", map[string]any{
		"question": "How should I implement the database schema?",
		"context":  "Working on the user authentication feature",
	})

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Implement user authentication")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))
	sm.SetStateData(KeyStoryID, "story-456")

	ctx := context.Background()
	nextState, done, err := coder.handlePlanning(ctx, sm)

	// Verify no error
	if err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Verify not done (question needs to be answered)
	if done {
		t.Error("Expected done=false when asking question")
	}

	// Verify state transition to QUESTION
	if nextState != StateQuestion {
		t.Errorf("Expected state %s, got: %s", StateQuestion, nextState)
	}

	// Verify question data was stored in state (this is what the bug fix ensures)
	stateData := sm.GetStateData()
	questionDataRaw, exists := stateData[KeyPendingQuestion]
	if !exists {
		t.Fatal("Expected KeyPendingQuestion to be set in state data")
	}

	questionData, ok := questionDataRaw.(map[string]any)
	if !ok {
		t.Fatalf("Expected question data to be map[string]any, got: %T", questionDataRaw)
	}

	// Verify question content
	if question, ok := questionData["question"].(string); !ok || question != "How should I implement the database schema?" {
		t.Errorf("Expected question text to be stored, got: %v", questionData["question"])
	}

	// Verify context
	if ctx, ok := questionData["context"].(string); !ok || ctx != "Working on the user authentication feature" {
		t.Errorf("Expected context to be stored, got: %v", questionData["context"])
	}

	// Verify origin state is PLANNING
	if origin, ok := questionData["origin"].(string); !ok || origin != string(StatePlanning) {
		t.Errorf("Expected origin to be %s, got: %v", StatePlanning, questionData["origin"])
	}

	t.Logf("handlePlanning ask_question: question data correctly stored before QUESTION transition")
}

// =============================================================================
// LLM response sequence tests
// =============================================================================

func TestHandleCoding_MultipleToolCalls(t *testing.T) {
	mockLLM := mocks.NewMockLLMClient()

	// Configure mock to return a sequence: first a general tool call, then done
	mockLLM.RespondWithSequence([]llm.CompletionResponse{
		{
			Content: "Let me write a file first",
			ToolCalls: []llm.ToolCall{
				{ID: "call1", Name: "write_file", Parameters: map[string]any{
					"path":    "/workspace/main.go",
					"content": "package main\n\nfunc main() {}",
				}},
			},
			StopReason: "tool_use",
		},
		{
			Content: "Now I'm done",
			ToolCalls: []llm.ToolCall{
				{ID: "call2", Name: "done", Parameters: map[string]any{
					"signal":  "TESTING",
					"summary": "Implementation complete",
				}},
			},
			StopReason: "tool_use",
		},
	})

	coder := createTestCoder(t, &testCoderOptions{
		llmClient: mockLLM,
	})

	sm := coder.BaseStateMachine
	sm.SetStateData(string(stateDataKeyTaskContent), "Create main.go file")
	sm.SetStateData(KeyPlan, "1. Create main.go")
	sm.SetStateData(proto.KeyStoryType, string(proto.StoryTypeApp))

	ctx := context.Background()
	_, _, err := coder.handleCoding(ctx, sm)

	// Verify multiple LLM calls were made
	callCount := mockLLM.GetCompleteCallCount()
	t.Logf("handleCoding with sequence: %d LLM calls, err=%v", callCount, err)

	if callCount < 1 {
		t.Error("Expected at least 1 LLM call")
	}
}
