package architect

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// StoryStatus represents the status of a story in the queue
type StoryStatus string

const (
	StatusPending            StoryStatus = "pending"
	StatusInProgress         StoryStatus = "in_progress"
	StatusWaitingReview      StoryStatus = "waiting_review"
	StatusCompleted          StoryStatus = "completed"
	StatusBlocked            StoryStatus = "blocked"
	StatusCancelled          StoryStatus = "cancelled"
	StatusAwaitHumanFeedback StoryStatus = "await_human_feedback"
)

// QueuedStory represents a story in the architect's queue
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

// Queue manages the architect's story queue with dependency resolution
type Queue struct {
	stories      map[string]*QueuedStory
	storiesDir   string
	readyStoryCh chan<- string // Channel to notify when stories become ready
}

// NewQueue creates a new queue manager
func NewQueue(storiesDir string) *Queue {
	return &Queue{
		stories:    make(map[string]*QueuedStory),
		storiesDir: storiesDir,
		// readyStoryCh will be set by SetReadyChannel
	}
}

// SetReadyChannel sets the channel for ready story notifications
func (q *Queue) SetReadyChannel(ch chan<- string) {
	q.readyStoryCh = ch
}

// LoadFromDirectory scans the stories directory and loads all story files
func (q *Queue) LoadFromDirectory() error {
	if _, err := os.Stat(q.storiesDir); os.IsNotExist(err) {
		// Stories directory doesn't exist yet, start with empty queue
		return nil
	}

	files, err := filepath.Glob(filepath.Join(q.storiesDir, "*.md"))
	if err != nil {
		return fmt.Errorf("failed to scan stories directory: %w", err)
	}

	for _, file := range files {
		story, err := q.parseStoryFile(file)
		if err != nil {
			// Log warning but continue with other files
			fmt.Printf("Warning: failed to parse story file %s: %v\n", file, err)
			continue
		}

		// Initialize as pending if not already tracked
		if existing, exists := q.stories[story.ID]; !exists {
			story.Status = StatusPending
			story.LastUpdated = time.Now().UTC()
			q.stories[story.ID] = story
		} else {
			// Update metadata but preserve status and tracking info
			existing.Title = story.Title
			existing.DependsOn = story.DependsOn
			existing.EstimatedPoints = story.EstimatedPoints
			existing.FilePath = story.FilePath
			existing.LastUpdated = time.Now().UTC()
		}
	}

	// After loading all stories, check for initially ready ones
	q.checkAndNotifyReady()

	return nil
}

// parseStoryFile reads a story markdown file and extracts metadata
func (q *Queue) parseStoryFile(filePath string) (*QueuedStory, error) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read file: %w", err)
	}

	story := &QueuedStory{
		FilePath: filePath,
	}

	// Parse front-matter
	if err := q.parseFrontMatter(string(content), story); err != nil {
		return nil, fmt.Errorf("failed to parse front-matter: %w", err)
	}

	return story, nil
}

// parseFrontMatter extracts front-matter from markdown content
func (q *Queue) parseFrontMatter(content string, story *QueuedStory) error {
	// Look for front-matter block
	frontMatterRegex := regexp.MustCompile(`(?s)^---\n(.*?)\n---`)
	matches := frontMatterRegex.FindStringSubmatch(content)
	if len(matches) < 2 {
		return fmt.Errorf("no front-matter found")
	}

	frontMatter := matches[1]
	lines := strings.Split(frontMatter, "\n")

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		// Parse key-value pairs
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch key {
		case "id":
			story.ID = value
		case "title":
			// Remove quotes if present
			story.Title = strings.Trim(value, `"`)
		case "depends_on":
			story.DependsOn = q.parseStringArray(value)
		case "est_points":
			if points, err := parseEstimatedPoints(value); err == nil {
				story.EstimatedPoints = points
			}
		}
	}

	// Validate required fields
	if story.ID == "" {
		return fmt.Errorf("missing required field: id")
	}
	if story.Title == "" {
		return fmt.Errorf("missing required field: title")
	}

	return nil
}

// parseStringArray parses a YAML-style array from a string
func (q *Queue) parseStringArray(value string) []string {
	value = strings.TrimSpace(value)

	// Handle empty array
	if value == "[]" || value == "" {
		return []string{}
	}

	// Remove brackets and split by comma
	value = strings.Trim(value, "[]")
	parts := strings.Split(value, ",")

	var result []string
	for _, part := range parts {
		part = strings.TrimSpace(part)
		part = strings.Trim(part, `"'`) // Remove quotes
		if part != "" {
			result = append(result, part)
		}
	}

	return result
}

// NextReadyStory returns the next story that's ready to be worked on
func (q *Queue) NextReadyStory() *QueuedStory {
	ready := q.GetReadyStories()
	if len(ready) == 0 {
		return nil
	}

	// Sort by estimated points (smaller first) then by ID for deterministic ordering
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].EstimatedPoints == ready[j].EstimatedPoints {
			return ready[i].ID < ready[j].ID
		}
		return ready[i].EstimatedPoints < ready[j].EstimatedPoints
	})

	return ready[0]
}

// GetReadyStories returns all stories that are ready to be worked on
func (q *Queue) GetReadyStories() []*QueuedStory {
	var ready []*QueuedStory

	for _, story := range q.stories {
		if story.Status != StatusPending {
			continue
		}

		// Check if all dependencies are completed
		if q.areDependenciesMet(story) {
			ready = append(ready, story)
		}
	}

	return ready
}

// AllStoriesCompleted checks if all stories in the queue are completed
func (q *Queue) AllStoriesCompleted() bool {
	for _, story := range q.stories {
		if story.Status != StatusCompleted && story.Status != StatusCancelled {
			return false
		}
	}
	return true
}

// areDependenciesMet checks if all dependencies for a story are completed
func (q *Queue) areDependenciesMet(story *QueuedStory) bool {
	for _, depID := range story.DependsOn {
		dep, exists := q.stories[depID]
		if !exists {
			// Dependency doesn't exist - consider it as not met
			return false
		}
		if dep.Status != StatusCompleted {
			return false
		}
	}
	return true
}

// MarkInProgress marks a story as in progress and assigns it to an agent
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

	return nil
}

// MarkWaitingReview marks a story as waiting for review
func (q *Queue) MarkWaitingReview(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	if story.Status != StatusInProgress {
		return fmt.Errorf("story %s is not in progress (current: %s)", storyID, story.Status)
	}

	story.Status = StatusWaitingReview
	story.LastUpdated = time.Now().UTC()

	return nil
}

// MarkCompleted marks a story as completed
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

	// Check if any pending stories became ready due to this completion
	q.checkAndNotifyReady()

	return nil
}

// checkAndNotifyReady checks for stories that became ready and notifies via channel
func (q *Queue) checkAndNotifyReady() {
	if q.readyStoryCh == nil {
		return // Channel not set, skip notifications
	}

	for _, story := range q.stories {
		if story.Status == StatusPending && q.areDependenciesMet(story) {
			// Try to notify (non-blocking)
			select {
			case q.readyStoryCh <- story.ID:
				fmt.Printf("📥 Queue: notified that story %s is ready\n", story.ID)
			default:
				// Channel full, that's OK - the dispatcher will check again
			}
		}
	}
}

// MarkBlocked marks a story as blocked
func (q *Queue) MarkBlocked(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	story.Status = StatusBlocked
	story.LastUpdated = time.Now().UTC()

	return nil
}

// MarkAwaitHumanFeedback marks a story as awaiting human feedback/intervention
func (q *Queue) MarkAwaitHumanFeedback(storyID string) error {
	story, exists := q.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	story.Status = StatusAwaitHumanFeedback
	story.LastUpdated = time.Now().UTC()

	return nil
}

// GetStory returns a story by ID
func (q *Queue) GetStory(storyID string) (*QueuedStory, bool) {
	story, exists := q.stories[storyID]
	return story, exists
}

// GetAllStories returns all stories in the queue
func (q *Queue) GetAllStories() []*QueuedStory {
	stories := make([]*QueuedStory, 0, len(q.stories))
	for _, story := range q.stories {
		stories = append(stories, story)
	}

	// Sort by ID for consistent ordering
	sort.Slice(stories, func(i, j int) bool {
		return stories[i].ID < stories[j].ID
	})

	return stories
}

// GetStoriesByStatus returns all stories with a specific status
func (q *Queue) GetStoriesByStatus(status StoryStatus) []*QueuedStory {
	var filtered []*QueuedStory
	for _, story := range q.stories {
		if story.Status == status {
			filtered = append(filtered, story)
		}
	}

	// Sort by ID for consistent ordering
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].ID < filtered[j].ID
	})

	return filtered
}

// DetectCycles detects circular dependencies in the story queue
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

// detectCyclesDFS performs depth-first search to detect cycles
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
			// Found a cycle
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

// ToJSON serializes the queue to JSON
func (q *Queue) ToJSON() ([]byte, error) {
	stories := q.GetAllStories()
	return json.MarshalIndent(stories, "", "  ")
}

// FromJSON deserializes the queue from JSON
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

// GetQueueSummary returns a summary of the queue state
func (q *Queue) GetQueueSummary() map[string]interface{} {
	summary := make(map[string]interface{})

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

// parseEstimatedPoints parses estimated points from a string value
func parseEstimatedPoints(value string) (int, error) {
	value = strings.TrimSpace(value)
	points, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid estimated points value: %s", value)
	}

	// Validate range (1-5 points typical for story estimation)
	if points < 1 || points > 5 {
		return 2, nil // Default to 2 points for out-of-range values
	}

	return points, nil
}
