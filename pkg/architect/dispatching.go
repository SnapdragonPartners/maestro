package architect

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/proto"
)

const (
	buildSystemMake   = "make"
	buildSystemPython = "python"
	buildSystemNode   = "node"
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

	// Check if there are ready stories to dispatch.
	if story := d.queue.NextReadyStory(); story != nil {
		d.logger.Info("🚀 DISPATCHING: Found ready story %s (%s), dispatching to coder", story.ID, story.Title)
		// Attempt to dispatch the story (error handling is internal)
		_ = d.dispatchReadyStory(ctx, story.ID)
		// Transition to MONITORING after dispatch attempt (successful or not)
		d.logger.Info("🚀 DISPATCHING → MONITORING: Story dispatched, returning to monitor coder progress")
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

	if story.Status != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.Status)
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
		storyMsg.SetPayload(proto.KeyFilePath, story.FilePath)
		storyMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
		storyMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)
		storyMsg.SetPayload(proto.KeyStoryType, story.StoryType) // Pass actual story type

		// Read and parse story content for the coder.
		if content, requirements, err := d.parseStoryContent(story.FilePath); err == nil {
			storyMsg.SetPayload(proto.KeyContent, content)
			storyMsg.SetPayload(proto.KeyRequirements, requirements)

			// Detect backend from story content and requirements.
			backend := d.detectBackend(storyID, content, requirements)
			storyMsg.SetPayload(proto.KeyBackend, backend)
		} else {
			// Fallback to title if content parsing fails.
			storyMsg.SetPayload(proto.KeyContent, story.Title)
			storyMsg.SetPayload(proto.KeyRequirements, []string{})

			// Default backend detection from title.
			backend := d.detectBackend(storyID, story.Title, []string{})
			storyMsg.SetPayload(proto.KeyBackend, backend)
		}
	}

	// Send story to dispatcher.

	dispatchEffect := &DispatchStoryEffect{Story: storyMsg}
	if err := d.ExecuteEffect(ctx, dispatchEffect); err != nil {
		return err
	}

	// Only mark story as dispatched AFTER successful channel send.
	if err := d.queue.MarkInProgress(storyID, "dispatcher"); err != nil {
		return fmt.Errorf("failed to mark story as dispatched: %w", err)
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
		statusCounts[string(story.Status)]++

		switch story.Status {
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
		case StatusCompleted:
			completedStories = append(completedStories, story.ID)
		case StatusInProgress:
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

// detectDeadlock checks if the system is in a deadlock state.
// Deadlock occurs when: no stories are in progress AND no stories are ready AND not all stories are completed.
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

	// Check if any stories are in progress - not a deadlock
	inProgressStories := d.queue.GetStoriesByStatus(StatusInProgress)
	if len(inProgressStories) > 0 {
		d.logger.Debug("🚀 DISPATCHING: No deadlock - %d stories in progress: %v",
			len(inProgressStories), d.getStoryIDs(inProgressStories))
		return false
	}

	// At this point: no completed stories, no ready stories, no in-progress stories
	// This indicates a deadlock - stories are stuck due to dependency issues
	pendingStories := d.queue.GetStoriesByStatus(StatusPending)
	d.logger.Error("🚀 DISPATCHING: DEADLOCK DETECTED - %d stories pending but none ready or in progress", len(pendingStories))

	// Log details about the deadlocked stories
	for _, story := range pendingStories {
		dependsOnStr := "none"
		if len(story.DependsOn) > 0 {
			dependsOnStr = fmt.Sprintf("%v", story.DependsOn)
		}
		d.logger.Error("🚀 DISPATCHING: Deadlocked story %s (%s) depends on: %s",
			story.ID, story.Title, dependsOnStr)

		// Check status of dependencies
		for _, depID := range story.DependsOn {
			if depStory, exists := d.queue.GetStory(depID); exists {
				d.logger.Error("🚀 DISPATCHING: Dependency %s status: %s", depID, depStory.Status)
			} else {
				d.logger.Error("🚀 DISPATCHING: Dependency %s NOT FOUND in queue", depID)
			}
		}
	}

	return true
}

// getStoryIDs extracts story IDs from a slice of QueuedStory pointers.
func (d *Driver) getStoryIDs(stories []*QueuedStory) []string {
	ids := make([]string, len(stories))
	for i, story := range stories {
		ids[i] = story.ID
	}
	return ids
}

// parseStoryContent reads a story file and extracts content and requirements for the coder.
func (d *Driver) parseStoryContent(filePath string) (string, []string, error) {
	// Read the story file.
	fileBytes, err := os.ReadFile(filePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read story file %s: %w", filePath, err)
	}

	content := string(fileBytes)

	// Skip YAML frontmatter (everything before the second ---).
	lines := strings.Split(content, "\n")
	contentStart := 0
	dashCount := 0
	for i, line := range lines {
		if strings.TrimSpace(line) == "---" {
			dashCount++
			if dashCount == 2 {
				contentStart = i + 1
				break
			}
		}
	}

	if contentStart >= len(lines) {
		return "", nil, fmt.Errorf("no content found after YAML frontmatter in %s", filePath)
	}

	// Get content after frontmatter.
	contentLines := lines[contentStart:]
	storyContent := strings.Join(contentLines, "\n")

	// Extract Story description (everything after **Story** until **Acceptance Criteria**).
	storyStart := strings.Index(storyContent, "**Story**")
	criteriaStart := strings.Index(storyContent, "**Acceptance Criteria**")

	var storyDescription string
	if storyStart != -1 && criteriaStart != -1 {
		storyDescription = strings.TrimSpace(storyContent[storyStart+9 : criteriaStart])
	} else if storyStart != -1 {
		storyDescription = strings.TrimSpace(storyContent[storyStart+9:])
	} else {
		// Fallback: use first paragraph.
		paragraphs := strings.Split(strings.TrimSpace(storyContent), "\n\n")
		if len(paragraphs) > 0 {
			storyDescription = strings.TrimSpace(paragraphs[0])
		}
	}

	// Extract requirements from Acceptance Criteria bullets.
	var requirements []string
	if criteriaStart != -1 {
		criteriaSection := storyContent[criteriaStart+23:] // Skip "**Acceptance Criteria**"
		lines := strings.Split(criteriaSection, "\n")
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "*") || strings.HasPrefix(line, "-") {
				// Remove bullet point marker and clean up.
				requirement := strings.TrimSpace(line[1:])
				if requirement != "" {
					requirements = append(requirements, requirement)
				}
			}
		}
	}

	return storyDescription, requirements, nil
}

// detectBackend analyzes story content and requirements to determine the appropriate backend.
func (d *Driver) detectBackend(_ /* storyID */, content string, requirements []string) string {
	// Convert content to lowercase for case-insensitive matching.
	contentLower := strings.ToLower(content)

	// Convert requirements to lowercase for case-insensitive matching.
	requirementsLower := make([]string, len(requirements))
	for i, req := range requirements {
		requirementsLower[i] = strings.ToLower(req)
	}

	// Check content for backend indicators.
	if containsBackendKeywords(contentLower, []string{
		"go", "golang", "go.mod", "go.sum", "main.go", "package main",
		"func main", "import \"", "go build", "go test", "go run",
	}) {
		return "go"
	}

	if containsBackendKeywords(contentLower, []string{
		"python", "pip", "requirements.txt", "setup.py", "pyproject.toml",
		"def ", "import ", "from ", "python3", "venv", "virtualenv", "uv",
	}) {
		return buildSystemPython
	}

	if containsBackendKeywords(contentLower, []string{
		"javascript", "typescript", "node", "npm", "package.json", "yarn",
		"pnpm", "bun", "const ", "let ", "var ", "function", "=>", "nodejs",
	}) {
		return buildSystemNode
	}

	if containsBackendKeywords(contentLower, []string{
		"makefile", "gcc", "clang", "c++", "cpp",
	}) || strings.Contains(contentLower, " make ") || strings.HasPrefix(contentLower, "make ") || strings.HasSuffix(contentLower, " make") || strings.Contains(contentLower, " c ") {
		return buildSystemMake
	}

	// Check requirements for backend indicators.
	for _, req := range requirementsLower {
		if containsBackendKeywords(req, []string{
			"go", "golang", "go.mod", "go.sum", "main.go", "package main",
		}) {
			return "go"
		}

		if containsBackendKeywords(req, []string{
			"python", "pip", "requirements.txt", "setup.py", "pyproject.toml",
		}) {
			return buildSystemPython
		}

		if containsBackendKeywords(req, []string{
			"javascript", "typescript", "node", "npm", "package.json", "yarn",
		}) {
			return buildSystemNode
		}

		if containsBackendKeywords(req, []string{
			"makefile", "gcc", "clang",
		}) || strings.Contains(req, " make ") || strings.HasPrefix(req, "make ") || strings.HasSuffix(req, " make") {
			return buildSystemMake
		}
	}

	// Default to null backend if no specific backend detected.
	return "null"
}

// containsBackendKeywords checks if text contains any of the given keywords.
func containsBackendKeywords(text string, keywords []string) bool {
	for _, keyword := range keywords {
		if strings.Contains(text, keyword) {
			return true
		}
	}
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
