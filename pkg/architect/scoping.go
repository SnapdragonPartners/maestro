package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
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

// handleScoping processes the scoping phase (platform detection, bootstrap, spec analysis and story generation).
func (d *Driver) handleScoping(ctx context.Context) (proto.State, error) {
	// State: analyzing specification and generating stories

	// Extract spec file path from the SPEC message.
	specFile := d.getSpecFileFromMessage()
	if specFile == "" {
		return StateError, fmt.Errorf("no spec file path found in SPEC message")
	}

	// Get spec content - either from file or direct content
	var rawSpecContent []byte
	var err error

	// Check if we have direct content from bootstrap
	// Get the stored spec message first
	var contentPayload interface{}
	var hasContent bool
	if specMsgData, exists := d.stateData["spec_message"]; exists {
		if currentSpecMsg, ok := specMsgData.(*proto.AgentMsg); ok {
			contentPayload, hasContent = currentSpecMsg.GetPayload("spec_content")
		}
	}

	if hasContent {
		if contentStr, ok := contentPayload.(string); ok {
			rawSpecContent = []byte(contentStr)
		} else {
			return StateError, fmt.Errorf("spec_content payload is not a string: %T", contentPayload)
		}
	} else {
		// Fallback to file-based spec reading
		rawSpecContent, err = os.ReadFile(specFile)
		if err != nil {
			return StateError, fmt.Errorf("failed to read spec file %s: %w", specFile, err)
		}
	}

	// Spec Analysis - check if spec already parsed.
	var requirements []Requirement
	if _, exists := d.stateData["spec_parsing_completed_at"]; !exists {
		// LLM parsing is required - no fallback.
		if d.llmClient == nil {
			return StateError, fmt.Errorf("LLM client not available - spec analysis requires LLM")
		}

		requirements, err = d.parseSpecWithLLM(ctx, string(rawSpecContent), specFile)
		if err != nil {
			return StateError, fmt.Errorf("LLM spec analysis failed: %w", err)
		}
		d.stateData["parsing_method"] = "llm_primary"

		// Store parsed requirements.
		d.stateData["requirements"] = requirements
		d.stateData["raw_spec_content"] = string(rawSpecContent)
		d.stateData["spec_parsing_completed_at"] = time.Now().UTC()
	} else {
		// Reload requirements from state data.
		if reqData, exists := d.stateData["requirements"]; exists {
			requirements, err = d.convertToRequirements(reqData)
			if err != nil {
				return StateError, fmt.Errorf("failed to convert requirements from state data: %w", err)
			}
		}
	}

	// STEP 4: Story Generation - check if stories already generated.
	if _, exists := d.stateData["stories_generated"]; !exists {
		// Generate stories from LLM-analyzed requirements.
		if d.persistenceChannel != nil {
			// Use database-aware story generation from requirements.
			//nolint:contextcheck // Context is handled appropriately within the retry chain
			specID, storyIDs, err := d.generateStoriesFromRequirements(requirements, string(rawSpecContent))
			if err != nil {
				return StateError, fmt.Errorf("failed to generate stories from requirements: %w", err)
			}

			d.stateData["spec_id"] = specID
			d.stateData["story_ids"] = storyIDs
			d.stateData["stories_generated"] = true
			d.stateData["stories_count"] = len(storyIDs)

			// DevOps story constraints removed - now using container-aware tooling approach
		} else {
			return StateError, fmt.Errorf("persistence channel not available - database storage is required for story generation")
		}
	}

	// Requirement parsing and story generation completed successfully

	return StateDispatching, nil
}

// parseSpecWithLLM uses the LLM to analyze the specification.
func (d *Driver) parseSpecWithLLM(ctx context.Context, rawSpecContent, specFile string) ([]Requirement, error) {
	// Check if renderer is available.
	if d.renderer == nil {
		return nil, fmt.Errorf("template renderer not available - falling back to deterministic parsing")
	}

	// LLM-first approach: send raw content directly to LLM.
	templateData := &templates.TemplateData{
		TaskContent: rawSpecContent,
		Extra: map[string]any{
			"spec_file_path": specFile,
			"mode":           "llm_analysis",
		},
	}

	prompt, err := d.renderer.RenderWithUserInstructions(templates.SpecAnalysisTemplate, templateData, d.workDir, "ARCHITECT")
	if err != nil {
		return nil, fmt.Errorf("failed to render spec analysis template: %w", err)
	}

	// Get LLM response using centralized helper
	llmAnalysis, err := d.callLLMWithTemplate(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM response for spec parsing: %w", err)
	}

	d.stateData["llm_analysis"] = llmAnalysis

	// Parse LLM response to extract requirements.
	return d.parseSpecAnalysisJSON(llmAnalysis)
}

// generateStoriesFromRequirements converts LLM-analyzed requirements into database stories.
func (d *Driver) generateStoriesFromRequirements(requirements []Requirement, specContent string) (string, []string, error) {
	// Generate spec ID and create spec record
	specID := persistence.GenerateSpecID()
	spec := &persistence.Spec{
		ID:        specID,
		Content:   specContent,
		CreatedAt: time.Now(),
	}

	// Store spec in database (fire-and-forget)
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}

	// Process requirements: generate stories and collect dependencies for batch operation
	storyIDs, allDependencies, err := d.processRequirementsCommon(requirements, specID, "")
	if err != nil {
		return "", nil, err
	}

	// Validate story mix before persisting to database
	if err := d.validateStoryMix(); err != nil {
		// Check if this is already a retry to prevent infinite loops
		if d.isRetryAttempt() {
			d.logger.Error("🚨 SCOPING: Story validation failed even after retry - giving up. Error: %v", err)
			return "", nil, fmt.Errorf("story validation failed on retry attempt: %w", err)
		}

		// Clear queue and retry with additional context about container requirements
		d.logger.Warn("⚠️ SCOPING: Initial story generation failed container validation")
		d.logger.Info("🔄 SCOPING: Validation error: %s", err.Error())
		d.logger.Info("🔄 SCOPING: Clearing queue and retrying with container-specific context...")
		d.queue.ClearAll()
		d.markRetryAttempt()

		// Retry story generation with additional context, reusing the existing spec ID
		return d.retryStoryGenerationWithContainerContext(requirements, specContent, specID, err.Error())
	}

	// Complete the processing using common logic
	return d.finalizeStoriesAndDependencies(specID, storyIDs, allDependencies)
}

// requirementToStory converts a LLM-analyzed requirement to a database story.
// Currently unused but kept for potential future use.
//
//nolint:unused
func (d *Driver) requirementToStory(storyID, specID string, req *Requirement) *persistence.Story {
	// Generate rich story content from LLM-analyzed requirement
	content := d.generateRichStoryContent(req)

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
		StoryType:  req.StoryType, // Use story type from requirement
	}
}

// generateRichStoryContent creates detailed markdown content for a story from LLM-analyzed requirement.
// Currently unused but kept for potential future use.
//
//nolint:unused
func (d *Driver) generateRichStoryContent(req *Requirement) string {
	content := fmt.Sprintf("# %s\n\n", req.Title)

	// Add detailed description from LLM analysis
	if req.Description != "" {
		content += fmt.Sprintf("## Description\n%s\n\n", req.Description)
	}

	// Add acceptance criteria from LLM analysis or provide defaults
	if len(req.AcceptanceCriteria) > 0 {
		content += acceptanceCriteriaHeader
		for _, criterion := range req.AcceptanceCriteria {
			content += fmt.Sprintf("- %s\n", criterion)
		}
		content += "\n"
	} else {
		content += acceptanceCriteriaHeader
		content += "- Implementation completes successfully\n"
		content += "- All tests pass\n"
		content += "- Code follows project conventions\n\n"
	}

	// Add dependencies if any
	if len(req.Dependencies) > 0 {
		content += "## Dependencies\n"
		for _, dep := range req.Dependencies {
			content += fmt.Sprintf("- %s\n", dep)
		}
		content += "\n"
	}

	content += fmt.Sprintf("**Estimated Points:** %d\n", req.EstimatedPoints)

	return content
}

// getSpecFileFromMessage extracts the spec file path from the stored SPEC message.
func (d *Driver) getSpecFileFromMessage() string {
	// Get the stored spec message.
	specMsgData, exists := d.stateData["spec_message"]
	if !exists {
		return ""
	}

	// Cast to AgentMsg.
	specMsg, ok := specMsgData.(*proto.AgentMsg)
	if !ok {
		return ""
	}

	// Debug: check payload structure (keys available for debugging if needed)

	// Check if we have spec_content (bootstrap mode) - no file needed
	if _, hasContent := specMsg.GetPayload("spec_content"); hasContent {
		return "<bootstrap-content>" // Return placeholder since actual content is handled elsewhere
	}

	// Extract spec file path from payload - try different keys.
	specFile, exists := specMsg.GetPayload("spec_file")
	if !exists {
		// Try alternative keys.
		specFile, exists = specMsg.GetPayload("file_path")
		if !exists {
			specFile, exists = specMsg.GetPayload("filepath")
			if !exists {
				return ""
			}
		}
	}

	// Convert to string.
	if specFileStr, ok := specFile.(string); ok {
		return specFileStr
	}

	return ""
}

// convertToRequirements converts state data back to Requirements slice.
func (d *Driver) convertToRequirements(data any) ([]Requirement, error) {
	// Handle slice of Requirement structs (from spec parser).
	if reqs, ok := data.([]Requirement); ok {
		return reqs, nil
	}

	// Handle slice of maps (from mock or legacy data).
	if reqMaps, ok := data.([]map[string]any); ok {
		var requirements []Requirement
		for _, reqMap := range reqMaps {
			req := Requirement{}

			if title, ok := reqMap["title"].(string); ok {
				req.Title = title
			}
			if desc, ok := reqMap["description"].(string); ok {
				req.Description = desc
			}
			if points, ok := reqMap["estimated_points"].(int); ok {
				req.EstimatedPoints = points
			}

			// Handle acceptance criteria.
			if criteria, ok := reqMap["acceptance_criteria"]; ok {
				if criteriaSlice, ok := criteria.([]string); ok {
					req.AcceptanceCriteria = criteriaSlice
				}
			}

			requirements = append(requirements, req)
		}
		return requirements, nil
	}

	return nil, fmt.Errorf("unsupported requirements data type: %T", data)
}

// parseSpecAnalysisJSON parses the LLM's JSON response to extract requirements.
func (d *Driver) parseSpecAnalysisJSON(response string) ([]Requirement, error) {
	// Try to extract JSON from the response.
	jsonStart := strings.Index(response, "{")
	jsonEnd := strings.LastIndex(response, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonEnd <= jsonStart {
		return nil, fmt.Errorf("no valid JSON found in LLM response")
	}

	jsonStr := response[jsonStart : jsonEnd+1]

	// Define the expected LLM response structure.
	//nolint:govet // JSON parsing struct, field order must match expected JSON
	var llmResponse struct {
		Analysis string `json:"analysis"`
		//nolint:govet // JSON parsing struct, field order must match expected JSON
		Requirements []struct {
			Title              string   `json:"title"`
			Description        string   `json:"description"`
			AcceptanceCriteria []string `json:"acceptance_criteria"`
			EstimatedPoints    int      `json:"estimated_points"`
			Dependencies       []string `json:"dependencies,omitempty"`
			StoryType          string   `json:"story_type,omitempty"` // Add story type field
		} `json:"requirements"`
		NextAction string `json:"next_action"`
	}

	// Log the JSON response length for debugging without cluttering logs

	if err := json.Unmarshal([]byte(jsonStr), &llmResponse); err != nil {
		// Enhanced error reporting with truncation detection
		baseErr := fmt.Errorf("failed to parse LLM JSON response: %w", err)

		// Check if this might be a truncation issue by comparing response length to token limits
		// Using tiktoken to get accurate token count for O3 model (approximated with GPT-4 encoding)
		responseTokens := utils.CountTokensSimple(response)
		maxTokens := agent.ArchitectMaxTokens // Current MaxTokens limit from LLMClientAdapter

		// If we're within 10% of the token limit, likely truncation
		if float64(responseTokens) >= float64(maxTokens)*0.9 {
			// Likely response was truncated due to token limits
			return nil, fmt.Errorf("JSON parsing failed - likely truncated due to token limit (%d tokens, %.1f%% of %d limit): %w",
				responseTokens, float64(responseTokens)/float64(maxTokens)*100, maxTokens, err)
		}

		// Not a truncation issue, provide standard error with response details
		// Response analysis for debugging

		return nil, baseErr
	}

	// Convert to internal Requirement format.
	requirements := make([]Requirement, 0, len(llmResponse.Requirements))
	for i := range llmResponse.Requirements {
		req := &llmResponse.Requirements[i]
		requirement := Requirement{
			Title:              req.Title,
			Description:        req.Description,
			AcceptanceCriteria: req.AcceptanceCriteria,
			EstimatedPoints:    req.EstimatedPoints,
			Dependencies:       req.Dependencies,
			StoryType:          req.StoryType,
		}

		// Validate and set reasonable defaults.
		if requirement.EstimatedPoints < 1 || requirement.EstimatedPoints > 5 {
			requirement.EstimatedPoints = 2 // Default to medium complexity
		}

		// Log the raw story_type value from JSON for debugging

		// Validate story type and set default if invalid or missing
		if !proto.IsValidStoryType(requirement.StoryType) {
			// Invalid or empty story type - default to app
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
		return nil, fmt.Errorf("no valid requirements extracted from LLM response")
	}

	return requirements, nil
}

// validateStoryMix validates that stories have proper DevOps dependencies when no valid target image exists.
// Returns error if validation fails and stories need to be regenerated with additional context.
func (d *Driver) validateStoryMix() error {
	if config.IsValidTargetImage() {
		// Valid target image exists, any story order is fine
		return nil
	}

	// No valid target image - need to check dependency-ordered stories
	stories := d.queue.GetDependencyOrderedStories()
	if len(stories) == 0 {
		return nil // No stories to validate
	}

	hasDevOps := false
	hasApp := false

	// Check what story types we have
	for _, story := range stories {
		switch story.StoryType {
		case string(proto.StoryTypeDevOps):
			hasDevOps = true
		case string(proto.StoryTypeApp):
			hasApp = true
		}
	}

	// If we have app stories but no valid target image, we need DevOps story first
	if hasApp && !hasDevOps {
		return fmt.Errorf("no valid target container available - need DevOps story to build valid target image before app stories can run")
	}

	// If we have both app and DevOps stories, the first story must be DevOps
	if hasApp && hasDevOps {
		firstStory := stories[0]
		if firstStory.StoryType != string(proto.StoryTypeDevOps) {
			return fmt.Errorf("no valid target container available - first story must be DevOps type to build target image, but found %s story", firstStory.StoryType)
		}
	}

	return nil
}

// retryStoryGenerationWithContainerContext retries story generation with additional context about container requirements.
func (d *Driver) retryStoryGenerationWithContainerContext(requirements []Requirement, specContent, specID, validationError string) (string, []string, error) {
	d.logger.Info("🔄 SCOPING: Starting story generation retry with container-aware context")
	d.logger.Debug("🔄 SCOPING: Original requirements count: %d", len(requirements))

	// Add container context to the story generation prompt
	containerContext := fmt.Sprintf(`

IMPORTANT CONSTRAINT: No valid target container image exists in this project. This means:
- App stories require a containerized development environment to run properly
- You MUST create at least one DevOps story first to build the target container
- The first story (in dependency order) must be a DevOps story that creates a valid container

Previous validation error: %s

Please regenerate the stories ensuring that:
1. At least one DevOps story exists to build the target container environment
2. The first story in dependency order is a DevOps story 
3. DevOps stories handle container setup, build environment, or infrastructure
4. App stories handle application code, features, and business logic within containers`, validationError)

	// Create modified story generation template data with container context
	data := &templates.TemplateData{
		TaskContent: containerContext, // Additional context as task content
		Extra: map[string]any{
			"requirements":     requirements,
			"spec_content":     specContent,
			"retry_context":    "container_validation_failed",
			"validation_error": validationError,
		},
	}

	// Get story generation template
	templateName := templates.StoryGenerationTemplate

	// Call LLM with updated prompt including container requirements
	prompt, err := d.renderer.Render(templateName, data)
	if err != nil {
		return "", nil, fmt.Errorf("failed to render story generation template with container context: %w", err)
	}

	// Get LLM response (use background context for now - could be parameterized later)
	ctx := context.Background()
	messages := []agent.CompletionMessage{
		{Role: agent.RoleUser, Content: prompt},
	}

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: 4000,
	}

	resp, err := d.llmClient.Complete(ctx, req)
	if err != nil {
		return "", nil, fmt.Errorf("LLM failed to regenerate stories with container context: %w", err)
	}

	response := resp.Content

	// Parse the LLM response to extract stories
	stories, err := d.parseStoriesFromLLMResponse(response)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse regenerated stories: %w", err)
	}

	if len(stories) == 0 {
		return "", nil, fmt.Errorf("no valid stories generated in retry attempt")
	}

	// Convert parsed stories to requirements format and re-run the normal flow
	newRequirements := make([]Requirement, 0, len(stories))
	for i := range stories {
		story := &stories[i]
		req := Requirement{
			ID:              story.ID,
			Title:           story.Title,
			Description:     story.Content,
			EstimatedPoints: story.EstimatedPoints,
			Dependencies:    story.DependsOn,
			StoryType:       story.StoryType,
		}
		newRequirements = append(newRequirements, req)
	}

	// Process the new requirements directly (bypass the original function to avoid recursion)
	d.logger.Info("✅ SCOPING: Successfully generated %d retry stories - proceeding with validation", len(newRequirements))
	return d.processRequirementsDirectly(newRequirements, specID)
}

// parseStoriesFromLLMResponse parses story generation response from LLM.
func (d *Driver) parseStoriesFromLLMResponse(response string) ([]StoryResponse, error) {
	// Parse JSON response from LLM (similar to existing parsing logic)
	// This is a simplified version - you may want to enhance based on actual LLM response format

	// Try to extract JSON from response
	var result struct {
		Stories []StoryResponse `json:"stories"`
	}

	if err := json.Unmarshal([]byte(response), &result); err != nil {
		return nil, fmt.Errorf("failed to parse JSON response: %w", err)
	}

	return result.Stories, nil
}

// StoryResponse represents a story in LLM response format.
type StoryResponse struct {
	ID              string   `json:"id"`
	Title           string   `json:"title"`
	Content         string   `json:"content"`
	StoryType       string   `json:"story_type"`
	DependsOn       []string `json:"depends_on"`
	EstimatedPoints int      `json:"est_points"`
}

// isRetryAttempt checks if this is already a retry attempt to prevent infinite loops.
func (d *Driver) isRetryAttempt() bool {
	_, exists := d.stateData["story_generation_retry"]
	return exists
}

// markRetryAttempt marks this as a retry attempt.
func (d *Driver) markRetryAttempt() {
	d.stateData["story_generation_retry"] = true
}

// processRequirementsDirectly processes requirements without validation retry logic.
func (d *Driver) processRequirementsDirectly(requirements []Requirement, specID string) (string, []string, error) {
	// Reuse the existing spec ID - the spec was already persisted in the original attempt

	// Use common processing logic
	storyIDs, allDependencies, err := d.processRequirementsCommon(requirements, specID, "retry")
	if err != nil {
		return "", nil, err
	}

	// Final validation (should pass now with proper container-first stories)
	if err := d.validateStoryMix(); err != nil {
		d.logger.Error("🚨 SCOPING: Retry validation failed: %v", err)
		return "", nil, fmt.Errorf("story validation failed even after retry: %w", err)
	}

	d.logger.Info("✅ SCOPING: Retry validation passed - container-first story ordering achieved")

	// Complete the processing
	return d.finalizeStoriesAndDependencies(specID, storyIDs, allDependencies)
}

// processRequirementsCommon handles the common logic of processing requirements into stories.
func (d *Driver) processRequirementsCommon(requirements []Requirement, specID, logPrefix string) ([]string, []*persistence.StoryDependency, error) {
	storyIDs := make([]string, 0, len(requirements))
	allDependencies := make([]*persistence.StoryDependency, 0)

	// Process each requirement and add to queue
	for i := range requirements {
		req := &requirements[i]
		// Generate unique story ID
		storyID, err := persistence.GenerateStoryID()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to generate story ID: %w", err)
		}

		// Calculate dependencies based on order (simple dependency model)
		var dependencies []string
		if len(req.Dependencies) > 0 {
			// Simple implementation: depend on all previous stories
			for j := 0; j < i; j++ {
				dependencies = append(dependencies, storyIDs[j])
			}
		}

		// Update canonical queue with story and dependencies
		d.queue.AddStory(storyID, specID, req.Title, req.Description, req.StoryType, dependencies, req.EstimatedPoints)
		d.logger.Debug("Added %s story %s to queue with dependencies: %v", logPrefix, storyID, dependencies)

		// Collect dependencies for batch operation
		for _, depID := range dependencies {
			dependency := &persistence.StoryDependency{
				StoryID:   storyID,
				DependsOn: depID,
			}
			allDependencies = append(allDependencies, dependency)
		}

		storyIDs = append(storyIDs, storyID)
	}

	return storyIDs, allDependencies, nil
}

// finalizeStoriesAndDependencies completes the story processing by flushing to database.
func (d *Driver) finalizeStoriesAndDependencies(specID string, storyIDs []string, _ []*persistence.StoryDependency) (string, []string, error) {
	// Get spec for processing if it exists
	var spec *persistence.Spec
	if specData, exists := d.stateData["current_spec"]; exists {
		if s, ok := specData.(*persistence.Spec); ok {
			spec = s
		}
	}
	// Flush canonical queue to database (stories first, then dependencies)
	d.queue.FlushToDatabase()

	// Mark spec as processed if we have one
	if spec != nil {
		spec.ProcessedAt = &[]time.Time{time.Now()}[0]
		d.persistenceChannel <- &persistence.Request{
			Operation: persistence.OpUpsertSpec,
			Data:      spec,
			Response:  nil, // Fire-and-forget
		}
	}

	d.logger.Info("🚀 SCOPING: Successfully processed %d stories with spec ID %s", len(storyIDs), specID)
	return specID, storyIDs, nil
}

// DevOps story constraints have been removed in favor of container-aware tooling approach.
// See docs/DEVOPS_STORIES.md for the new architecture.
