package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// handleDispatching processes the dispatching phase (queue management and story assignment).
func (d *Driver) handleDispatching(ctx context.Context) (proto.State, error) {
	// State: managing queue and assigning stories
	d.logger.Info("🚀 DISPATCHING: Starting dependency resolution and story assignment")

	// Initialize queue if not already done.
	if _, exists := d.stateData["queue_initialized"]; !exists {
		d.logger.Info("🚀 DISPATCHING: Initializing queue (recovery scenario)")
		// Queue should already be populated during SCOPING phase
		// Only load from database if this is a recovery scenario

		// Detect cycles in dependencies.
		cycles := d.queue.DetectCycles()
		if len(cycles) > 0 {
			return StateError, fmt.Errorf("dependency cycles detected: %v", cycles)
		}

		// Persist queue state to JSON for monitoring.
		if err := d.persistQueueState(); err != nil {
			return StateError, fmt.Errorf("critical: failed to persist queue state: %w", err)
		}

		d.stateData["queue_initialized"] = true
		d.stateData["queue_management_completed_at"] = time.Now().UTC()

		// Get queue summary for logging.
		summary := d.queue.GetQueueSummary()
		d.logger.Info("🚀 DISPATCHING: queue initialized - %d stories (%d ready)",
			summary["total_stories"], summary["ready_stories"])
		d.stateData["queue_summary"] = summary
	}

	// Log current queue state for debugging
	d.logQueueState()

	// Get ALL ready stories to dispatch (not just one)
	readyStories := d.queue.GetReadyStories()
	if len(readyStories) > 0 {
		d.logger.Info("🚀 DISPATCHING: Found %d ready stories, dispatching all to enable parallel execution", len(readyStories))

		// Dispatch all ready stories to maximize parallelism
		dispatchedCount := 0
		for _, story := range readyStories {
			d.logger.Info("🚀 DISPATCHING: Dispatching story %s (%s) to coder", story.ID, story.Title)
			if err := d.dispatchReadyStory(ctx, story.ID); err != nil {
				d.logger.Error("🚀 DISPATCHING: Failed to dispatch story %s: %v", story.ID, err)
				// Continue dispatching other stories even if one fails
			} else {
				dispatchedCount++
			}
		}

		d.logger.Info("🚀 DISPATCHING → MONITORING: Successfully dispatched %d/%d stories, returning to monitor coder progress",
			dispatchedCount, len(readyStories))
		return StateMonitoring, nil
	}

	// If no stories are ready and all are completed, we're done.
	if d.queue.AllStoriesCompleted() {
		d.logger.Info("🚀 DISPATCHING → DONE: All stories completed successfully")
		return StateDone, nil
	}

	// No ready stories found - check for deadlock before returning to MONITORING
	if d.detectDeadlock() {
		return StateError, fmt.Errorf("deadlock detected: no stories in progress, no stories ready, but not all stories completed")
	}

	// No ready stories found, return to MONITORING per canonical STATES.md
	// DISPATCHING should always transition to MONITORING after processing
	d.logger.Info("🚀 DISPATCHING → MONITORING: No ready stories found, returning to wait for story completions")
	return StateMonitoring, nil
}

// dispatchReadyStory assigns a ready story to an available agent.
func (d *Driver) dispatchReadyStory(ctx context.Context, storyID string) error {
	// Get the story from queue.
	story, exists := d.queue.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	if story.GetStatus() != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.GetStatus())
	}

	// Send to dispatcher via story message.

	return d.sendStoryToDispatcher(ctx, storyID)
}

// sendStoryToDispatcher sends a story to the dispatcher.
func (d *Driver) sendStoryToDispatcher(ctx context.Context, storyID string) error {
	// Create story message for the dispatcher ("coder" targets any available coder).
	storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, d.architectID, "coder")
	storyMsg.SetPayload(proto.KeyStoryID, storyID)

	// Get story details.
	if story, exists := d.queue.stories[storyID]; exists {
		storyMsg.SetPayload(proto.KeyTitle, story.Title)
		storyMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
		storyMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)
		storyMsg.SetPayload(proto.KeyStoryType, story.StoryType)

		// Use story content from the queue (set during SCOPING)
		content := story.Content
		if content == "" {
			// Fallback to title if content is not set
			content = story.Title
		}
		storyMsg.SetPayload(proto.KeyContent, content)

		// Parse requirements from content if available
		requirements := []string{} // TODO: Extract requirements from story content during SCOPING
		storyMsg.SetPayload(proto.KeyRequirements, requirements)
	}

	// Send story to dispatcher.

	dispatchEffect := &DispatchStoryEffect{Story: storyMsg}
	if err := d.ExecuteEffect(ctx, dispatchEffect); err != nil {
		return err
	}

	// Only mark story as pending AFTER successful channel send.
	if err := d.queue.UpdateStoryStatus(storyID, StatusPending); err != nil {
		return fmt.Errorf("failed to mark story as pending: %w", err)
	}

	return nil
}

// logQueueState logs detailed queue state for debugging dependency resolution.
func (d *Driver) logQueueState() {
	if d.queue == nil {
		d.logger.Warn("🚀 DISPATCHING: Queue is nil")
		return
	}

	allStories := d.queue.GetAllStories()
	d.logger.Info("🚀 DISPATCHING: Queue state analysis (%d total stories)", len(allStories))

	// Group stories by status
	statusCounts := make(map[string]int)
	var pendingStories, completedStories, inProgressStories []string

	for _, story := range allStories {
		statusCounts[string(story.GetStatus())]++

		switch story.GetStatus() {
		case StatusPending:
			// Check if dependencies are met for pending stories
			dependencyStatus := "BLOCKED"
			if d.queue != nil {
				readyStories := d.queue.GetReadyStories()
				for _, readyStory := range readyStories {
					if readyStory.ID == story.ID {
						dependencyStatus = "READY"
						break
					}
				}
			}
			dependsOnStr := "none"
			if len(story.DependsOn) > 0 {
				dependsOnStr = fmt.Sprintf("%v", story.DependsOn)
			}
			pendingStories = append(pendingStories, fmt.Sprintf("%s (%s, deps: %s)",
				story.ID, dependencyStatus, dependsOnStr))
		case StatusDone:
			completedStories = append(completedStories, story.ID)
		case StatusAssigned:
			inProgressStories = append(inProgressStories, story.ID)
		}
	}

	// Log status summary
	d.logger.Info("🚀 DISPATCHING: Status summary - pending: %d, in_progress: %d, completed: %d",
		statusCounts["pending"], statusCounts["in_progress"], statusCounts["completed"])

	// Log detailed story states
	if len(completedStories) > 0 {
		d.logger.Info("🚀 DISPATCHING: Completed stories: %v", completedStories)
	}
	if len(inProgressStories) > 0 {
		d.logger.Info("🚀 DISPATCHING: In-progress stories: %v", inProgressStories)
	}
	if len(pendingStories) > 0 {
		d.logger.Info("🚀 DISPATCHING: Pending stories: %v", pendingStories)
	}

	// Check for ready stories specifically
	readyStories := d.queue.GetReadyStories()
	if len(readyStories) > 0 {
		var readyIDs []string
		for _, story := range readyStories {
			readyIDs = append(readyIDs, story.ID)
		}
		d.logger.Info("🚀 DISPATCHING: Ready to dispatch: %v", readyIDs)
	} else {
		d.logger.Info("🚀 DISPATCHING: No stories ready to dispatch")
	}
}

// detectDeadlock checks if the system is in a true deadlock state.
// A deadlock occurs when:
// 1. Not all stories are completed
// 2. No stories are ready to dispatch (all unreleased stories are blocked by dependencies)
// 3. All unreleased stories have circular dependencies or depend on stories that don't exist
//
// This is a more robust check than looking at agent states, which can be brittle.
func (d *Driver) detectDeadlock() bool {
	if d.queue == nil {
		return false // Can't detect deadlock without queue
	}

	allStories := d.queue.GetAllStories()
	if len(allStories) == 0 {
		return false // No stories means no deadlock
	}

	// Check if all stories are completed - not a deadlock
	if d.queue.AllStoriesCompleted() {
		return false
	}

	// Check if any stories are ready to dispatch - not a deadlock
	readyStories := d.queue.GetReadyStories()
	if len(readyStories) > 0 {
		return false
	}

	// At this point: not all stories completed AND no stories ready
	// Now check if unreleased stories have valid, satisfiable dependencies

	// Get all unreleased (not done) stories
	var unreleasedStories []*QueuedStory
	for _, story := range allStories {
		if story.GetStatus() != StatusDone {
			unreleasedStories = append(unreleasedStories, story)
		}
	}

	if len(unreleasedStories) == 0 {
		// All stories are done, should have been caught above but double-check
		return false
	}

	// For each unreleased story, check if its dependencies can ever be satisfied
	// A dependency is unsatisfiable if:
	// 1. The dependency doesn't exist in the queue
	// 2. The dependencies form a cycle (circular dependency)

	// First check for circular dependencies
	cycles := d.queue.DetectCycles()
	if len(cycles) > 0 {
		d.logger.Error("🚀 DISPATCHING: DEADLOCK DETECTED - Circular dependencies found")
		for _, cycle := range cycles {
			d.logger.Error("🚀 DISPATCHING: Dependency cycle: %v", cycle)
		}
		return true
	}

	// Check for missing dependencies
	hasMissingDeps := false
	for _, story := range unreleasedStories {
		for _, depID := range story.DependsOn {
			if _, exists := d.queue.GetStory(depID); !exists {
				d.logger.Error("🚀 DISPATCHING: DEADLOCK DETECTED - Story %s depends on non-existent story %s",
					story.ID, depID)
				hasMissingDeps = true
			}
		}
	}

	if hasMissingDeps {
		return true
	}

	// If we get here: no cycles, no missing deps, no ready stories, not all complete
	// This means there are stories being actively worked on - NOT a deadlock
	d.logger.Debug("🚀 DISPATCHING: No deadlock - %d unreleased stories have valid dependencies and work is in progress",
		len(unreleasedStories))
	return false
}

// persistQueueState saves the current queue state to the state store.
func (d *Driver) persistQueueState() error {
	queueData, err := d.queue.ToJSON()
	if err != nil {
		return fmt.Errorf("failed to serialize queue: %w", err)
	}

	// Store queue data in state data for persistence.
	d.stateData["queue_json"] = string(queueData)

	return nil
}
