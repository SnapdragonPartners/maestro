package architect

import (
	"context"
	"fmt"
	"time"

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
		id := utils.GetMapFieldOr(reqMap, "id", "")
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
			ID:                 id,
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

// loadStoriesFromSubmitResultData loads stories into the queue from ProcessEffect.Data.
// This is called during spec review in REQUEST state (after PM spec approval).
// Returns spec ID, story IDs, and error.
//
// Dependency resolution uses ordinal IDs (req_001, req_002, etc.) from the LLM output.
// A bootstrap gate ensures all app stories depend on the last devops story (if any).
// The resulting DAG is validated for cycles before returning.
func (d *Driver) loadStoriesFromSubmitResultData(_ context.Context, specMarkdown string, effectData map[string]any) (string, []string, error) {
	// Reset PM notification flag so new spec lifecycle can re-notify
	d.pmAllCompleteNotified = false

	// Convert ProcessEffect.Data directly to Requirements (no JSON round-trip)
	requirements, err := d.convertToolResultToRequirements(effectData)
	if err != nil {
		return "", nil, fmt.Errorf("failed to convert tool result to requirements: %w", err)
	}

	// Create and persist spec record
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

	// First pass: create all stories and build ordinalID → storyID map
	ordinalToStory := make(map[string]string, len(requirements))
	storyIDs := make([]string, 0, len(requirements))

	type storyEntry struct {
		storyID string
		req     *Requirement
		title   string
		content string
	}

	entries := make([]storyEntry, 0, len(requirements))
	for i := range requirements {
		req := &requirements[i]
		storyID, genErr := persistence.GenerateStoryID()
		if genErr != nil {
			return "", nil, fmt.Errorf("failed to generate story ID: %w", genErr)
		}

		title, content := d.requirementToStoryContent(req)

		// Map ordinal ID to story ID
		if req.ID != "" {
			ordinalToStory[req.ID] = storyID
		}

		entries = append(entries, storyEntry{storyID: storyID, req: req, title: title, content: content})
		storyIDs = append(storyIDs, storyID)
	}

	// Second pass: resolve ordinal dependencies to real story IDs and add stories to queue
	var lastDevopsStoryID string
	for i := range entries {
		entry := &entries[i]
		var resolvedDeps []string
		for _, depOrdinal := range entry.req.Dependencies {
			depStoryID, found := ordinalToStory[depOrdinal]
			if !found {
				return "", nil, fmt.Errorf("unresolvable dependency: requirement %q (story %q) references unknown ordinal %q",
					entry.req.ID, entry.title, depOrdinal)
			}
			resolvedDeps = append(resolvedDeps, depStoryID)
		}

		d.queue.AddStory(entry.storyID, specID, entry.title, entry.content, entry.req.StoryType, resolvedDeps, entry.req.EstimatedPoints)

		// Track the last devops story for bootstrap gate
		if entry.req.StoryType == "devops" {
			lastDevopsStoryID = entry.storyID
		}
	}

	// Bootstrap gate: all app stories depend on the last devops story (if one exists)
	if lastDevopsStoryID != "" {
		allStories := d.queue.GetAllStories()
		for _, story := range allStories {
			if story.SpecID != specID || story.StoryType != "app" {
				continue
			}
			// Add last devops story as dependency if not already present
			alreadyDepends := false
			for _, dep := range story.DependsOn {
				if dep == lastDevopsStoryID {
					alreadyDepends = true
					break
				}
			}
			if !alreadyDepends {
				story.DependsOn = append(story.DependsOn, lastDevopsStoryID)
			}
		}
		d.logger.Info("🔧 Bootstrap gate: all app stories depend on last devops story %s", lastDevopsStoryID)
	}

	// Validate DAG — detect cycles before persisting
	cycles := d.queue.DetectCycles()
	if len(cycles) > 0 {
		// Clear the invalid stories so caller can retry
		d.queue.ClearAll()
		return "", nil, fmt.Errorf("dependency cycle detected in generated stories: %v", cycles)
	}

	// Flush stories to database
	d.queue.FlushToDatabase()

	// Mark spec as processed
	spec.ProcessedAt = &[]time.Time{time.Now()}[0]
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil,
	}

	// Store completion state
	d.SetStateData(StateKeySpecID, specID)
	d.SetStateData(StateKeyStoryIDs, storyIDs)
	d.SetStateData(StateKeyStoriesGenerated, true)
	d.SetStateData(StateKeyStoriesCount, len(storyIDs))

	d.logger.Info("✅ Loaded %d stories from spec (spec_id: %s)", len(storyIDs), specID)
	return specID, storyIDs, nil
}
