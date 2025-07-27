package architect

import (
	"fmt"
	"os"
	"time"

	"orchestrator/pkg/persistence"
)

// DBSpecProcessor handles processing specifications into database-stored stories.
// This replaces the file-based SpecParser with database operations.
type DBSpecProcessor struct {
	persistenceChannel chan<- *persistence.Request
}

// NewDBSpecProcessor creates a new database-aware spec processor.
func NewDBSpecProcessor(persistenceChannel chan<- *persistence.Request) *DBSpecProcessor {
	return &DBSpecProcessor{
		persistenceChannel: persistenceChannel,
	}
}

// ProcessSpecContent processes spec content and stores both the spec and generated stories in the database.
// Returns the spec ID and the generated story IDs.
func (dsp *DBSpecProcessor) ProcessSpecContent(specContent string) (string, []string, error) {
	// Check if persistence channel is available
	if dsp.persistenceChannel == nil {
		return "", nil, fmt.Errorf("persistence channel not available - cannot process spec content with database")
	}

	// Generate spec ID and create spec record
	specID := persistence.GenerateSpecID()
	spec := &persistence.Spec{
		ID:        specID,
		Content:   specContent,
		CreatedAt: time.Now(),
	}

	// Store spec in database (fire-and-forget)
	dsp.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}

	// Parse the spec content using the existing logic
	parser := &SpecParser{storiesDir: ""} // Empty since we won't use file operations
	requirements, err := parser.parseSpecContent(specContent)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse spec content: %w", err)
	}

	if len(requirements) == 0 {
		return "", nil, fmt.Errorf("no requirements found in spec content")
	}

	// Convert requirements to database stories
	storyIDs := make([]string, 0, len(requirements))

	for i := range requirements {
		req := &requirements[i]
		// Generate story ID (8-char hex)
		storyID, err := persistence.GenerateStoryID()
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate story ID: %w", err)
		}

		// Convert requirement to story
		story := dsp.requirementToStory(storyID, specID, req)

		// Store story in database (fire-and-forget)
		if dsp.persistenceChannel != nil {
			dsp.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpUpsertStory,
				Data:      story,
				Response:  nil, // Fire-and-forget
			}
		}

		storyIDs = append(storyIDs, storyID)
	}

	// Handle dependencies between stories
	dsp.processDependencies(requirements, storyIDs)

	// Mark spec as processed
	spec.ProcessedAt = &[]time.Time{time.Now()}[0]
	if dsp.persistenceChannel != nil {
		dsp.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpsertSpec,
			Data:      spec,
			Response:  nil, // Fire-and-forget
		}
	}

	return specID, storyIDs, nil
}

// requirementToStory converts a parsed requirement to a database story.
func (dsp *DBSpecProcessor) requirementToStory(storyID, specID string, req *Requirement) *persistence.Story {
	// Generate story content in markdown format similar to the original
	content := dsp.generateStoryContent(req)

	return &persistence.Story{
		ID:         storyID,
		SpecID:     specID,
		Title:      req.Title,
		Content:    content,
		Status:     persistence.StatusNew,
		Priority:   req.EstimatedPoints, // Use points as priority
		CreatedAt:  time.Now(),
		TokensUsed: 0,
		CostUSD:    0.0,
	}
}

// generateStoryContent creates markdown content for a story based on the requirement.
func (dsp *DBSpecProcessor) generateStoryContent(req *Requirement) string {
	content := fmt.Sprintf("# %s\n\n", req.Title)

	if req.Description != "" {
		content += fmt.Sprintf("## Description\n%s\n\n", req.Description)
	}

	if len(req.AcceptanceCriteria) > 0 {
		content += "## Acceptance Criteria\n"
		for _, criterion := range req.AcceptanceCriteria {
			content += fmt.Sprintf("- %s\n", criterion)
		}
		content += "\n"
	} else {
		content += "## Acceptance Criteria\n"
		content += "- Implementation completes successfully\n"
		content += "- All tests pass\n"
		content += "- Code follows project conventions\n\n"
	}

	content += fmt.Sprintf("**Estimated Points:** %d\n", req.EstimatedPoints)

	return content
}

// processDependencies handles story dependencies by storing them in the database.
func (dsp *DBSpecProcessor) processDependencies(requirements []Requirement, storyIDs []string) {
	// Create a mapping from requirement index to story ID for dependency resolution
	for i := range requirements {
		req := &requirements[i]
		if len(req.Dependencies) == 0 {
			continue
		}

		storyID := storyIDs[i]

		// For now, we'll implement a simple dependency model where dependencies
		// are based on the order of requirements (earlier requirements are dependencies)
		// This is a simplification - in a real system, you'd want more sophisticated
		// dependency parsing from the spec content
		for j := 0; j < i; j++ {
			// Add dependency from current story to previous story
			// This is a basic implementation - can be enhanced later
			dependsOnStoryID := storyIDs[j]

			dependency := &persistence.StoryDependency{
				StoryID:   storyID,
				DependsOn: dependsOnStoryID,
			}

			if dsp.persistenceChannel != nil {
				dsp.persistenceChannel <- &persistence.Request{
					Operation: persistence.OpAddStoryDependency,
					Data:      dependency,
					Response:  nil, // Fire-and-forget
				}
			}
		}
	}
}

// ProcessSpecFile processes a spec file and stores the results in the database.
// This provides compatibility with the existing file-based interface.
func (dsp *DBSpecProcessor) ProcessSpecFile(specFilePath string) (string, []string, error) {
	// Read the spec file content
	content, err := os.ReadFile(specFilePath)
	if err != nil {
		return "", nil, fmt.Errorf("failed to read spec file %s: %w", specFilePath, err)
	}

	// Process the content using our database-aware processor
	return dsp.ProcessSpecContent(string(content))
}
