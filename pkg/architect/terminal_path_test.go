package architect

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

func newTerminalTestDriver(t *testing.T) (*Driver, *dispatch.Dispatcher) {
	t.Helper()

	cfg := &config.Config{
		Agents: &config.AgentConfig{MaxCoders: 2},
	}
	dispatcher, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	queue := NewQueue(nil)
	baseSM := agent.NewBaseStateMachine("test-arch", StateWaiting, nil, architectTransitions)
	persistCh := make(chan<- *persistence.Request, 10)

	driver := &Driver{
		BaseStateMachine:   baseSM,
		agentContexts:      make(map[string]*contextmgr.ContextManager),
		contextMutex:       sync.RWMutex{},
		queue:              queue,
		dispatcher:         dispatcher,
		logger:             logx.NewLogger("test-arch"),
		persistenceChannel: persistCh,
		workDir:            t.TempDir(),
	}

	return driver, dispatcher
}

func TestMonitoringTransitionsDoneOnTerminal(t *testing.T) {
	driver, disp := newTerminalTestDriver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := disp.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithCancel(context.Background())
		defer stopCancel()
		disp.Stop(stopCtx)
	}()

	driver.queue.AddStory("s1", "spec-1", "Story 1", "content", "app", nil, 1)
	driver.queue.AddStory("s2", "spec-1", "Story 2", "content", "app", nil, 1)

	s1, _ := driver.queue.GetStory("s1")
	_ = s1.SetStatus(StatusDone)
	s2, _ := driver.queue.GetStory("s2")
	_ = s2.SetStatus(StatusFailed)
	s2.LastFailReason = "git corruption"

	// AllStoriesCompleted should be false (s2 is failed, not done)
	if driver.queue.AllStoriesCompleted() {
		t.Fatal("AllStoriesCompleted should be false with a failed story")
	}
	// AllStoriesTerminal should be true
	if !driver.queue.AllStoriesTerminal() {
		t.Fatal("AllStoriesTerminal should be true")
	}

	nextState, err := driver.handleMonitoring(ctx)
	if err != nil {
		t.Fatalf("handleMonitoring returned error: %v", err)
	}
	if nextState != StateDone {
		t.Errorf("Expected DONE state, got: %s", nextState)
	}

	if !driver.pmAllTerminalNotified {
		t.Error("Expected pmAllTerminalNotified to be true after terminal notification")
	}
}

func TestDispatchingTransitionsDoneOnTerminal(t *testing.T) {
	driver, disp := newTerminalTestDriver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := disp.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithCancel(context.Background())
		defer stopCancel()
		disp.Stop(stopCtx)
	}()

	driver.queue.AddStory("s1", "spec-1", "Story 1", "content", "app", nil, 1)
	driver.queue.AddStory("s2", "spec-1", "Story 2", "content", "app", nil, 1)

	s1, _ := driver.queue.GetStory("s1")
	_ = s1.SetStatus(StatusDone)
	s2, _ := driver.queue.GetStory("s2")
	_ = s2.SetStatus(StatusFailed)
	s2.LastFailReason = "story invalid"

	nextState, err := driver.handleDispatching(ctx)
	if err != nil {
		t.Fatalf("handleDispatching returned error: %v", err)
	}
	if nextState != StateDone {
		t.Errorf("Expected DONE state, got: %s", nextState)
	}

	if !driver.pmAllTerminalNotified {
		t.Error("Expected pmAllTerminalNotified to be true")
	}
}

func TestTerminalNotificationSentOnlyOnce(t *testing.T) {
	driver, disp := newTerminalTestDriver(t)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	if err := disp.Start(ctx); err != nil {
		t.Fatalf("Failed to start dispatcher: %v", err)
	}
	defer func() {
		stopCtx, stopCancel := context.WithCancel(context.Background())
		defer stopCancel()
		disp.Stop(stopCtx)
	}()

	driver.queue.AddStory("s1", "spec-1", "Story 1", "content", "app", nil, 1)
	s1, _ := driver.queue.GetStory("s1")
	_ = s1.SetStatus(StatusFailed)
	s1.LastFailReason = "broken"

	// First call should send notification
	err := driver.notifyPMAllStoriesTerminal(ctx)
	if err != nil {
		t.Fatalf("First notification failed: %v", err)
	}
	if !driver.pmAllTerminalNotified {
		t.Error("Expected flag to be true after first call")
	}

	// Second call should be a no-op (flag already set)
	err = driver.notifyPMAllStoriesTerminal(ctx)
	if err != nil {
		t.Fatalf("Second notification should not fail: %v", err)
	}
}

func TestPMClearsInFlightOnTerminalNotification(t *testing.T) {
	payload := &proto.AllStoriesTerminalPayload{
		SpecID:       "spec-42",
		TotalStories: 3,
		FailedStories: []proto.FailedStoryDetail{
			{StoryID: "s2", Title: "Fix auth", Reason: "git corruption"},
		},
		Timestamp: "2026-04-18T12:00:00Z",
	}

	msgPayload := proto.NewAllStoriesTerminalPayload(payload)

	extracted, err := msgPayload.ExtractAllStoriesTerminal()
	if err != nil {
		t.Fatalf("ExtractAllStoriesTerminal failed: %v", err)
	}

	if extracted.SpecID != "spec-42" {
		t.Errorf("Expected spec-42, got %s", extracted.SpecID)
	}
	if len(extracted.FailedStories) != 1 {
		t.Fatalf("Expected 1 failed story, got %d", len(extracted.FailedStories))
	}
	if extracted.FailedStories[0].Reason != "git corruption" {
		t.Errorf("Expected reason 'git corruption', got %s", extracted.FailedStories[0].Reason)
	}

	// Verify payload kind is correct for PM routing
	if msgPayload.Kind != proto.PayloadKindAllStoriesTerminal {
		t.Errorf("Expected kind %s, got %s", proto.PayloadKindAllStoriesTerminal, msgPayload.Kind)
	}

	// Build a full message as the architect would
	notifyMsg := proto.NewAgentMsg(proto.MsgTypeRESPONSE, "architect", "pm-001")
	notifyMsg.SetTypedPayload(msgPayload)

	if notifyMsg.Type != proto.MsgTypeRESPONSE {
		t.Errorf("Expected RESPONSE type, got %s", notifyMsg.Type)
	}
	if notifyMsg.GetTypedPayload().Kind != proto.PayloadKindAllStoriesTerminal {
		t.Errorf("Payload kind should survive round-trip through AgentMsg")
	}

	// Verify the PM can extract the same data from the message
	typed := notifyMsg.GetTypedPayload()
	pmExtracted, err := typed.ExtractAllStoriesTerminal()
	if err != nil {
		t.Fatalf("PM extraction failed: %v", err)
	}
	if pmExtracted.TotalStories != 3 {
		t.Errorf("PM should see TotalStories=3, got %d", pmExtracted.TotalStories)
	}

	// Verify the failure details survive serialization
	expectedMsg := fmt.Sprintf("%d out of %d stories failed", len(pmExtracted.FailedStories), pmExtracted.TotalStories)
	if len(pmExtracted.FailedStories) != 1 || pmExtracted.TotalStories != 3 {
		t.Errorf("Failure summary should indicate 1 of 3 failed, got: %s", expectedMsg)
	}
}
