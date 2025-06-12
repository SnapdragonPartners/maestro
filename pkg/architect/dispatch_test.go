package architect

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/proto"
)

func TestNewStoryDispatcher(t *testing.T) {
	queue := NewQueue("/tmp/test")

	storyDispatcher := NewMockStoryDispatcher(queue)

	if storyDispatcher == nil {
		t.Fatal("NewStoryDispatcher returned nil")
	}

	if storyDispatcher.queue != queue {
		t.Error("Queue not set correctly")
	}

	if !storyDispatcher.mockMode {
		t.Error("Should be in mock mode")
	}

	if storyDispatcher.policy == nil {
		t.Error("Default policy not set")
	}

	// Check default policy values
	if storyDispatcher.policy.MaxAgentsPerStory != 1 {
		t.Errorf("Expected MaxAgentsPerStory 1, got %d", storyDispatcher.policy.MaxAgentsPerStory)
	}

	if storyDispatcher.policy.MaxStoriesPerAgent != 1 {
		t.Errorf("Expected MaxStoriesPerAgent 1, got %d", storyDispatcher.policy.MaxStoriesPerAgent)
	}
}

func TestDefaultAssignmentPolicy(t *testing.T) {
	policy := DefaultAssignmentPolicy()

	if policy.MaxAgentsPerStory != 1 {
		t.Errorf("Expected MaxAgentsPerStory 1, got %d", policy.MaxAgentsPerStory)
	}

	if policy.MaxStoriesPerAgent != 1 {
		t.Errorf("Expected MaxStoriesPerAgent 1, got %d", policy.MaxStoriesPerAgent)
	}

	if len(policy.PreferredAgentTypes) != 1 || policy.PreferredAgentTypes[0] != "coder" {
		t.Errorf("Expected PreferredAgentTypes ['coder'], got %v", policy.PreferredAgentTypes)
	}
}

func TestSetPolicy(t *testing.T) {
	queue := NewQueue("/tmp/test")
	storyDispatcher := NewMockStoryDispatcher(queue)

	customPolicy := &AssignmentPolicy{
		MaxAgentsPerStory:   2,
		MaxStoriesPerAgent:  3,
		PreferredAgentTypes: []string{"architect", "coder"},
	}

	storyDispatcher.SetPolicy(customPolicy)

	if storyDispatcher.policy.MaxAgentsPerStory != 2 {
		t.Errorf("Expected MaxAgentsPerStory 2, got %d", storyDispatcher.policy.MaxAgentsPerStory)
	}

	if storyDispatcher.policy.MaxStoriesPerAgent != 3 {
		t.Errorf("Expected MaxStoriesPerAgent 3, got %d", storyDispatcher.policy.MaxStoriesPerAgent)
	}
}

func TestSelectBestAgent(t *testing.T) {
	queue := NewQueue("/tmp/test")
	storyDispatcher := NewMockStoryDispatcher(queue)

	story := &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		EstimatedPoints: 2,
	}

	// Test with no agents
	agent := storyDispatcher.selectBestAgent(story, []string{})
	if agent != "" {
		t.Errorf("Expected empty agent with no available agents, got %s", agent)
	}

	// Test with available agents
	availableAgents := []string{"agent1", "agent2", "agent3"}
	agent = storyDispatcher.selectBestAgent(story, availableAgents)
	if agent != "agent1" {
		t.Errorf("Expected agent1 (first available), got %s", agent)
	}
}

func TestIsAgentAvailable(t *testing.T) {
	queue := NewQueue("/tmp/test")
	storyDispatcher := NewMockStoryDispatcher(queue)

	agentID := "test-agent"

	// Agent should be available initially
	if !storyDispatcher.isAgentAvailable(agentID) {
		t.Error("Agent should be available initially")
	}

	// Add an active assignment
	assignment := &Assignment{
		StoryID:    "001",
		AgentID:    agentID,
		AssignedAt: time.Now(),
		Status:     "dispatched",
	}
	storyDispatcher.activeAssignments[agentID] = assignment

	// Agent should not be available now
	if storyDispatcher.isAgentAvailable(agentID) {
		t.Error("Agent should not be available with active assignment")
	}
}

func TestRemoveAgent(t *testing.T) {
	queue := NewQueue("/tmp/test")
	storyDispatcher := NewMockStoryDispatcher(queue)

	agents := []string{"agent1", "agent2", "agent3"}

	// Remove middle agent
	result := storyDispatcher.removeAgent(agents, "agent2")

	if len(result) != 2 {
		t.Errorf("Expected 2 agents after removal, got %d", len(result))
	}

	expected := []string{"agent1", "agent3"}
	for i, agent := range result {
		if agent != expected[i] {
			t.Errorf("Expected agent %s at index %d, got %s", expected[i], i, agent)
		}
	}

	// Remove non-existent agent
	result = storyDispatcher.removeAgent(agents, "agent4")
	if len(result) != 3 {
		t.Errorf("Expected 3 agents when removing non-existent agent, got %d", len(result))
	}
}

func TestDispatchStoryToAgent(t *testing.T) {
	// Create queue with a test story
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Test Story",
		Status:          StatusPending,
		FilePath:        "/tmp/test/001.md",
		EstimatedPoints: 2,
		DependsOn:       []string{},
	}

	storyDispatcher := NewMockStoryDispatcher(queue)

	ctx := context.Background()
	story := queue.stories["001"]
	agentID := "test-agent"

	// Test successful dispatch
	assignment, err := storyDispatcher.dispatchStoryToAgent(ctx, story, agentID)
	if err != nil {
		t.Fatalf("Failed to dispatch story: %v", err)
	}

	if assignment.StoryID != "001" {
		t.Errorf("Expected assignment story ID '001', got '%s'", assignment.StoryID)
	}

	if assignment.AgentID != agentID {
		t.Errorf("Expected assignment agent ID '%s', got '%s'", agentID, assignment.AgentID)
	}

	if assignment.Status != "dispatched" {
		t.Errorf("Expected assignment status 'dispatched', got '%s'", assignment.Status)
	}

	// Verify story status changed to in_progress
	updatedStory, exists := queue.GetStory("001")
	if !exists {
		t.Fatal("Story not found after dispatch")
	}

	if updatedStory.Status != StatusInProgress {
		t.Errorf("Expected story status in_progress, got %s", updatedStory.Status)
	}

	if updatedStory.AssignedAgent != agentID {
		t.Errorf("Expected assigned agent '%s', got '%s'", agentID, updatedStory.AssignedAgent)
	}

	// Verify assignment tracking
	trackedAssignment, exists := storyDispatcher.activeAssignments[agentID]
	if !exists {
		t.Error("Assignment not tracked in activeAssignments")
	} else if trackedAssignment.StoryID != "001" {
		t.Errorf("Expected tracked story ID '001', got '%s'", trackedAssignment.StoryID)
	}

	assignedAgent, exists := storyDispatcher.storyAssignments["001"]
	if !exists {
		t.Error("Story assignment not tracked")
	} else if assignedAgent != agentID {
		t.Errorf("Expected assigned agent '%s', got '%s'", agentID, assignedAgent)
	}
}

func TestHandleResult(t *testing.T) {
	// Create queue with a story in progress
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:            "001",
		Title:         "Test Story",
		Status:        StatusInProgress,
		AssignedAgent: "test-agent",
	}

	// Create a mock story dispatcher (can't create real dispatcher without full config)
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Set up active assignment
	agentID := "test-agent"
	assignment := &Assignment{
		StoryID:    "001",
		AgentID:    agentID,
		AssignedAt: time.Now(),
		Status:     "in_progress",
	}
	storyDispatcher.activeAssignments[agentID] = assignment
	storyDispatcher.storyAssignments["001"] = agentID

	// Create RESULT message
	resultMsg := proto.NewAgentMsg(
		proto.MsgTypeRESULT,
		agentID,
		"architect",
	)
	resultMsg.Payload["story_id"] = "001"
	resultMsg.Payload["status"] = "completed"

	ctx := context.Background()
	err := storyDispatcher.HandleResult(ctx, resultMsg)
	if err != nil {
		t.Fatalf("Failed to handle result: %v", err)
	}

	// Verify story status changed to waiting_review
	story, exists := queue.GetStory("001")
	if !exists {
		t.Fatal("Story not found after handling result")
	}

	if story.Status != StatusWaitingReview {
		t.Errorf("Expected story status waiting_review, got %s", story.Status)
	}

	// Verify assignment tracking was cleaned up
	_, exists = storyDispatcher.activeAssignments[agentID]
	if exists {
		t.Error("Assignment should be removed from activeAssignments")
	}

	_, exists = storyDispatcher.storyAssignments["001"]
	if exists {
		t.Error("Story assignment should be removed")
	}
}

func TestHandleError(t *testing.T) {
	// Create queue with a story in progress
	queue := NewQueue("/tmp/test")
	queue.stories["001"] = &QueuedStory{
		ID:            "001",
		Title:         "Test Story",
		Status:        StatusInProgress,
		AssignedAgent: "test-agent",
	}

	// Create a mock story dispatcher (can't create real dispatcher without full config)
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Set up active assignment
	agentID := "test-agent"
	assignment := &Assignment{
		StoryID:    "001",
		AgentID:    agentID,
		AssignedAt: time.Now(),
		Status:     "in_progress",
	}
	storyDispatcher.activeAssignments[agentID] = assignment
	storyDispatcher.storyAssignments["001"] = agentID

	// Create ERROR message
	errorMsg := proto.NewAgentMsg(
		proto.MsgTypeERROR,
		agentID,
		"architect",
	)
	errorMsg.Payload["story_id"] = "001"
	errorMsg.Payload["error"] = "Failed to implement story"

	ctx := context.Background()
	err := storyDispatcher.HandleError(ctx, errorMsg)
	if err != nil {
		t.Fatalf("Failed to handle error: %v", err)
	}

	// Verify story status changed to blocked
	story, exists := queue.GetStory("001")
	if !exists {
		t.Fatal("Story not found after handling error")
	}

	if story.Status != StatusBlocked {
		t.Errorf("Expected story status blocked, got %s", story.Status)
	}

	// Verify assignment tracking was cleaned up
	_, exists = storyDispatcher.activeAssignments[agentID]
	if exists {
		t.Error("Assignment should be removed from activeAssignments")
	}

	_, exists = storyDispatcher.storyAssignments["001"]
	if exists {
		t.Error("Story assignment should be removed")
	}
}

func TestGetAssignmentStatus(t *testing.T) {
	queue := NewQueue("/tmp/test")
	storyDispatcher := NewMockStoryDispatcher(queue)

	// Add some active assignments
	assignment1 := &Assignment{
		StoryID:    "001",
		AgentID:    "agent1",
		AssignedAt: time.Now(),
		Status:     "dispatched",
	}
	assignment2 := &Assignment{
		StoryID:    "002",
		AgentID:    "agent2",
		AssignedAt: time.Now(),
		Status:     "in_progress",
	}

	storyDispatcher.activeAssignments["agent1"] = assignment1
	storyDispatcher.activeAssignments["agent2"] = assignment2

	status := storyDispatcher.GetAssignmentStatus()

	if status.ActiveAssignments != 2 {
		t.Errorf("Expected 2 active assignments, got %d", status.ActiveAssignments)
	}

	if len(status.Assignments) != 2 {
		t.Errorf("Expected 2 assignments in status, got %d", len(status.Assignments))
	}

	// Verify policy is included
	if status.Policy.MaxAgentsPerStory != 1 {
		t.Errorf("Expected policy MaxAgentsPerStory 1, got %d", status.Policy.MaxAgentsPerStory)
	}
}

func TestDispatchReadyStoriesIntegration(t *testing.T) {
	// Create queue with ready stories
	queue := NewQueue("/tmp/test")

	// Add ready stories
	queue.stories["001"] = &QueuedStory{
		ID:              "001",
		Title:           "Ready Story 1",
		Status:          StatusPending,
		EstimatedPoints: 1,
		DependsOn:       []string{},
	}
	queue.stories["002"] = &QueuedStory{
		ID:              "002",
		Title:           "Ready Story 2",
		Status:          StatusPending,
		EstimatedPoints: 2,
		DependsOn:       []string{},
	}

	storyDispatcher := NewMockStoryDispatcher(queue)

	ctx := context.Background()
	result, err := storyDispatcher.DispatchReadyStories(ctx)
	if err != nil {
		t.Fatalf("Failed to dispatch ready stories: %v", err)
	}

	// Should have dispatched both stories
	if result.StoriesDispatched != 2 {
		t.Errorf("Expected 2 stories dispatched, got %d", result.StoriesDispatched)
	}

	if len(result.Assignments) != 2 {
		t.Errorf("Expected 2 assignments, got %d", len(result.Assignments))
	}

	// Verify stories are marked as in progress
	story1, _ := queue.GetStory("001")
	if story1.Status != StatusInProgress {
		t.Errorf("Expected story 001 status in_progress, got %s", story1.Status)
	}

	story2, _ := queue.GetStory("002")
	if story2.Status != StatusInProgress {
		t.Errorf("Expected story 002 status in_progress, got %s", story2.Status)
	}

	// Verify assignments are tracked
	if len(storyDispatcher.activeAssignments) != 2 {
		t.Errorf("Expected 2 active assignments, got %d", len(storyDispatcher.activeAssignments))
	}
}
