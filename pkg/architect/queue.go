package architect

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

// StoryStatus represents the status of a story (canonical source of truth).
type StoryStatus string

const (
	// StatusNew indicates a story was just created, not yet released to queue.
	StatusNew StoryStatus = "new"
	// StatusPending indicates a story is released to dispatcher queue, ready for assignment.
	StatusPending StoryStatus = "pending"
	// StatusAssigned indicates a story was picked up by coder, assignment created.
	StatusAssigned StoryStatus = "assigned"
	// StatusPlanning indicates a coder is planning the work.
	StatusPlanning StoryStatus = "planning"
	// StatusCoding indicates a coder is implementing the work.
	StatusCoding StoryStatus = "coding"
	// StatusDone indicates work is completed and merged.
	StatusDone StoryStatus = "done"
)

// QueuedStory embeds the unified Story type with architect-specific methods.
type QueuedStory struct {
	persistence.Story
}

// GetStatus returns the story status as StoryStatus enum.
func (s *QueuedStory) GetStatus() StoryStatus {
	return StoryStatus(s.Status)
}

// SetStatus sets the story status from StoryStatus enum.
func (s *QueuedStory) SetStatus(status StoryStatus) {
	s.Status = string(status)
}

// NewQueuedStory creates a new QueuedStory from a persistence.Story.
func NewQueuedStory(story *persistence.Story) *QueuedStory {
	return &QueuedStory{Story: *story}
}

// ToPersistenceStory converts to persistence.Story for database operations.
func (s *QueuedStory) ToPersistenceStory() *persistence.Story {
	return &s.Story
}

// Queue manages the architect's story queue with dependency resolution.
//
//nolint:govet // Simple management struct, logical grouping preferred
type Queue struct {
	mutex              sync.RWMutex // Protects all story operations
	stories            map[string]*QueuedStory
	readyStoryCh       chan<- string               // Channel to notify when stories become ready
	persistenceChannel chan<- *persistence.Request // Channel for database operations
}

// NewQueue creates a new queue manager with database persistence.
func NewQueue(persistenceChannel chan<- *persistence.Request) *Queue {
	return &Queue{
		stories:            make(map[string]*QueuedStory),
		persistenceChannel: persistenceChannel,
		// readyStoryCh will be set by SetReadyChannel.
	}
}

// SetPersistenceChannel sets the persistence channel for database operations.
func (q *Queue) SetPersistenceChannel(ch chan<- *persistence.Request) {
	q.persistenceChannel = ch
}

// SetReadyChannel sets the channel for ready story notifications.
func (q *Queue) SetReadyChannel(ch chan<- string) {
	q.readyStoryCh = ch
}

// AddStory adds a story directly to the in-memory queue.
// This should be used when stories are generated during normal operation.
func (q *Queue) AddStory(storyID, specID, title, content, storyType string, dependencies []string, estimatedPoints int) {
	now := time.Now()
	queuedStory := &QueuedStory{
		Story: persistence.Story{
			ID:              storyID,
			SpecID:          specID,
			Title:           title,
			Content:         content, // Story content from requirement description
			ApprovedPlan:    "",      // Plan will be set during approval
			Priority:        estimatedPoints,
			DependsOn:       dependencies,
			EstimatedPoints: estimatedPoints,
			AssignedAgent:   "",
			StartedAt:       nil,
			CompletedAt:     nil,
			LastUpdated:     now,
			CreatedAt:       now,
			StoryType:       storyType,
		},
	}
	queuedStory.SetStatus(StatusPending)

	q.stories[storyID] = queuedStory

	// Check if this story or others became ready
	q.checkAndNotifyReady()
}

// FlushToDatabase writes all in-memory stories and dependencies to the database for persistence.
// This uses the new persistence functions and ensures proper ordering (stories first, then dependencies).
func (q *Queue) FlushToDatabase() {
	if q.persistenceChannel == nil {
		return
	}

	// Phase 1: Persist all stories first (to satisfy foreign key constraints)
	for _, queuedStory := range q.stories {
		// Convert queue status to database status
		var dbStatus string
		switch queuedStory.GetStatus() {
		case StatusPending:
			dbStatus = persistence.StatusNew
		case StatusAssigned:
			dbStatus = persistence.StatusCoding
		case StatusDone:
			dbStatus = persistence.StatusDone
		default:
			dbStatus = persistence.StatusNew
		}

		// Convert QueuedStory to persistence.Story with complete data
		dbStory := &persistence.Story{
			ID:            queuedStory.ID,
			SpecID:        queuedStory.SpecID,
			Title:         queuedStory.Title,
			Content:       queuedStory.Content,      // Now includes story content
			ApprovedPlan:  queuedStory.ApprovedPlan, // Now includes approved plan
			Status:        dbStatus,
			Priority:      queuedStory.Priority,
			CreatedAt:     queuedStory.LastUpdated,
			StartedAt:     queuedStory.StartedAt,
			CompletedAt:   queuedStory.CompletedAt,
			AssignedAgent: queuedStory.AssignedAgent,
			StoryType:     queuedStory.StoryType,
			TokensUsed:    0,   // Metrics data added during completion
			CostUSD:       0.0, // Metrics data added during completion
		}

		persistence.PersistStory(dbStory, q.persistenceChannel)
	}

	// Phase 2: Persist all dependencies (now that stories exist in database)
	for _, queuedStory := range q.stories {
		for _, dependsOnID := range queuedStory.DependsOn {
			persistence.PersistDependency(queuedStory.ID, dependsOnID, q.persistenceChannel)
		}
	}
}

// LoadFromDatabase has been removed - the queue is canonical and never loads from database.
// The database is purely a persistence log for external monitoring and debugging.

// Database loading methods have been removed - the queue is canonical.

// NextReadyStory returns the next story that's ready to be worked on.
func (q *Queue) NextReadyStory() *QueuedStory {
	ready := q.GetReadyStories()
	if len(ready) == 0 {
		return nil
	}

	// Sort by priority (higher first), then by estimated points (smaller first), then by ID for deterministic ordering.
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority == ready[j].Priority {
			if ready[i].EstimatedPoints == ready[j].EstimatedPoints {
				return ready[i].ID < ready[j].ID
			}
			return ready[i].EstimatedPoints < ready[j].EstimatedPoints
		}
		return ready[i].Priority > ready[j].Priority // Higher priority first
	})

	return ready[0]
}

// GetReadyStories returns all stories that are ready to be worked on.
func (q *Queue) GetReadyStories() []*QueuedStory {
	var ready []*QueuedStory

	for _, story := range q.stories {
		if story.GetStatus() != StatusPending {
			continue
		}

		// Check if all dependencies are completed.
		if q.areDependenciesMet(story) {
			ready = append(ready, story)
		}
	}

	return ready
}

// AllStoriesCompleted checks if all stories in the queue are completed.
func (q *Queue) AllStoriesCompleted() bool {
	for _, story := range q.stories {
		if story.GetStatus() != StatusDone {
			return false
		}
	}
	return true
}

// areDependenciesMet checks if all dependencies for a story are completed.
func (q *Queue) areDependenciesMet(story *QueuedStory) bool {
	for _, depID := range story.DependsOn {
		dep, exists := q.stories[depID]
		if !exists {
			// Dependency doesn't exist - consider it as not met.
			return false
		}
		if dep.GetStatus() != StatusDone {
			return false
		}
	}
	return true
}

// checkAndNotifyReady checks for stories that became ready and notifies via channel.
func (q *Queue) checkAndNotifyReady() {
	if q.readyStoryCh == nil {
		return // Channel not set, skip notifications
	}

	for _, story := range q.stories {
		if story.GetStatus() == StatusPending && q.areDependenciesMet(story) {
			// Try to notify (non-blocking).
			select {
			case q.readyStoryCh <- story.ID:
				logx.Infof("queue: notified that story %s is ready", story.ID)
			default:
				// Channel full, that's OK - the dispatcher will check again.
			}
		}
	}
}

// RequeueStory resets a story to pending status and clears the approved plan for fresh start.
// This should be used when a coder errors out and a new coder needs to start from scratch.
func (q *Queue) RequeueStory(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	// Clear assignment, approved plan, and reset to pending
	story.SetStatus(StatusPending)
	story.AssignedAgent = ""
	story.ApprovedPlan = "" // Clear approved plan for fresh start
	story.StartedAt = nil
	story.LastUpdated = time.Now().UTC()

	// TODO: Persist the requeue event to database for tracking

	return nil
}

// SetApprovedPlan sets the approved plan for a story.
func (q *Queue) SetApprovedPlan(storyID, approvedPlan string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	story.ApprovedPlan = approvedPlan
	story.LastUpdated = time.Now().UTC()

	return nil
}

// GetStory returns a story by ID.
func (q *Queue) GetStory(storyID string) (*QueuedStory, bool) {
	story, exists := q.stories[storyID]
	return story, exists
}

// GetAllStories returns all stories in the queue.
func (q *Queue) GetAllStories() []*QueuedStory {
	stories := make([]*QueuedStory, 0, len(q.stories))
	for _, story := range q.stories {
		stories = append(stories, story)
	}

	// Sort by ID for consistent ordering.
	sort.Slice(stories, func(i, j int) bool {
		return stories[i].ID < stories[j].ID
	})

	return stories
}

// GetStoriesByStatus returns all stories with a specific status.
func (q *Queue) GetStoriesByStatus(status StoryStatus) []*QueuedStory {
	var filtered []*QueuedStory
	for _, story := range q.stories {
		if story.GetStatus() == status {
			filtered = append(filtered, story)
		}
	}

	// Sort by ID for consistent ordering.
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ID < filtered[j].ID
	})

	return filtered
}

// DetectCycles detects circular dependencies in the story queue.
func (q *Queue) DetectCycles() [][]string {
	var cycles [][]string
	visited := make(map[string]bool)
	recStack := make(map[string]bool)

	for storyID := range q.stories {
		if !visited[storyID] {
			if cycle := q.detectCyclesDFS(storyID, visited, recStack, []string{}); len(cycle) > 0 {
				cycles = append(cycles, cycle)
			}
		}
	}

	return cycles
}

// detectCyclesDFS performs depth-first search to detect cycles.
func (q *Queue) detectCyclesDFS(storyID string, visited, recStack map[string]bool, path []string) []string {
	visited[storyID] = true
	recStack[storyID] = true
	path = append(path, storyID)

	story, exists := q.stories[storyID]
	if !exists {
		return nil
	}

	for _, depID := range story.DependsOn {
		if !visited[depID] {
			if cycle := q.detectCyclesDFS(depID, visited, recStack, path); len(cycle) > 0 {
				return cycle
			}
		} else if recStack[depID] {
			// Found a cycle.
			cycleStart := -1
			for i, id := range path {
				if id == depID {
					cycleStart = i
					break
				}
			}
			if cycleStart >= 0 {
				return append(path[cycleStart:], depID)
			}
		}
	}

	recStack[storyID] = false
	return nil
}

// ToJSON serializes the queue to JSON.
func (q *Queue) ToJSON() ([]byte, error) {
	stories := q.GetAllStories()
	data, err := json.MarshalIndent(stories, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("failed to marshal queue to JSON: %w", err)
	}
	return data, nil
}

// FromJSON deserializes the queue from JSON.
func (q *Queue) FromJSON(data []byte) error {
	var stories []*QueuedStory
	if err := json.Unmarshal(data, &stories); err != nil {
		return fmt.Errorf("failed to unmarshal queue JSON: %w", err)
	}

	q.stories = make(map[string]*QueuedStory)
	for _, story := range stories {
		q.stories[story.ID] = story
	}

	return nil
}

// GetQueueSummary returns a summary of the queue state.
func (q *Queue) GetQueueSummary() map[string]any {
	summary := make(map[string]any)

	statusCounts := make(map[StoryStatus]int)
	totalPoints := 0
	completedPoints := 0

	for _, story := range q.stories {
		statusCounts[story.GetStatus()]++
		totalPoints += story.EstimatedPoints
		if story.GetStatus() == StatusDone {
			completedPoints += story.EstimatedPoints
		}
	}

	summary["total_stories"] = len(q.stories)
	summary["status_counts"] = statusCounts
	summary["total_points"] = totalPoints
	summary["completed_points"] = completedPoints
	summary["ready_stories"] = len(q.GetReadyStories())

	cycles := q.DetectCycles()
	summary["has_cycles"] = len(cycles) > 0
	summary["cycles"] = cycles

	return summary
}

// UpdateStoryStatus updates a story's status with mutex protection and persistence.
func (q *Queue) UpdateStoryStatus(storyID string, status StoryStatus) error {
	// Lock for in-memory update
	q.mutex.Lock()
	story, exists := q.stories[storyID]
	if !exists {
		q.mutex.Unlock()
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	story.SetStatus(status)
	story.LastUpdated = time.Now().UTC()
	q.mutex.Unlock() // Release before persistence

	// Persist to database (no mutex needed)
	if q.persistenceChannel != nil {
		persistence.PersistStory(story.ToPersistenceStory(), q.persistenceChannel)
	}

	return nil
}
