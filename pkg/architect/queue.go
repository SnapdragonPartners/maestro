package architect

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

// StoryStatus represents the status of a story in the queue.
type StoryStatus string

const (
	// StatusPending indicates a story is waiting to be started.
	StatusPending StoryStatus = "pending"
	// StatusInProgress indicates a story is being actively worked on.
	StatusInProgress StoryStatus = "in_progress"
	// StatusWaitingReview indicates a story is waiting for review.
	StatusWaitingReview StoryStatus = "waiting_review"
	// StatusCompleted indicates a story has been completed successfully.
	StatusCompleted StoryStatus = "completed"
	// StatusBlocked indicates a story is blocked and cannot proceed.
	StatusBlocked StoryStatus = "blocked"
	// StatusCancelled indicates a story has been cancelled.
	StatusCancelled StoryStatus = "cancelled"
	// StatusAwaitHumanFeedback indicates a story is waiting for human input.
	StatusAwaitHumanFeedback StoryStatus = "await_human_feedback"
)

// QueuedStory represents a story in the architect's queue.
//
//nolint:govet // Large complex struct, logical grouping preferred
type QueuedStory struct {
	ID              string      `json:"id"`
	Title           string      `json:"title"`
	FilePath        string      `json:"file_path"`
	Status          StoryStatus `json:"status"`
	DependsOn       []string    `json:"depends_on"`
	EstimatedPoints int         `json:"estimated_points"`
	AssignedAgent   string      `json:"assigned_agent,omitempty"`
	StartedAt       *time.Time  `json:"started_at,omitempty"`
	CompletedAt     *time.Time  `json:"completed_at,omitempty"`
	LastUpdated     time.Time   `json:"last_updated"`
}

// Queue manages the architect's story queue with dependency resolution.
//
//nolint:govet // Simple management struct, logical grouping preferred
type Queue struct {
	stories            map[string]*QueuedStory
	storiesDir         string
	readyStoryCh       chan<- string               // Channel to notify when stories become ready
	persistenceChannel chan<- *persistence.Request // Channel for database operations
}

// NewQueue creates a new queue manager.
func NewQueue(storiesDir string) *Queue {
	return &Queue{
		stories:    make(map[string]*QueuedStory),
		storiesDir: storiesDir,
		// readyStoryCh will be set by SetReadyChannel.
		// persistenceChannel will be set by SetPersistenceChannel.
	}
}

// NewQueueWithPersistence creates a new queue manager with database persistence.
func NewQueueWithPersistence(storiesDir string, persistenceChannel chan<- *persistence.Request) *Queue {
	return &Queue{
		stories:            make(map[string]*QueuedStory),
		storiesDir:         storiesDir,
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

// LoadFromDatabase loads stories from the database.
// This is the only method for story loading - filesystem fallback removed to ensure single source of truth.
func (q *Queue) LoadFromDatabase() error {
	if q.persistenceChannel == nil {
		return fmt.Errorf("persistence channel not available - database storage is required for story loading")
	}

	// Request all stories from database
	responseCh := make(chan interface{}, 1)
	req := &persistence.Request{
		Operation: persistence.OpGetAllStories,
		Data:      nil,
		Response:  responseCh,
	}

	// Send request to persistence worker
	q.persistenceChannel <- req

	// Wait for response
	response := <-responseCh
	if response == nil {
		// No stories in database yet, start with empty queue
		return nil
	}

	// Handle error response
	if err, ok := response.(error); ok {
		return fmt.Errorf("failed to load stories from database: %w", err)
	}

	// Convert response to stories
	stories, ok := response.([]*persistence.Story)
	if !ok {
		return fmt.Errorf("unexpected response type from database: %T", response)
	}

	// Convert database stories to queue stories
	for _, dbStory := range stories {
		queueStory := q.convertDatabaseStoryToQueueStory(dbStory)
		q.stories[queueStory.ID] = queueStory
	}

	// After loading all stories, check for initially ready ones
	q.checkAndNotifyReady()

	return nil
}

// convertDatabaseStoryToQueueStory converts a database story to a queue story.
func (q *Queue) convertDatabaseStoryToQueueStory(dbStory *persistence.Story) *QueuedStory {
	// Convert database status to queue status
	var queueStatus StoryStatus
	switch dbStory.Status {
	case persistence.StatusNew:
		queueStatus = StatusPending
	case persistence.StatusPlanning:
		queueStatus = StatusInProgress
	case persistence.StatusCoding:
		queueStatus = StatusInProgress
	case persistence.StatusCommitted:
		queueStatus = StatusCompleted
	case persistence.StatusMerged:
		queueStatus = StatusCompleted
	case persistence.StatusError:
		queueStatus = StatusBlocked
	case persistence.StatusDuplicate:
		queueStatus = StatusCancelled
	default:
		queueStatus = StatusPending // Default fallback
	}

	// Get dependencies for this story
	dependencies := q.getStoryDependencies(dbStory.ID)

	return &QueuedStory{
		ID:              dbStory.ID,
		Title:           dbStory.Title,
		FilePath:        "", // No file path for database stories
		Status:          queueStatus,
		DependsOn:       dependencies,
		EstimatedPoints: dbStory.Priority,  // Use priority as estimated points
		AssignedAgent:   "",                // Not tracked in database yet
		StartedAt:       nil,               // Not tracked in database yet
		CompletedAt:     nil,               // Not tracked in database yet
		LastUpdated:     dbStory.CreatedAt, // Use CreatedAt since UpdatedAt doesn't exist
	}
}

// getStoryDependencies retrieves dependencies for a story from the database.
func (q *Queue) getStoryDependencies(storyID string) []string {
	if q.persistenceChannel == nil {
		return []string{}
	}

	// Request dependencies from database
	responseCh := make(chan interface{}, 1)
	req := &persistence.Request{
		Operation: persistence.OpGetStoryDependencies,
		Data:      storyID,
		Response:  responseCh,
	}

	q.persistenceChannel <- req

	// Wait for response
	response := <-responseCh
	if response == nil {
		return []string{}
	}

	// Handle error response
	if _, ok := response.(error); ok {
		return []string{} // Return empty on error
	}

	// Convert response to dependencies
	if deps, ok := response.([]string); ok {
		return deps
	}

	return []string{}
}

// NextReadyStory returns the next story that's ready to be worked on.
func (q *Queue) NextReadyStory() *QueuedStory {
	ready := q.GetReadyStories()
	if len(ready) == 0 {
		return nil
	}

	// Sort by estimated points (smaller first) then by ID for deterministic ordering.
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].EstimatedPoints == ready[j].EstimatedPoints {
			return ready[i].ID < ready[j].ID
		}
		return ready[i].EstimatedPoints < ready[j].EstimatedPoints
	})

	return ready[0]
}

// GetReadyStories returns all stories that are ready to be worked on.
func (q *Queue) GetReadyStories() []*QueuedStory {
	var ready []*QueuedStory

	for _, story := range q.stories {
		if story.Status != StatusPending {
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
		if story.Status != StatusCompleted && story.Status != StatusCancelled {
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
		if dep.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// MarkInProgress marks a story as in progress and assigns it to an agent.
func (q *Queue) MarkInProgress(storyID, agentID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	if story.Status != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.Status)
	}

	now := time.Now().UTC()
	story.Status = StatusInProgress
	story.AssignedAgent = agentID
	story.StartedAt = &now
	story.LastUpdated = now

	// Update database status to coding (fire-and-forget)
	if q.persistenceChannel != nil {
		updateReq := &persistence.UpdateStoryStatusRequest{
			StoryID:   storyID,
			Status:    persistence.StatusCoding,
			Timestamp: now,
		}
		q.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpdateStoryStatus,
			Data:      updateReq,
			Response:  nil, // Fire-and-forget
		}
	}

	return nil
}

// MarkWaitingReview marks a story as waiting for review.
func (q *Queue) MarkWaitingReview(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	if story.Status != StatusInProgress {
		return fmt.Errorf("story %s is not in progress (current: %s)", storyID, story.Status)
	}

	now := time.Now().UTC()
	story.Status = StatusWaitingReview
	story.LastUpdated = now

	// Update database status remains as coding since the story is still being worked on
	if q.persistenceChannel != nil {
		updateReq := &persistence.UpdateStoryStatusRequest{
			StoryID:   storyID,
			Status:    persistence.StatusCoding,
			Timestamp: now,
		}
		q.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpdateStoryStatus,
			Data:      updateReq,
			Response:  nil, // Fire-and-forget
		}
	}

	return nil
}

// MarkCompleted marks a story as completed.
func (q *Queue) MarkCompleted(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	allowedStatuses := []StoryStatus{StatusInProgress, StatusWaitingReview}
	statusAllowed := false
	for _, status := range allowedStatuses {
		if story.Status == status {
			statusAllowed = true
			break
		}
	}

	if !statusAllowed {
		return fmt.Errorf("story %s is not in a completable status (current: %s)", storyID, story.Status)
	}

	now := time.Now().UTC()
	story.Status = StatusCompleted
	story.CompletedAt = &now
	story.LastUpdated = now

	// Update database status to committed (fire-and-forget)
	if q.persistenceChannel != nil {
		updateReq := &persistence.UpdateStoryStatusRequest{
			StoryID:   storyID,
			Status:    persistence.StatusCommitted,
			Timestamp: now,
		}
		q.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpdateStoryStatus,
			Data:      updateReq,
			Response:  nil, // Fire-and-forget
		}
	}

	// Check if any pending stories became ready due to this completion.
	q.checkAndNotifyReady()

	return nil
}

// checkAndNotifyReady checks for stories that became ready and notifies via channel.
func (q *Queue) checkAndNotifyReady() {
	if q.readyStoryCh == nil {
		return // Channel not set, skip notifications
	}

	for _, story := range q.stories {
		if story.Status == StatusPending && q.areDependenciesMet(story) {
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

// MarkBlocked marks a story as blocked.
func (q *Queue) MarkBlocked(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	story.Status = StatusBlocked
	story.LastUpdated = time.Now().UTC()

	return nil
}

// MarkAwaitHumanFeedback marks a story as awaiting human feedback/intervention.
func (q *Queue) MarkAwaitHumanFeedback(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	story.Status = StatusAwaitHumanFeedback
	story.LastUpdated = time.Now().UTC()

	return nil
}

// MarkPending resets a story to pending status for reassignment (used for requeuing).
func (q *Queue) MarkPending(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	// Clear assignment and reset to pending.
	story.Status = StatusPending
	story.AssignedAgent = ""
	story.StartedAt = nil
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
		if story.Status == status {
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
		statusCounts[story.Status]++
		totalPoints += story.EstimatedPoints
		if story.Status == StatusCompleted {
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
