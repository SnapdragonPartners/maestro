package architect

import (
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// StoryStatus represents the status of a story (canonical source of truth).
type StoryStatus string

const (
	// StatusNew indicates a story was just created, not yet released to queue.
	StatusNew StoryStatus = "new"
	// StatusPending indicates a story is released to dispatcher queue, ready for assignment.
	StatusPending StoryStatus = "pending"
	// StatusDispatched indicates a story was sent to work queue, waiting for coder pickup.
	StatusDispatched StoryStatus = "dispatched"
	// StatusPlanning indicates a coder picked up the story and is planning the work.
	StatusPlanning StoryStatus = "planning"
	// StatusCoding indicates a coder is implementing the work.
	StatusCoding StoryStatus = "coding"
	// StatusDone indicates work is completed and merged.
	StatusDone StoryStatus = "done"
	// StatusFailed indicates the story failed after exhausting retry limit.
	StatusFailed StoryStatus = "failed"
	// StatusOnHold indicates the story is paused but recoverable (e.g., blocked by a failure under recovery).
	StatusOnHold StoryStatus = "on_hold"
)

// ToDatabaseStatus converts StoryStatus to persistence package status string.
// This is the single source of truth for status mapping between queue and database.
func (s StoryStatus) ToDatabaseStatus() string {
	switch s {
	case StatusNew, StatusPending:
		return persistence.StatusNew
	case StatusDispatched:
		return persistence.StatusDispatched
	case StatusPlanning:
		return persistence.StatusPlanning
	case StatusCoding:
		return persistence.StatusCoding
	case StatusDone:
		return persistence.StatusDone
	case StatusFailed:
		return persistence.StatusFailed
	case StatusOnHold:
		return persistence.StatusOnHold
	default:
		return persistence.StatusNew
	}
}

// MaxStoryAttempts is the maximum number of times a story can be dispatched before
// the retry budget is exhausted. Deprecated: use MaxAttemptRetries with budget methods.
const MaxStoryAttempts = 3

// Per-class budget limits for failure recovery.
const (
	// MaxAttemptRetries is the maximum number of attempt-level retries (same as legacy MaxStoryAttempts).
	MaxAttemptRetries = 3
	// MaxStoryRewrites is the maximum number of times a story can be rewritten by the architect.
	MaxStoryRewrites = 2
	// MaxRepairAttempts is the maximum number of environment repair attempts per story.
	MaxRepairAttempts = 2
	// MaxHumanRoundTrips is the maximum number of human intervention round-trips per story.
	MaxHumanRoundTrips = 1
)

// Budget class constants for IncrementBudget/IsBudgetExhausted.
const (
	BudgetClassAttempt = "attempt"
	BudgetClassRewrite = "rewrite"
	BudgetClassRepair  = "repair"
	BudgetClassHuman   = "human"
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
// Returns an error if attempting to modify a terminal story's status.
func (s *QueuedStory) SetStatus(status StoryStatus) error {
	current := s.GetStatus()
	// Protect terminal stories from status changes
	if current == StatusDone {
		return fmt.Errorf("cannot change status of completed story %s from done to %s: completed stories are immutable", s.ID, status)
	}
	if current == StatusFailed {
		return fmt.Errorf("cannot change status of failed story %s from failed to %s: failed stories are terminal", s.ID, status)
	}
	s.Status = string(status)
	return nil
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
	dispatchSuppressed bool                        // When true, GetReadyStories returns empty (system repair in progress)
	suppressReason     string                      // Why dispatch is suppressed (for logging)
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
	_ = queuedStory.SetStatus(StatusPending) // New story, cannot fail

	q.mutex.Lock()
	q.stories[storyID] = queuedStory
	q.mutex.Unlock()

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
		// Convert QueuedStory to persistence.Story with complete data
		dbStory := &persistence.Story{
			ID:            queuedStory.ID,
			SpecID:        queuedStory.SpecID,
			Title:         queuedStory.Title,
			Content:       queuedStory.Content,      // Now includes story content
			ApprovedPlan:  queuedStory.ApprovedPlan, // Now includes approved plan
			Status:        queuedStory.GetStatus().ToDatabaseStatus(),
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

// SuppressDispatch prevents new story dispatch during system-level repair.
// Active coders continue but no new stories are assigned.
func (q *Queue) SuppressDispatch(reason string) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	q.dispatchSuppressed = true
	q.suppressReason = reason
}

// ResumeDispatch re-enables story dispatch after repair completes.
func (q *Queue) ResumeDispatch() {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	q.dispatchSuppressed = false
	q.suppressReason = ""
}

// IsDispatchSuppressed returns whether dispatch is currently suppressed and why.
func (q *Queue) IsDispatchSuppressed() (bool, string) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()
	return q.dispatchSuppressed, q.suppressReason
}

// GetReadyStories returns all stories that are ready to be worked on.
// Returns empty if dispatch is suppressed (system repair in progress).
func (q *Queue) GetReadyStories() []*QueuedStory {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	if q.dispatchSuppressed {
		return nil
	}

	var ready []*QueuedStory

	for _, story := range q.stories {
		if story.GetStatus() != StatusPending {
			continue
		}

		// Check if all dependencies are completed.
		if q.areDependenciesMetLocked(story) {
			ready = append(ready, story)
		}
	}

	return ready
}

// AllStoriesCompleted checks if all stories in the queue are completed.
func (q *Queue) AllStoriesCompleted() bool {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, story := range q.stories {
		if story.GetStatus() != StatusDone {
			return false
		}
	}
	return true
}

// AllStoriesTerminal checks if all stories in the queue are in a terminal state (done or failed).
// Used by the circuit breaker to detect "no more work to do" without implying success.
// This is distinct from AllStoriesCompleted which only checks for success (StatusDone).
func (q *Queue) AllStoriesTerminal() bool {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, story := range q.stories {
		status := story.GetStatus()
		if status != StatusDone && status != StatusFailed {
			return false
		}
	}
	return true
}

// AllNonMaintenanceStoriesCompleted checks if all non-maintenance stories are completed.
// Used to notify PM that spec work is done before maintenance stories are dispatched.
func (q *Queue) AllNonMaintenanceStoriesCompleted() bool {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, story := range q.stories {
		if story.IsMaintenance {
			continue
		}
		if story.GetStatus() != StatusDone {
			return false
		}
	}
	return true
}

// GetSpecTotalPoints returns the sum of EstimatedPoints for all stories in a spec.
// Used by the maintenance heuristic to decide if a spec is significant enough to trigger maintenance.
func (q *Queue) GetSpecTotalPoints(specID string) int {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	total := 0
	for _, story := range q.stories {
		if story.SpecID == specID && !story.IsMaintenance {
			total += story.EstimatedPoints
		}
	}
	return total
}

// CheckSpecComplete returns true if all stories for a spec are done.
// Used by maintenance tracking to detect when a spec's work is complete.
func (q *Queue) CheckSpecComplete(specID string) bool {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	foundStories := false
	for _, story := range q.stories {
		if story.SpecID == specID {
			foundStories = true
			if story.GetStatus() != StatusDone {
				return false
			}
		}
	}

	// If no stories found for this spec, it's not "complete" (it never started)
	return foundStories
}

// GetSpecStoryCount returns total and completed story counts for a spec.
// Useful for progress tracking and maintenance trigger decisions.
func (q *Queue) GetSpecStoryCount(specID string) (total, completed int) {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, story := range q.stories {
		if story.SpecID == specID {
			total++
			if story.GetStatus() == StatusDone {
				completed++
			}
		}
	}
	return
}

// GetUniqueSpecIDs returns all unique spec IDs that have stories in the queue.
func (q *Queue) GetUniqueSpecIDs() []string {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	specIDs := make(map[string]bool)
	for _, story := range q.stories {
		if story.SpecID != "" {
			specIDs[story.SpecID] = true
		}
	}

	result := make([]string, 0, len(specIDs))
	for specID := range specIDs {
		result = append(result, specID)
	}
	return result
}

// areDependenciesMetLocked checks if all dependencies for a story are completed.
// Must be called with mutex held (read or write).
func (q *Queue) areDependenciesMetLocked(story *QueuedStory) bool {
	for _, depID := range story.DependsOn {
		dep, exists := q.stories[depID]
		if !exists {
			return false
		}
		if dep.GetStatus() != StatusDone {
			return false
		}
	}
	return true
}

// checkAndNotifyReady checks for stories that became ready and notifies via channel.
// Must be called without holding the lock - it acquires its own read lock.
func (q *Queue) checkAndNotifyReady() {
	if q.readyStoryCh == nil {
		return // Channel not set, skip notifications
	}

	// Collect ready story IDs while holding the read lock
	q.mutex.RLock()
	var readyIDs []string
	for _, story := range q.stories {
		if story.GetStatus() == StatusPending && q.areDependenciesMetLocked(story) {
			readyIDs = append(readyIDs, story.ID)
		}
	}
	q.mutex.RUnlock()

	// Notify outside the lock to avoid blocking other operations
	for _, storyID := range readyIDs {
		select {
		case q.readyStoryCh <- storyID:
			logx.Infof("queue: notified that story %s is ready", storyID)
		default:
			// Channel full, that's OK - the dispatcher will check again.
		}
	}
}

// AddMaintenanceStory adds a maintenance story to the queue.
// Maintenance stories have no dependencies and are automatically set to pending status.
func (q *Queue) AddMaintenanceStory(storyID, specID, title, content string, express, isMaintenance bool) {
	now := time.Now()
	queuedStory := &QueuedStory{
		Story: persistence.Story{
			ID:            storyID,
			SpecID:        specID,
			Title:         title,
			Content:       content,
			Priority:      1, // Low priority - maintenance runs in background
			DependsOn:     nil,
			Express:       express,
			IsMaintenance: isMaintenance,
			AssignedAgent: "",
			StartedAt:     nil,
			CompletedAt:   nil,
			LastUpdated:   now,
			CreatedAt:     now,
			StoryType:     string(proto.StoryTypeMaintenance),
		},
	}
	_ = queuedStory.SetStatus(StatusPending) // New story, cannot fail

	q.mutex.Lock()
	q.stories[storyID] = queuedStory
	q.mutex.Unlock()

	// Check if this story is ready (no dependencies, so it should be)
	q.checkAndNotifyReady()
}

// RequeueStory resets a story to pending status and clears the approved plan for fresh start.
// This should be used when a coder errors out and a new coder needs to start from scratch.
func (q *Queue) RequeueStory(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	// Protect terminal stories from requeue
	if story.GetStatus() == StatusDone || story.GetStatus() == StatusFailed {
		return fmt.Errorf("cannot requeue terminal story %s (status=%s)", storyID, story.GetStatus())
	}

	// Clear assignment, approved plan, and reset to pending
	_ = story.SetStatus(StatusPending) // Already checked for terminal above
	story.AssignedAgent = ""
	story.ApprovedPlan = "" // Clear approved plan for fresh start
	story.StartedAt = nil
	story.LastUpdated = time.Now().UTC()

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
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	story, exists := q.stories[storyID]
	return story, exists
}

// GetAllStories returns all stories in the queue.
func (q *Queue) GetAllStories() []*QueuedStory {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

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
	q.mutex.RLock()
	defer q.mutex.RUnlock()

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

// RemoveCyclicEdges removes dependency edges that participate in cycles.
// For each cycle, it removes the last edge (back-edge) to break the cycle.
func (q *Queue) RemoveCyclicEdges(cycles [][]string) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for _, cycle := range cycles {
		if len(cycle) < 2 {
			continue
		}
		// Remove the back-edge: last node depends on first node in the cycle
		// cycle is [A, B, C, A] — remove C→A edge
		fromID := cycle[len(cycle)-2]
		toID := cycle[len(cycle)-1]

		story, exists := q.stories[fromID]
		if !exists {
			continue
		}

		// Remove toID from story's DependsOn
		filtered := make([]string, 0, len(story.DependsOn))
		for _, dep := range story.DependsOn {
			if dep != toID {
				filtered = append(filtered, dep)
			}
		}
		story.DependsOn = filtered
	}
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

	// Protect terminal stories from status changes
	if story.GetStatus() == StatusDone || story.GetStatus() == StatusFailed {
		q.mutex.Unlock()
		return fmt.Errorf("cannot update status of terminal story %s (status=%s)", storyID, story.GetStatus())
	}

	if err := story.SetStatus(status); err != nil {
		q.mutex.Unlock()
		return err
	}
	story.LastUpdated = time.Now().UTC()
	q.mutex.Unlock() // Release before persistence

	// Persist to database (no mutex needed)
	if q.persistenceChannel != nil {
		persistence.PersistStory(story.ToPersistenceStory(), q.persistenceChannel)
	}

	return nil
}

// GetDependencyOrderedStories returns stories in dependency order (topologically sorted).
// Stories with no dependencies come first, followed by their dependents.
func (q *Queue) GetDependencyOrderedStories() []*QueuedStory {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// Simple topological sort
	inDegree := make(map[string]int)
	adjacencyList := make(map[string][]string)
	allStories := make(map[string]*QueuedStory)

	// Initialize
	for storyID, story := range q.stories {
		inDegree[storyID] = 0
		allStories[storyID] = story
		adjacencyList[storyID] = []string{}
	}

	// Count dependencies (in-degree)
	for storyID, story := range q.stories {
		for _, depID := range story.DependsOn {
			if _, exists := q.stories[depID]; exists {
				inDegree[storyID]++
				adjacencyList[depID] = append(adjacencyList[depID], storyID)
			}
		}
	}

	// Topological sort using Kahn's algorithm
	var queue []string
	for storyID, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, storyID)
		}
	}

	var result []*QueuedStory
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		result = append(result, allStories[current])

		// Reduce in-degree of neighbors
		for _, neighbor := range adjacencyList[current] {
			inDegree[neighbor]--
			if inDegree[neighbor] == 0 {
				queue = append(queue, neighbor)
			}
		}
	}

	return result
}

// ClearAll removes all stories from the in-memory queue.
// Used when retrying story generation with different parameters.
func (q *Queue) ClearAll() {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	q.stories = make(map[string]*QueuedStory)
}

// ClearSpec removes only stories belonging to the given spec from the queue.
// Preserves stories from other specs (e.g., bootstrap stories).
func (q *Queue) ClearSpec(specID string) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	for id, story := range q.stories {
		if story.SpecID == specID {
			delete(q.stories, id)
		}
	}
}

// LoadStoriesFromDB loads the complete story graph from the database into the in-memory queue.
// This is used during resume to restore the architect's story state.
// All stories are loaded (including done ones) so dependency checks work correctly.
// Database status "new" is mapped to StatusPending so GetReadyStories will dispatch them.
// Done stories keep their status and won't be re-dispatched.
func (q *Queue) LoadStoriesFromDB(stories []*persistence.Story) int {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	// Clear existing stories before loading
	q.stories = make(map[string]*QueuedStory)

	for _, story := range stories {
		qs := NewQueuedStory(story)
		// Map "new" status from database to pending so stories get dispatched.
		// Database stores both StatusNew and StatusPending as "new" (see ToDatabaseStatus).
		// Done stories keep their status - they won't be re-dispatched.
		if qs.Status == persistence.StatusNew {
			_ = qs.SetStatus(StatusPending)
		}
		q.stories[story.ID] = qs
	}

	return len(q.stories)
}

// FindStoryByTitle finds a story by its title.
// Returns nil if no story with that title exists.
func (q *Queue) FindStoryByTitle(title string) *QueuedStory {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	for _, story := range q.stories {
		if story.Title == title {
			return story
		}
	}
	return nil
}

// AddHotfixStory adds a hotfix story to the queue.
// Hotfix stories are marked with IsHotfix=true for routing to the dedicated hotfix coder.
func (q *Queue) AddHotfixStory(story *QueuedStory) error {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	if story.ID == "" {
		return fmt.Errorf("story ID is required")
	}

	// Ensure hotfix flags are set
	story.IsHotfix = true

	// Set status to pending (ready for dispatch)
	if err := story.SetStatus(StatusPending); err != nil {
		return fmt.Errorf("cannot add hotfix story: %w", err)
	}

	// Set timestamps
	now := time.Now()
	story.CreatedAt = now
	story.LastUpdated = now

	q.stories[story.ID] = story

	// Notify that a story is ready (hotfix stories are always ready since deps are pre-validated)
	q.checkAndNotifyReadyLocked()

	return nil
}

// checkAndNotifyReadyLocked checks for ready stories and notifies the channel.
// Must be called with mutex held.
func (q *Queue) checkAndNotifyReadyLocked() {
	if q.readyStoryCh == nil {
		return
	}

	for storyID, story := range q.stories {
		if story.GetStatus() == StatusPending && q.areDependenciesSatisfiedLocked(storyID) {
			// Non-blocking send
			select {
			case q.readyStoryCh <- storyID:
			default:
				// Channel full or closed, story will be picked up later
			}
		}
	}
}

// IncrementBudget increments a per-class budget counter for a story.
// Returns the new count and the max for that budget class.
func (q *Queue) IncrementBudget(storyID, budgetClass string) (count, limit int, err error) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	story, exists := q.stories[storyID]
	if !exists {
		return 0, 0, fmt.Errorf("story %s not found", storyID)
	}

	switch budgetClass {
	case BudgetClassAttempt:
		story.AttemptRetryBudget++
		return story.AttemptRetryBudget, MaxAttemptRetries, nil
	case BudgetClassRewrite:
		story.RewriteBudget++
		return story.RewriteBudget, MaxStoryRewrites, nil
	case BudgetClassRepair:
		story.RepairBudget++
		return story.RepairBudget, MaxRepairAttempts, nil
	case BudgetClassHuman:
		story.HumanBudget++
		return story.HumanBudget, MaxHumanRoundTrips, nil
	default:
		return 0, 0, fmt.Errorf("unknown budget class: %s", budgetClass)
	}
}

// IsBudgetExhausted checks if a per-class budget is exhausted for a story.
func (q *Queue) IsBudgetExhausted(storyID, budgetClass string) bool {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	story, exists := q.stories[storyID]
	if !exists {
		return true // Story not found = cannot retry
	}

	switch budgetClass {
	case BudgetClassAttempt:
		return story.AttemptRetryBudget >= MaxAttemptRetries
	case BudgetClassRewrite:
		return story.RewriteBudget >= MaxStoryRewrites
	case BudgetClassRepair:
		return story.RepairBudget >= MaxRepairAttempts
	case BudgetClassHuman:
		return story.HumanBudget >= MaxHumanRoundTrips
	default:
		return true // Unknown budget class = exhausted
	}
}

// ReconstructBudgetsFromFailures rebuilds in-memory budget counters from failure counts.
// Called on resume to restore budgets from the durable failures table.
func (q *Queue) ReconstructBudgetsFromFailures(storyID string, failureCounts map[string]int) {
	q.mutex.Lock()
	defer q.mutex.Unlock()

	story, exists := q.stories[storyID]
	if !exists {
		return
	}

	if count, ok := failureCounts[string(proto.FailureActionRetryAttempt)]; ok {
		story.AttemptRetryBudget = count
	}
	if count, ok := failureCounts[string(proto.FailureActionRewriteStory)]; ok {
		story.RewriteBudget = count
	}
	if count, ok := failureCounts[string(proto.FailureActionRepairEnvironment)]; ok {
		story.RepairBudget = count
	}
	if count, ok := failureCounts[string(proto.FailureActionAskHuman)]; ok {
		story.HumanBudget = count
	}
	// Also count mark_failed as attempt retries (they represent exhausted attempts)
	if count, ok := failureCounts[string(proto.FailureActionMarkFailed)]; ok {
		story.AttemptRetryBudget += count
	}
}

// GetHeldStories returns all stories currently on hold.
func (q *Queue) GetHeldStories() []*QueuedStory {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	var held []*QueuedStory
	for _, story := range q.stories {
		if story.GetStatus() == StatusOnHold {
			held = append(held, story)
		}
	}

	sort.Slice(held, func(i, j int) bool {
		return held[i].ID < held[j].ID
	})

	return held
}

// HoldStory places a story on hold with the given metadata.
// Clears AssignedAgent so the coder slot is freed. Persists the updated story to DB.
func (q *Queue) HoldStory(storyID, reason, owner, failureID, note string) error {
	q.mutex.Lock()
	story, exists := q.stories[storyID]
	if !exists {
		q.mutex.Unlock()
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	if story.GetStatus() == StatusDone || story.GetStatus() == StatusFailed {
		q.mutex.Unlock()
		return fmt.Errorf("cannot hold terminal story %s (status=%s)", storyID, story.GetStatus())
	}

	_ = story.SetStatus(StatusOnHold)
	now := time.Now().UTC()
	story.HoldReason = reason
	story.HoldSince = &now
	story.HoldOwner = owner
	story.HoldNote = note
	story.BlockedByFailureID = failureID
	story.AssignedAgent = ""
	story.LastUpdated = now
	q.mutex.Unlock()

	// Persist to database
	if q.persistenceChannel != nil {
		persistence.PersistStory(story.ToPersistenceStory(), q.persistenceChannel)
	}

	return nil
}

// ReleaseHeldStories releases the specified stories from on_hold back to pending.
// Clears all hold metadata, AssignedAgent, StartedAt, and ApprovedPlan for a fresh start.
// Returns the IDs of stories that were actually released.
func (q *Queue) ReleaseHeldStories(storyIDs []string, _ string) ([]string, error) {
	q.mutex.Lock()
	released := make([]string, 0, len(storyIDs))
	for _, storyID := range storyIDs {
		story, exists := q.stories[storyID]
		if !exists {
			continue
		}
		if story.GetStatus() != StatusOnHold {
			continue
		}

		_ = story.SetStatus(StatusPending)
		story.HoldReason = ""
		story.HoldSince = nil
		story.HoldOwner = ""
		story.HoldNote = ""
		story.BlockedByFailureID = ""
		story.AssignedAgent = ""
		story.StartedAt = nil
		story.ApprovedPlan = ""
		story.LastUpdated = time.Now().UTC()
		released = append(released, storyID)
	}

	// Collect persistence snapshots while still under lock
	var toPersist []*persistence.Story
	if q.persistenceChannel != nil {
		for _, storyID := range released {
			if story, exists := q.stories[storyID]; exists {
				toPersist = append(toPersist, story.ToPersistenceStory())
			}
		}
	}
	q.mutex.Unlock()

	// Persist outside the lock
	for _, dbStory := range toPersist {
		persistence.PersistStory(dbStory, q.persistenceChannel)
	}

	return released, nil
}

// ReleaseHeldStoriesByFailure releases all stories held by a specific failure ID.
// Returns the IDs of stories that were released.
func (q *Queue) ReleaseHeldStoriesByFailure(failureID, cause string) ([]string, error) {
	q.mutex.RLock()
	var matchingIDs []string
	for _, story := range q.stories {
		if story.GetStatus() == StatusOnHold && story.BlockedByFailureID == failureID {
			matchingIDs = append(matchingIDs, story.ID)
		}
	}
	q.mutex.RUnlock()

	if len(matchingIDs) == 0 {
		return nil, nil
	}

	return q.ReleaseHeldStories(matchingIDs, cause)
}

// GetActiveStoriesForScope returns story IDs that should be held for a given failure scope.
// For "story" scope, returns only the given storyID. For "epoch", returns active stories
// in the same spec. For "system", returns all active stories.
// Active means pending or dispatched — not done, failed, or already on_hold.
func (q *Queue) GetActiveStoriesForScope(scope proto.FailureScope, storyID string) []string {
	q.mutex.RLock()
	defer q.mutex.RUnlock()

	// Find the spec ID for the failing story
	var specID string
	if story, exists := q.stories[storyID]; exists {
		specID = story.SpecID
	}

	isActive := func(s *QueuedStory) bool {
		status := s.GetStatus()
		return status == StatusPending || status == StatusDispatched
	}

	var ids []string
	switch scope {
	case proto.FailureScopeStory:
		ids = []string{storyID}
	case proto.FailureScopeEpoch:
		for _, s := range q.stories {
			if s.SpecID == specID && isActive(s) && s.ID != storyID {
				ids = append(ids, s.ID)
			}
		}
	case proto.FailureScopeSystem:
		for _, s := range q.stories {
			if isActive(s) && s.ID != storyID {
				ids = append(ids, s.ID)
			}
		}
	}

	return ids
}

// areDependenciesSatisfiedLocked checks if all dependencies of a story are satisfied.
// Must be called with mutex held.
func (q *Queue) areDependenciesSatisfiedLocked(storyID string) bool {
	story, exists := q.stories[storyID]
	if !exists {
		return false
	}

	for _, depID := range story.DependsOn {
		depStory, exists := q.stories[depID]
		if !exists {
			// Dependency not in queue - assume satisfied (external dependency)
			continue
		}
		if depStory.GetStatus() != StatusDone {
			return false
		}
	}
	return true
}

// UpdateStoryFailureMetadata atomically updates a story's failure-related fields under the write lock.
func (q *Queue) UpdateStoryFailureMetadata(storyID, reason string, fi *proto.FailureInfo) {
	q.mutex.Lock()
	defer q.mutex.Unlock()
	story, exists := q.stories[storyID]
	if !exists {
		return
	}
	story.AttemptCount++
	story.LastFailReason = reason
	story.LastFailureInfo = fi
}
