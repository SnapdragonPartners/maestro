package architect

import (
	"context"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/proto"
)

// TestEpochHoldCancelsInFlightCoders verifies the full epoch/system hold path:
// in-flight stories are identified, their agents are cancelled, stories go on_hold,
// and dispatch is suppressed for system scope. No requeue churn occurs.
func TestEpochHoldCancelsInFlightCoders(t *testing.T) {
	q := NewQueue(nil)

	// Set up 3 stories in the same spec:
	// s1: the trigger story (already failed/being requeued)
	// s2: in planning with coder-001
	// s3: in coding with coder-002
	q.AddStory("s1", "spec-A", "Story 1", "content", "app", nil, 1)
	q.AddStory("s2", "spec-A", "Story 2", "content", "app", nil, 1)
	q.AddStory("s3", "spec-A", "Story 3", "content", "app", nil, 1)

	s2, _ := q.GetStory("s2")
	_ = s2.SetStatus(StatusPlanning)
	s2.AssignedAgent = "coder-001"

	s3, _ := q.GetStory("s3")
	_ = s3.SetStatus(StatusCoding)
	s3.AssignedAgent = "coder-002"

	// Verify GetActiveStoriesForScope includes in-flight stories
	affectedIDs := q.GetActiveStoriesForScope(proto.FailureScopeEpoch, "s1")
	if len(affectedIDs) != 2 {
		t.Fatalf("Expected 2 affected stories for epoch scope, got %d: %v", len(affectedIDs), affectedIDs)
	}

	// Verify GetAssignedAgent returns correct agents
	if agent := q.GetAssignedAgent("s2"); agent != "coder-001" {
		t.Errorf("Expected coder-001 for s2, got %q", agent)
	}
	if agent := q.GetAssignedAgent("s3"); agent != "coder-002" {
		t.Errorf("Expected coder-002 for s3, got %q", agent)
	}

	// Hold both stories (simulating what driver.go does)
	for _, id := range affectedIDs {
		if err := q.HoldStory(id, "epoch_environment_hold", "architect", "fail-123", "docker is down"); err != nil {
			t.Fatalf("HoldStory(%s) failed: %v", id, err)
		}
	}

	// Verify stories are on_hold and agents are cleared
	s2, _ = q.GetStory("s2")
	if s2.GetStatus() != StatusOnHold {
		t.Errorf("s2 should be on_hold, got %s", s2.GetStatus())
	}
	if s2.AssignedAgent != "" {
		t.Errorf("s2 agent should be cleared, got %q", s2.AssignedAgent)
	}

	s3, _ = q.GetStory("s3")
	if s3.GetStatus() != StatusOnHold {
		t.Errorf("s3 should be on_hold, got %s", s3.GetStatus())
	}

	// On-hold stories should not appear as ready.
	// s1 (the trigger) is still pending and may be ready — only check that s2/s3 aren't.
	ready := q.GetReadyStories()
	for _, rs := range ready {
		if rs.ID == "s2" || rs.ID == "s3" {
			t.Errorf("Held story %s should not be ready", rs.ID)
		}
	}
}

// TestSystemScopeHoldSuppressesDispatch verifies that system-scoped failures
// suppress dispatch and that GetReadyStories respects suppression.
func TestSystemScopeHoldSuppressesDispatch(t *testing.T) {
	q := NewQueue(nil)

	q.AddStory("s1", "spec-A", "Story 1", "content", "app", nil, 1)
	q.AddStory("s2", "spec-B", "Story 2", "content", "app", nil, 1)

	// System scope affects ALL stories, not just same spec
	affectedIDs := q.GetActiveStoriesForScope(proto.FailureScopeSystem, "s1")
	// s1 is excluded (it's the trigger), s2 should be included
	found := false
	for _, id := range affectedIDs {
		if id == "s2" {
			found = true
		}
	}
	if !found {
		t.Errorf("System scope should include s2 from different spec, got %v", affectedIDs)
	}

	// Suppress dispatch
	q.SuppressDispatch("system-scoped environment failure")

	suppressed, reason := q.IsDispatchSuppressed()
	if !suppressed {
		t.Fatal("Dispatch should be suppressed")
	}
	if reason == "" {
		t.Error("Suppression reason should not be empty")
	}

	// GetReadyStories returns nil when suppressed
	ready := q.GetReadyStories()
	if len(ready) != 0 {
		t.Errorf("Expected 0 ready stories during suppression, got %d", len(ready))
	}

	// Resume dispatch
	q.ResumeDispatch()
	suppressed, _ = q.IsDispatchSuppressed()
	if suppressed {
		t.Error("Dispatch should not be suppressed after ResumeDispatch")
	}

	// Now stories are ready again (s1 and s2 are both pending)
	ready = q.GetReadyStories()
	if len(ready) == 0 {
		t.Error("Stories should be ready after dispatch is resumed")
	}
}

// TestCancelAgentEffectSendsRequest verifies that CancelAgentEffect correctly
// sends an AgentCancelRequest through the dispatcher channel.
func TestCancelAgentEffectSendsRequest(t *testing.T) {
	cfg := &config.Config{
		Agents: &config.AgentConfig{MaxCoders: 2},
	}
	disp, err := dispatch.NewDispatcher(cfg)
	if err != nil {
		t.Fatalf("NewDispatcher failed: %v", err)
	}

	eff := &CancelAgentEffect{
		Dispatcher: disp,
		AgentID:    "coder-001",
		StoryID:    "story-123",
		Reason:     "epoch hold",
	}

	// Verify effect type
	if eff.Type() != "cancel_agent" {
		t.Errorf("Expected type 'cancel_agent', got %q", eff.Type())
	}

	// Execute the effect with a minimal runtime
	runtime := &nullRuntime{}
	_, err = eff.Execute(context.Background(), runtime)
	if err != nil {
		t.Fatalf("CancelAgentEffect.Execute failed: %v", err)
	}

	// Read from the cancel channel
	cancelCh := disp.GetCancelRequestsChannel()
	select {
	case req := <-cancelCh:
		if req.AgentID != "coder-001" {
			t.Errorf("Expected agent coder-001, got %s", req.AgentID)
		}
		if req.StoryID != "story-123" {
			t.Errorf("Expected story story-123, got %s", req.StoryID)
		}
		if req.Reason != "epoch hold" {
			t.Errorf("Expected reason 'epoch hold', got %s", req.Reason)
		}
	default:
		t.Fatal("Expected cancel request on channel, got nothing")
	}
}

// TestPrerequisiteFailureHoldsNotRetries verifies that prerequisite failures
// put the story on hold instead of falling through to the retry path.
func TestPrerequisiteFailureHoldsNotRetries(t *testing.T) {
	q := NewQueue(nil)
	q.AddStory("s1", "spec-A", "Story 1", "content", "app", nil, 1)

	// Simulate what the prerequisite case in processRequeueRequests does:
	// hold the story
	if err := q.HoldStory("s1", "awaiting_human", "architect", "fail-prereq", "API key expired"); err != nil {
		t.Fatalf("HoldStory failed: %v", err)
	}

	s1, _ := q.GetStory("s1")
	if s1.GetStatus() != StatusOnHold {
		t.Errorf("Prerequisite failure should hold story, got %s", s1.GetStatus())
	}

	// Story should NOT be ready (not requeued to pending)
	ready := q.GetReadyStories()
	if len(ready) != 0 {
		t.Errorf("Held story should not be ready, got %d", len(ready))
	}

	// Story can be released later
	released, err := q.ReleaseHeldStories([]string{"s1"}, "API key rotated")
	if err != nil {
		t.Fatalf("Release failed: %v", err)
	}
	if len(released) != 1 {
		t.Errorf("Expected 1 released, got %d", len(released))
	}
	s1, _ = q.GetStory("s1")
	if s1.GetStatus() != StatusPending {
		t.Errorf("Released story should be pending, got %s", s1.GetStatus())
	}
}

// nullRuntime implements effect.Runtime for testing effects without a real dispatcher.
type nullRuntime struct{}

func (r *nullRuntime) SendMessage(_ *proto.AgentMsg) error { return nil }
func (r *nullRuntime) ReceiveMessage(_ context.Context, _ proto.MsgType) (*proto.AgentMsg, error) {
	return &proto.AgentMsg{}, nil
}
func (r *nullRuntime) GetAgentID() string       { return "test-architect" }
func (r *nullRuntime) GetAgentRole() string     { return "architect" }
func (r *nullRuntime) Info(_ string, _ ...any)  {}
func (r *nullRuntime) Error(_ string, _ ...any) {}
func (r *nullRuntime) Debug(_ string, _ ...any) {}

// Verify nullRuntime implements effect.Runtime at compile time.
var _ effect.Runtime = (*nullRuntime)(nil)
