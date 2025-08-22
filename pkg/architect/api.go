// Package architect provides external API operations that work outside the FSM context.
// These operations are called by the dispatcher during agent lifecycle management.
package architect

import (
	"fmt"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

// ExternalAPI provides methods for external operations outside the FSM.
// This encapsulates operations that need to be called externally but shouldn't go through
// the normal REQUEST/RESPONSE message flow.
type ExternalAPI struct {
	queue  *Queue
	logger *logx.Logger
}

// NewExternalAPI creates a new external API instance.
func NewExternalAPI(queue *Queue, logger *logx.Logger) *ExternalAPI {
	return &ExternalAPI{
		queue:  queue,
		logger: logger,
	}
}

// RequeueAndRelease atomically requeues a story and releases it if unblocked.
// This is called by the dispatcher when a coder agent fails and the story needs
// to be made available for other coders.
//
// Safety: Verifies story dependencies before release to prevent malformed releases.
func (api *ExternalAPI) RequeueAndRelease(storyID string) error {
	api.logger.Info("Requeuing and releasing story %s from failed agent", storyID)

	// Step 1: Requeue the story in internal storage
	if err := api.queue.RequeueStory(storyID); err != nil {
		return fmt.Errorf("failed to requeue story %s: %w", storyID, err)
	}
	api.logger.Info("Story %s requeued successfully", storyID)

	// Step 2: Safety check - verify story is unblocked before release
	if !api.isStoryUnblocked(storyID) {
		api.logger.Warn("Story %s is still blocked by dependencies, not releasing", storyID)
		return nil // Not an error - story is requeued but not ready for work
	}

	// Step 3: Release story to work queue for immediate assignment
	if err := api.releaseStoryToWorkQueue(storyID); err != nil {
		return fmt.Errorf("failed to release story %s to work queue: %w", storyID, err)
	}
	api.logger.Info("Story %s released to work queue for assignment", storyID)

	return nil
}

// isStoryUnblocked checks if a story has all its dependencies satisfied.
// Since this story was previously assigned to an agent, it should be unblocked,
// but we verify to be safe against malformed story IDs or dependency changes.
func (api *ExternalAPI) isStoryUnblocked(storyID string) bool {
	story, exists := api.queue.stories[storyID]
	if !exists {
		api.logger.Error("Story %s not found in queue during unblock check", storyID)
		return false
	}

	// Check if all dependencies are completed
	for _, depID := range story.DependsOn {
		depStory, depExists := api.queue.stories[depID]
		if !depExists {
			api.logger.Warn("Dependency %s not found for story %s", depID, storyID)
			return false
		}
		if depStory.GetStatus() != StoryStatus(persistence.StatusDone) {
			api.logger.Debug("Story %s blocked by incomplete dependency %s (status: %s)",
				storyID, depID, depStory.Status)
			return false
		}
	}

	return true
}

// releaseStoryToWorkQueue makes a story available for immediate assignment.
// This simulates what the DISPATCHING state would do but operates outside FSM.
func (api *ExternalAPI) releaseStoryToWorkQueue(storyID string) error {
	// Get the story
	story, exists := api.queue.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found", storyID)
	}

	// Mark as pending (ready for work) - queue calculates ready stories dynamically
	story.SetStatus(StatusPending)
	api.queue.stories[storyID] = story

	// Queue will automatically include this in GetReadyStories() if dependencies are met
	api.logger.Debug("Marked story %s as pending (ready for assignment)", storyID)

	return nil
}

// UpdateStoryStatus updates a story's status with thread-safe protection and persistence.
// This method is designed for external calls (e.g., from dispatcher) to update story status
// without blocking on architect's internal state machine.
func (api *ExternalAPI) UpdateStoryStatus(storyID string, status interface{}) error {
	// Convert interface{} to StoryStatus to avoid circular imports
	var storyStatus StoryStatus
	switch s := status.(type) {
	case string:
		storyStatus = StoryStatus(s)
	case StoryStatus:
		storyStatus = s
	default:
		return fmt.Errorf("invalid status type: %T", status)
	}

	api.logger.Info("📝 Updating story %s status to: %s", storyID, storyStatus)

	// Delegate to queue's thread-safe method
	if err := api.queue.UpdateStoryStatus(storyID, storyStatus); err != nil {
		api.logger.Error("❌ Failed to update story %s status: %v", storyID, err)
		return err
	}

	api.logger.Info("✅ Story %s status updated to: %s", storyID, storyStatus)
	return nil
}
