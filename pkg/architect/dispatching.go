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

	// Initialize queue if not already done.
	if _, exists := d.stateData["queue_initialized"]; !exists {
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
		d.logger.Info("queue ready: %d stories (%d ready)",
			summary["total_stories"], summary["ready_stories"])
		d.stateData["queue_summary"] = summary
	}

	// Check if there are ready stories to dispatch.
	if story := d.queue.NextReadyStory(); story != nil {
		d.logger.Info("🏗️ Found ready story %s, dispatching to coder", story.ID)
		if err := d.dispatchReadyStory(ctx, story.ID); err != nil {
			// Dispatch failed - dispatcher already logged the details
			// Just note we'll retry later (story remains ready in queue)
			d.logger.Debug("🏗️ Story %s dispatch failed, will retry later", story.ID)
		} else {
			d.logger.Info("🏗️ Successfully dispatched story %s", story.ID)
		}
		// Transition to MONITORING after dispatch attempt (successful or not)
		return StateMonitoring, nil
	}

	// If no stories are ready and all are completed, we're done.
	if d.queue.AllStoriesCompleted() {
		d.logger.Info("all stories completed - transitioning to DONE")
		return StateDone, nil
	}

	// Otherwise, stay in DISPATCHING and wait for stories to become ready.
	return StateDispatching, nil
}

// dispatchReadyStory assigns a ready story to an available agent.
func (d *Driver) dispatchReadyStory(ctx context.Context, storyID string) error {
	d.logger.Info("🏗️ Dispatching ready story %s", storyID)

	// Get the story from queue.
	story, exists := d.queue.stories[storyID]
	if !exists {
		return fmt.Errorf("story %s not found in queue", storyID)
	}

	if story.Status != StatusPending {
		return fmt.Errorf("story %s is not in pending status (current: %s)", storyID, story.Status)
	}

	// Send to dispatcher via story message.
	d.logger.Info("🏗️ Sending story %s to dispatcher", storyID)

	return d.sendStoryToDispatcher(ctx, storyID)
}

// sendStoryToDispatcher sends a story to the dispatcher.
func (d *Driver) sendStoryToDispatcher(ctx context.Context, storyID string) error {
	d.logger.Info("🏗️ Sending story %s to dispatcher", storyID)

	// Create story message for the dispatcher ("coder" targets any available coder).
	storyMsg := proto.NewAgentMsg(proto.MsgTypeSTORY, d.architectID, "coder")
	storyMsg.SetPayload(proto.KeyStoryID, storyID)

	d.logger.Info("🏗️ Created STORY message %s for story %s -> dispatcher", storyMsg.ID, storyID)

	// Get story details.
	if story, exists := d.queue.stories[storyID]; exists {
		d.logger.Info("🏗️ Queue story StoryType for %s: '%s'", storyID, story.StoryType)
		storyMsg.SetPayload(proto.KeyTitle, story.Title)
		storyMsg.SetPayload(proto.KeyFilePath, story.FilePath)
		storyMsg.SetPayload(proto.KeyEstimatedPoints, story.EstimatedPoints)
		storyMsg.SetPayload(proto.KeyDependsOn, story.DependsOn)
		storyMsg.SetPayload(proto.KeyStoryType, story.StoryType) // Pass actual story type
		d.logger.Info("🏗️ Set story_type payload to '%s' for story %s", story.StoryType, storyID)

		// Read and parse story content for the coder.
		if content, requirements, err := d.parseStoryContent(story.FilePath); err == nil {
			storyMsg.SetPayload(proto.KeyContent, content)
			storyMsg.SetPayload(proto.KeyRequirements, requirements)

			// Detect backend from story content and requirements.
			backend := d.detectBackend(storyID, content, requirements)
			storyMsg.SetPayload(proto.KeyBackend, backend)
			d.logger.Info("🏗️ Detected backend '%s' for story %s", backend, storyID)
		} else {
			// Fallback to title if content parsing fails.
			storyMsg.SetPayload(proto.KeyContent, story.Title)
			storyMsg.SetPayload(proto.KeyRequirements, []string{})

			// Default backend detection from title.
			backend := d.detectBackend(storyID, story.Title, []string{})
			storyMsg.SetPayload(proto.KeyBackend, backend)
			d.logger.Info("🏗️ Detected backend '%s' for story %s (from title)", backend, storyID)
		}
	}

	// Send story to dispatcher.
	d.logger.Info("🏗️ Dispatching STORY message %s using Effects pattern", storyMsg.ID)

	dispatchEffect := &DispatchStoryEffect{Story: storyMsg}
	if err := d.ExecuteEffect(ctx, dispatchEffect); err != nil {
		return err
	}

	// Only mark story as dispatched AFTER successful channel send.
	if err := d.queue.MarkInProgress(storyID, "dispatcher"); err != nil {
		return fmt.Errorf("failed to mark story as dispatched: %w", err)
	}

	d.logger.Info("🏗️ Successfully dispatched STORY message %s to dispatcher", storyMsg.ID)
	return nil
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
func (d *Driver) detectBackend(storyID, content string, requirements []string) string {
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
	d.logger.Info("🏗️ No specific backend detected for story %s, using null backend", storyID)
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
