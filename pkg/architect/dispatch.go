package architect

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"time"

	"orchestrator/pkg/dispatch"
	"orchestrator/pkg/proto"
)

// AssignmentPolicy defines how stories are assigned to agents
type AssignmentPolicy struct {
	MaxAgentsPerStory   int      `json:"max_agents_per_story"`  // Usually 1
	MaxStoriesPerAgent  int      `json:"max_stories_per_agent"` // Concurrent limit
	PreferredAgentTypes []string `json:"preferred_agent_types"` // e.g., ["coder"]
}

// DefaultAssignmentPolicy returns a sensible default policy
func DefaultAssignmentPolicy() *AssignmentPolicy {
	return &AssignmentPolicy{
		MaxAgentsPerStory:   1,
		MaxStoriesPerAgent:  1, // One story at a time per agent
		PreferredAgentTypes: []string{"coder"},
	}
}

// StoryDispatcher handles dispatching stories to available agents
type StoryDispatcher struct {
	queue      *Queue
	dispatcher *dispatch.Dispatcher // Optional - for real dispatching
	policy     *AssignmentPolicy

	// Track active assignments
	activeAssignments map[string]*Assignment // agentID -> Assignment
	storyAssignments  map[string]string      // storyID -> agentID

	// Mock mode flag
	mockMode bool
}

// Assignment represents an active story assignment
type Assignment struct {
	StoryID    string    `json:"story_id"`
	AgentID    string    `json:"agent_id"`
	AssignedAt time.Time `json:"assigned_at"`
	Status     string    `json:"status"` // "dispatched", "acknowledged", "in_progress"
}

// NewStoryDispatcher creates a new story dispatcher
func NewStoryDispatcher(queue *Queue, dispatcher *dispatch.Dispatcher) *StoryDispatcher {
	return &StoryDispatcher{
		queue:             queue,
		dispatcher:        dispatcher,
		policy:            DefaultAssignmentPolicy(),
		activeAssignments: make(map[string]*Assignment),
		storyAssignments:  make(map[string]string),
		mockMode:          dispatcher == nil, // Use mock mode if no dispatcher provided
	}
}

// NewMockStoryDispatcher creates a story dispatcher in mock mode for testing
func NewMockStoryDispatcher(queue *Queue) *StoryDispatcher {
	return &StoryDispatcher{
		queue:             queue,
		dispatcher:        nil,
		policy:            DefaultAssignmentPolicy(),
		activeAssignments: make(map[string]*Assignment),
		storyAssignments:  make(map[string]string),
		mockMode:          true,
	}
}

// SetPolicy updates the assignment policy
func (sd *StoryDispatcher) SetPolicy(policy *AssignmentPolicy) {
	sd.policy = policy
}

// DispatchReadyStories finds ready stories and sends them to the dispatcher
func (sd *StoryDispatcher) DispatchReadyStories(ctx context.Context) (*DispatchResult, error) {
	result := &DispatchResult{
		StoriesDispatched: 0,
		Assignments:       []Assignment{},
		Errors:            []string{},
	}

	// Get ready stories from queue
	readyStories := sd.queue.GetReadyStories()
	if len(readyStories) == 0 {
		return result, nil
	}

	// Dispatch each ready story to the dispatcher (let dispatcher handle agent assignment)
	for _, story := range readyStories {
		// Read story content from file
		storyContent, err := sd.readStoryContent(story.FilePath)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("Failed to read story %s content: %v", story.ID, err))
			continue
		}

		// Create TASK message for this story
		// For now, send to the first available coding agent
		// TODO: Implement proper agent selection based on workload/capabilities
		taskMsg := proto.NewAgentMsg(proto.MsgTypeTASK, "architect", "claude_sonnet4:001")
		taskMsg.SetPayload("story_id", story.ID)
		taskMsg.SetPayload("content", storyContent)
		taskMsg.SetPayload("title", story.Title)
		taskMsg.SetMetadata("story_path", story.FilePath)
		taskMsg.SetMetadata("estimated_points", strconv.Itoa(story.EstimatedPoints))

		if sd.mockMode || sd.dispatcher == nil {
			// Mock mode - just simulate dispatch
			fmt.Printf("📨 Mock dispatch: would send TASK message to dispatcher for story %s\n", story.ID)

			// Create fake assignment for tracking
			assignment := Assignment{
				StoryID:    story.ID,
				AgentID:    fmt.Sprintf("claude_sonnet4:%03d", len(result.Assignments)+1),
				AssignedAt: time.Now(),
				Status:     "dispatched",
			}

			result.StoriesDispatched++
			result.Assignments = append(result.Assignments, assignment)

			// Mark story as in progress
			sd.queue.MarkInProgress(story.ID, assignment.AgentID)
		} else {
			// Live mode - send to real dispatcher (let it choose agent)
			err := sd.dispatcher.DispatchMessage(taskMsg)
			if err != nil {
				result.Errors = append(result.Errors, fmt.Sprintf("Failed to dispatch story %s: %v", story.ID, err))
				continue
			}

			// Create assignment record (dispatcher will fill in actual agent)
			assignment := Assignment{
				StoryID:    story.ID,
				AgentID:    "dispatcher-assigned", // Will be updated when agent responds
				AssignedAt: time.Now(),
				Status:     "dispatched",
			}

			result.StoriesDispatched++
			result.Assignments = append(result.Assignments, assignment)

			// Mark story as in progress with placeholder agent
			sd.queue.MarkInProgress(story.ID, "pending-assignment")
			fmt.Printf("✅ Dispatched story %s (%s) to dispatcher\n", story.ID, story.Title)
		}
	}

	return result, nil
}

// readStoryContent reads the content of a story file
func (sd *StoryDispatcher) readStoryContent(filePath string) (string, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}
	return string(content), nil
}

// dispatchStoryToAgent sends a TASK message to the specified agent
func (sd *StoryDispatcher) dispatchStoryToAgent(ctx context.Context, story *QueuedStory, agentID string) (*Assignment, error) {
	if !sd.mockMode {
		// Real dispatching mode
		// Create task payload
		taskPayload := map[string]interface{}{
			"story_id":         story.ID,
			"title":            story.Title,
			"file_path":        story.FilePath,
			"estimated_points": story.EstimatedPoints,
			"depends_on":       story.DependsOn,
			"task_type":        "implement_story",
		}

		// Create TASK message
		taskMsg := proto.NewAgentMsg(
			proto.MsgTypeTASK,
			"architect", // from
			agentID,     // to
		)

		// Set payload
		for key, value := range taskPayload {
			taskMsg.Payload[key] = value
		}

		// Add metadata
		taskMsg.SetMetadata("story_id", story.ID)
		taskMsg.SetMetadata("dispatch_time", time.Now().UTC().Format(time.RFC3339))

		// Send message via dispatcher
		err := sd.dispatcher.DispatchMessage(taskMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to send TASK message: %w", err)
		}
	} else {
		// Mock mode - just simulate the dispatch
		fmt.Printf("📨 Mock dispatch: would send TASK message to agent %s for story %s\n", agentID, story.ID)
	}

	// Mark story as in progress in queue
	err := sd.queue.MarkInProgress(story.ID, agentID)
	if err != nil {
		return nil, fmt.Errorf("failed to mark story as in progress: %w", err)
	}

	// Create assignment record
	assignment := &Assignment{
		StoryID:    story.ID,
		AgentID:    agentID,
		AssignedAt: time.Now().UTC(),
		Status:     "dispatched",
	}

	// Track assignment
	sd.activeAssignments[agentID] = assignment
	sd.storyAssignments[story.ID] = agentID

	return assignment, nil
}

// selectBestAgent chooses the best available agent for a story
func (sd *StoryDispatcher) selectBestAgent(story *QueuedStory, availableAgents []string) string {
	// Simple policy: return first available agent
	// In the future, this could consider:
	// - Agent specialization/skills
	// - Agent workload
	// - Story complexity
	// - Previous performance

	if len(availableAgents) == 0 {
		return ""
	}

	// For now, just return the first available agent
	return availableAgents[0]
}

// getAvailableAgents returns a list of agent IDs that can accept new assignments
func (sd *StoryDispatcher) getAvailableAgents() ([]string, error) {
	if sd.dispatcher == nil {
		// Mock mode - return fake agents for testing
		return []string{"claude_sonnet4:001", "claude_sonnet4:002", "claude_sonnet4:003"}, nil
	}

	// In live mode, get registered agents from the dispatcher
	stats := sd.dispatcher.GetStats()
	if agentCount, ok := stats["agents"].(int); ok && agentCount > 0 {
		// We have real agents registered - let dispatcher handle assignment
		// Return a generic "dispatcher-managed" agent list
		return []string{"dispatcher-managed"}, nil
	}

	return []string{}, fmt.Errorf("no agents registered with dispatcher")
}

// isAgentAvailable checks if an agent can accept new assignments
func (sd *StoryDispatcher) isAgentAvailable(agentID string) bool {
	// Check if agent has an active assignment
	assignment, hasAssignment := sd.activeAssignments[agentID]
	if hasAssignment {
		// Agent is busy with another story
		fmt.Printf("Agent %s busy with story %s\n", agentID, assignment.StoryID)
		return false
	}

	// Agent is available
	return true
}

// removeAgent removes an agent from the available list
func (sd *StoryDispatcher) removeAgent(agents []string, agentID string) []string {
	var result []string
	for _, agent := range agents {
		if agent != agentID {
			result = append(result, agent)
		}
	}
	return result
}

// HandleResult processes a RESULT message from an agent
func (sd *StoryDispatcher) HandleResult(ctx context.Context, msg *proto.AgentMsg) error {
	agentID := msg.FromAgent

	// Find the assignment for this agent
	assignment, exists := sd.activeAssignments[agentID]
	if !exists {
		return fmt.Errorf("no active assignment found for agent %s", agentID)
	}

	storyID := assignment.StoryID

	// Mark story as waiting for review in queue
	err := sd.queue.MarkWaitingReview(storyID)
	if err != nil {
		return fmt.Errorf("failed to mark story %s as waiting review: %w", storyID, err)
	}

	// Remove assignment tracking
	delete(sd.activeAssignments, agentID)
	delete(sd.storyAssignments, storyID)

	fmt.Printf("✅ Received result for story %s from agent %s, marked for review\n", storyID, agentID)
	return nil
}

// HandleError processes an ERROR message from an agent
func (sd *StoryDispatcher) HandleError(ctx context.Context, msg *proto.AgentMsg) error {
	agentID := msg.FromAgent

	// Find the assignment for this agent
	assignment, exists := sd.activeAssignments[agentID]
	if !exists {
		return fmt.Errorf("no active assignment found for agent %s", agentID)
	}

	storyID := assignment.StoryID

	// Mark story as blocked in queue
	err := sd.queue.MarkBlocked(storyID)
	if err != nil {
		return fmt.Errorf("failed to mark story %s as blocked: %w", storyID, err)
	}

	// Remove assignment tracking
	delete(sd.activeAssignments, agentID)
	delete(sd.storyAssignments, storyID)

	fmt.Printf("❌ Received error for story %s from agent %s, marked as blocked\n", storyID, agentID)
	return nil
}

// GetAssignmentStatus returns current assignment status
func (sd *StoryDispatcher) GetAssignmentStatus() *AssignmentStatus {
	status := &AssignmentStatus{
		ActiveAssignments: len(sd.activeAssignments),
		Assignments:       make([]Assignment, 0, len(sd.activeAssignments)),
		Policy:            *sd.policy,
	}

	for _, assignment := range sd.activeAssignments {
		status.Assignments = append(status.Assignments, *assignment)
	}

	return status
}

// DispatchResult represents the result of a dispatch operation
type DispatchResult struct {
	StoriesDispatched int          `json:"stories_dispatched"`
	Assignments       []Assignment `json:"assignments"`
	Errors            []string     `json:"errors"`
}

// AssignmentStatus represents the current state of assignments
type AssignmentStatus struct {
	ActiveAssignments int              `json:"active_assignments"`
	Assignments       []Assignment     `json:"assignments"`
	Policy            AssignmentPolicy `json:"policy"`
}
