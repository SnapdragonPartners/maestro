package architect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// Requirement represents a parsed requirement from a specification.
//
//nolint:govet // struct alignment optimization not critical for this type
type Requirement struct {
	ID                 string            `json:"id"`
	Title              string            `json:"title"`
	Description        string            `json:"description"`
	AcceptanceCriteria []string          `json:"acceptance_criteria"`
	EstimatedPoints    int               `json:"estimated_points"`
	Priority           int               `json:"priority"`
	Dependencies       []string          `json:"dependencies"`
	Tags               []string          `json:"tags"`
	Details            map[string]string `json:"details"`
	StoryType          string            `json:"story_type"` // "devops" or "app"
}

// Note: handleScoping, parseSpecWithLLM, getSpecFileFromMessage, and loadArchitecturalKnowledge
// have been removed - specs now come through REQUEST messages and are processed in handleSpecReview().

// requirementToStoryContent converts a requirement to story title and rich markdown content.
// This is the single source of truth for how LLM requirements become story content.
func (d *Driver) requirementToStoryContent(req *Requirement) (string, string) {
	title := req.Title

	content := fmt.Sprintf("**Task**\n%s\n\n", req.Description)
	content += "**Acceptance Criteria**\n"
	for _, criteria := range req.AcceptanceCriteria {
		content += fmt.Sprintf("* %s\n", criteria)
	}

	return title, content
}

// convertToolResultToRequirements converts the structured submit_stories tool result
// directly to Requirements without any JSON serialization/deserialization.
func (d *Driver) convertToolResultToRequirements(toolResult map[string]any) ([]Requirement, error) {
	// Extract requirements array from tool result using generic helper
	requirementsAny, err := utils.GetMapField[[]any](toolResult, "requirements")
	if err != nil {
		return nil, fmt.Errorf("requirements field invalid: %w", err)
	}

	if len(requirementsAny) == 0 {
		return nil, fmt.Errorf("requirements array is empty")
	}

	// Convert each requirement from map to Requirement struct
	requirements := make([]Requirement, 0, len(requirementsAny))
	for i, reqAny := range requirementsAny {
		reqMap, ok := utils.SafeAssert[map[string]any](reqAny)
		if !ok {
			return nil, fmt.Errorf("requirement %d is not a map", i)
		}

		// Extract fields using generic helpers with defaults
		title := utils.GetMapFieldOr(reqMap, "title", "")
		description := utils.GetMapFieldOr(reqMap, "description", "")
		storyType := utils.GetMapFieldOr(reqMap, "story_type", "")

		// Handle acceptance criteria array
		var acceptanceCriteria []string
		if acAny, ok := utils.SafeAssert[[]any](reqMap["acceptance_criteria"]); ok {
			acceptanceCriteria = make([]string, 0, len(acAny))
			for _, ac := range acAny {
				if acStr, ok := utils.SafeAssert[string](ac); ok {
					acceptanceCriteria = append(acceptanceCriteria, acStr)
				}
			}
		}

		// Handle dependencies array
		var dependencies []string
		if depsAny, ok := utils.SafeAssert[[]any](reqMap["dependencies"]); ok {
			dependencies = make([]string, 0, len(depsAny))
			for _, dep := range depsAny {
				if depStr, ok := utils.SafeAssert[string](dep); ok {
					dependencies = append(dependencies, depStr)
				}
			}
		}

		// Handle estimated points (could be float64 from JSON or int)
		estimatedPoints := 2 // Default
		if points, ok := utils.SafeAssert[float64](reqMap["estimated_points"]); ok {
			estimatedPoints = int(points)
		} else if points, ok := utils.SafeAssert[int](reqMap["estimated_points"]); ok {
			estimatedPoints = points
		}

		requirement := Requirement{
			Title:              title,
			Description:        description,
			AcceptanceCriteria: acceptanceCriteria,
			EstimatedPoints:    estimatedPoints,
			Dependencies:       dependencies,
			StoryType:          storyType,
		}

		// Validate and set reasonable defaults
		if requirement.EstimatedPoints < 1 || requirement.EstimatedPoints > 5 {
			requirement.EstimatedPoints = 2 // Default to medium complexity
		}

		// Validate story type and set default if invalid or missing
		if !proto.IsValidStoryType(requirement.StoryType) {
			requirement.StoryType = string(proto.StoryTypeApp)
		}

		if requirement.Title == "" {
			continue // Skip empty requirements
		}

		if len(requirement.AcceptanceCriteria) == 0 {
			requirement.AcceptanceCriteria = []string{
				"Implementation completes successfully",
				"All tests pass",
				"Code follows project conventions",
			}
		}

		requirements = append(requirements, requirement)
	}

	if len(requirements) == 0 {
		return nil, fmt.Errorf("no valid requirements extracted from tool result")
	}

	return requirements, nil
}

// All story generation now uses the clean linear flow in handleScoping()

// loadStoriesFromSubmitResult loads stories into the queue from submit_stories tool result.
// This is called during spec review in REQUEST state (after PM spec approval).
// Returns spec ID, story IDs, and error.
func (d *Driver) loadStoriesFromSubmitResult(ctx context.Context, specMarkdown string) (string, []string, error) {
	// 1. Extract structured data from stateData (stored by processArchitectToolCalls)
	submitResult, ok := d.stateData["submit_stories_result"]
	if !ok {
		return "", nil, fmt.Errorf("submit_stories result not found in state data")
	}

	resultMap, ok := submitResult.(map[string]any)
	if !ok {
		return "", nil, fmt.Errorf("submit_stories result has unexpected type")
	}

	// 2. Convert structured tool result directly to Requirements (no JSON round-trip)
	requirements, err := d.convertToolResultToRequirements(resultMap)
	if err != nil {
		return "", nil, fmt.Errorf("failed to convert tool result to requirements: %w", err)
	}

	// 3. Create and persist spec record
	specID := persistence.GenerateSpecID()
	spec := &persistence.Spec{
		ID:        specID,
		Content:   specMarkdown,
		CreatedAt: time.Now(),
	}
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil,
	}

	// 4. Convert requirements to stories and add to queue
	storyIDs := make([]string, 0, len(requirements))
	for i := range requirements {
		req := &requirements[i]
		// Generate unique story ID
		storyID, err := persistence.GenerateStoryID()
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate story ID: %w", err)
		}

		// Calculate dependencies based on order (simple dependency model)
		var dependencies []string
		if len(req.Dependencies) > 0 {
			// Simple implementation: depend on all previous stories
			for j := 0; j < i; j++ {
				dependencies = append(dependencies, storyIDs[j])
			}
		}

		// Convert requirement to rich story content
		title, content := d.requirementToStoryContent(req)

		// Add story to internal queue
		d.queue.AddStory(storyID, specID, title, content, req.StoryType, dependencies, req.EstimatedPoints)
		storyIDs = append(storyIDs, storyID)
	}

	// 5. Container validation and dependency fixing
	if err := d.validateAndFixContainerDependencies(ctx, specID); err != nil {
		// Check if this is a retry request
		if strings.Contains(err.Error(), "retry_needed") {
			// During spec review, this would trigger a retry
			// In REQUEST state after approval, we can't retry, so just log warning
			d.logger.Warn("⚠️  Container validation would retry, but continuing with current stories")
			// Clear the error and continue
		} else {
			return "", nil, fmt.Errorf("container validation failed: %w", err)
		}
	}

	// 6. Flush stories to database
	d.queue.FlushToDatabase()

	// 7. Mark spec as processed
	spec.ProcessedAt = &[]time.Time{time.Now()}[0]
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil,
	}

	// 8. Store completion state
	d.stateData["spec_id"] = specID
	d.stateData["story_ids"] = storyIDs
	d.stateData["stories_generated"] = true
	d.stateData["stories_count"] = len(storyIDs)

	d.logger.Info("✅ Loaded %d stories from spec (spec_id: %s)", len(storyIDs), specID)
	return specID, storyIDs, nil
}

// validateAndFixContainerDependencies implements hybrid container validation.
// 1. If validateTargetContainer fails and DevOps story exists → fix DAG
// 2. If validateTargetContainer fails and no DevOps story → retry LLM
// 3. If LLM retry still has no DevOps story → fatal error.
func (d *Driver) validateAndFixContainerDependencies(ctx context.Context, specID string) error {
	// Check if we have a valid target container
	if config.IsValidTargetImage() {
		d.logger.Info("✅ Valid target container exists, no dependency fixes needed")
		return nil
	}

	d.logger.Warn("⚠️  No valid target container - checking story dependencies")

	// Get all stories from the queue
	allStories := d.queue.GetAllStories()
	if len(allStories) == 0 {
		return fmt.Errorf("no stories found in queue")
	}

	// Filter to stories for this spec and categorize by type
	var devopsStories, appStories []*QueuedStory
	for _, story := range allStories {
		if story.SpecID == specID {
			if story.StoryType == "devops" {
				devopsStories = append(devopsStories, story)
			} else if story.StoryType == "app" {
				appStories = append(appStories, story)
			}
		}
	}

	if len(devopsStories) > 0 {
		// Option 1: DevOps story exists - fix DAG to make app stories depend on DevOps
		d.logger.Info("🔧 DevOps story exists (%d found) - fixing dependencies to block app stories", len(devopsStories))
		return d.fixContainerDependencies(devopsStories, appStories)
	} else {
		// Option 2: No DevOps story - retry with LLM
		d.logger.Warn("❌ No DevOps story found - requesting LLM retry with container guidance")
		return d.retryWithContainerGuidance(ctx, specID)
	}
}

// fixContainerDependencies adds dependencies so app stories are blocked by DevOps stories.
func (d *Driver) fixContainerDependencies(devopsStories, appStories []*QueuedStory) error {
	d.logger.Info("🔄 Adding DevOps→App dependencies: %d app stories will depend on %d devops stories",
		len(appStories), len(devopsStories))

	// Make each app story depend on all DevOps stories
	for _, appStory := range appStories {
		for _, devopsStory := range devopsStories {
			// Add DevOps story ID to app story's dependencies if not already present
			dependencyExists := false
			for _, existingDep := range appStory.DependsOn {
				if existingDep == devopsStory.ID {
					dependencyExists = true
					break
				}
			}

			if !dependencyExists {
				d.logger.Debug("📌 Adding dependency: %s depends on %s", appStory.ID, devopsStory.ID)
				appStory.DependsOn = append(appStory.DependsOn, devopsStory.ID)
			}
		}
	}

	d.logger.Info("✅ Container dependencies fixed - app stories now blocked until DevOps completes")
	return nil
}

// retryWithContainerGuidance retries LLM with enhanced container guidance following the empty response retry pattern.
func (d *Driver) retryWithContainerGuidance(_ context.Context, _ string) error {
	// Get retry counter from state data (0 if not set)
	retryCount := 0
	if retryData, exists := d.stateData["container_retry_count"]; exists {
		if count, ok := retryData.(int); ok {
			retryCount = count
		}
	}

	// Maximum 1 retry (attempt 0 + attempt 1)
	if retryCount >= 1 {
		d.logger.Error("❌ RETRY EXHAUSTED: LLM failed to generate DevOps story after guidance")
		d.logger.Error("🏗️  REQUIRED: Manually add container setup requirements to your specification")
		d.logger.Error("💡 GUIDANCE: DevOps stories handle container setup, build environment, infrastructure")
		return fmt.Errorf("no DevOps story generated after retry - manual intervention required")
	}

	d.logger.Warn("🔄 RETRY ATTEMPT %d: Re-running story generation with enhanced container guidance", retryCount+1)

	// Increment retry counter for enhanced guidance in the next iteration
	d.stateData["container_retry_count"] = retryCount + 1

	// Return special error that triggers retry flow
	return fmt.Errorf("retry_needed: no DevOps story found, triggering enhanced guidance retry")
}
