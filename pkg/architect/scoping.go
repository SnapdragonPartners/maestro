package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/utils"
)

// handleScoping processes the scoping phase (platform detection, bootstrap, spec analysis and story generation).
func (d *Driver) handleScoping(ctx context.Context) (proto.State, error) {
	// State: analyzing specification and generating stories

	// Extract spec file path from the SPEC message.
	specFile := d.getSpecFileFromMessage()
	if specFile == "" {
		return StateError, fmt.Errorf("no spec file path found in SPEC message")
	}

	d.logger.Info("🏗️ Architect reading spec file: %s", specFile)

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
			d.logger.Info("🏗️ Using direct spec content from bootstrap (%d bytes)", len(rawSpecContent))
		} else {
			return StateError, fmt.Errorf("spec_content payload is not a string: %T", contentPayload)
		}
	} else {
		// Fallback to file-based spec reading
		d.logger.Info("🏗️ Reading spec from file: %s", specFile)
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
			specID, storyIDs, err := d.generateStoriesFromRequirements(requirements, string(rawSpecContent))
			if err != nil {
				return StateError, fmt.Errorf("failed to generate stories from requirements: %w", err)
			}

			d.stateData["spec_id"] = specID
			d.stateData["story_ids"] = storyIDs
			d.stateData["stories_generated"] = true
			d.stateData["stories_count"] = len(storyIDs)

			d.logger.Info("🏗️ Story generation completed: %d stories generated and stored in database (spec ID: %s)", len(storyIDs), specID)
		} else {
			return StateError, fmt.Errorf("persistence channel not available - database storage is required for story generation")
		}
	}

	d.logger.Info("🏗️ Scoping completed using %s method, extracted %d requirements and generated %d stories",
		d.stateData["parsing_method"], len(requirements), d.stateData["stories_count"])

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

	// Get LLM response.
	response, err := d.llmClient.GenerateResponse(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("failed to get LLM response for spec parsing: %w", err)
	}

	// Add LLM response to context.
	d.contextManager.AddMessage("assistant", response)
	d.stateData["llm_analysis"] = response

	// Parse LLM response to extract requirements.
	return d.parseSpecAnalysisJSON(response)
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

	// Convert requirements to database stories
	storyIDs := make([]string, 0, len(requirements))

	for i := range requirements {
		req := &requirements[i]
		// Generate story ID (8-char hex)
		storyID, err := persistence.GenerateStoryID()
		if err != nil {
			return "", nil, fmt.Errorf("failed to generate story ID: %w", err)
		}

		// Add story to in-memory queue (canonical state)
		d.queue.AddStory(storyID, specID, req.Title, req.StoryType, req.Dependencies, req.EstimatedPoints)

		storyIDs = append(storyIDs, storyID)
	}

	// Flush in-memory queue to database for persistence/logging first
	d.queue.FlushToDatabase()

	// Handle dependencies between stories AFTER stories are in database
	d.processDependencies(requirements, storyIDs)

	// Mark spec as processed
	spec.ProcessedAt = &[]time.Time{time.Now()}[0]
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil, // Fire-and-forget
	}

	return specID, storyIDs, nil
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

// processDependencies handles story dependencies by storing them in the database.
func (d *Driver) processDependencies(requirements []Requirement, storyIDs []string) {
	// For now, implement a simple dependency model where dependencies
	// are based on the order of requirements (earlier requirements are dependencies)
	// This could be enhanced to parse actual dependencies from LLM analysis
	for i := range requirements {
		req := &requirements[i]
		if len(req.Dependencies) == 0 {
			continue
		}

		storyID := storyIDs[i]

		// Simple implementation: add dependency to previous story
		for j := 0; j < i; j++ {
			dependsOnStoryID := storyIDs[j]

			dependency := &persistence.StoryDependency{
				StoryID:   storyID,
				DependsOn: dependsOnStoryID,
			}

			d.persistenceChannel <- &persistence.Request{
				Operation: persistence.OpAddStoryDependency,
				Data:      dependency,
				Response:  nil, // Fire-and-forget
			}
		}
	}
}

// getSpecFileFromMessage extracts the spec file path from the stored SPEC message.
func (d *Driver) getSpecFileFromMessage() string {
	// Get the stored spec message.
	specMsgData, exists := d.stateData["spec_message"]
	if !exists {
		d.logger.Error("🏗️ No spec_message found in state data")
		return ""
	}

	// Cast to AgentMsg.
	specMsg, ok := specMsgData.(*proto.AgentMsg)
	if !ok {
		d.logger.Error("🏗️ spec_message is not an AgentMsg: %T", specMsgData)
		return ""
	}

	// Debug: log all payload keys.
	payloadKeys := make([]string, 0)
	for key := range specMsg.Payload {
		payloadKeys = append(payloadKeys, key)
	}
	d.logger.Info("🏗️ SPEC message payload keys: %v", payloadKeys)

	// Check if we have spec_content (bootstrap mode) - no file needed
	if _, hasContent := specMsg.GetPayload("spec_content"); hasContent {
		d.logger.Info("🏗️ Bootstrap mode detected - using spec_content")
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
				d.logger.Error("🏗️ No spec file path found in payload with keys: %v", payloadKeys)
				return ""
			}
		}
	}

	// Convert to string.
	if specFileStr, ok := specFile.(string); ok {
		d.logger.Info("🏗️ Found spec file path: %s", specFileStr)
		return specFileStr
	}

	d.logger.Error("🏗️ Spec file path is not a string: %T = %v", specFile, specFile)
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
	d.logger.Info("🏗️ Parsing LLM JSON response (%d chars)", len(jsonStr))

	if err := json.Unmarshal([]byte(jsonStr), &llmResponse); err != nil {
		// Enhanced error reporting with truncation detection
		baseErr := fmt.Errorf("failed to parse LLM JSON response: %w", err)

		// Check if this might be a truncation issue by comparing response length to token limits
		// Using tiktoken to get accurate token count for O3 model (approximated with GPT-4 encoding)
		responseTokens := utils.CountTokensSimple(response)
		maxTokens := agent.ArchitectMaxTokens // Current MaxTokens limit from LLMClientAdapter

		// If we're within 10% of the token limit, likely truncation
		if float64(responseTokens) >= float64(maxTokens)*0.9 {
			d.logger.Error("🏗️ JSON parsing failed - likely due to response truncation")
			d.logger.Error("🏗️ Response tokens: %d, MaxTokens limit: %d (%.1f%% of limit)",
				responseTokens, maxTokens, float64(responseTokens)/float64(maxTokens)*100)
			d.logger.Error("🏗️ Consider increasing MaxTokens in LLMClientAdapter")
			d.logger.Error("🏗️ Response length: %d characters", len(response))
			if len(response) > 1000 {
				d.logger.Error("🏗️ Response end (last 500 chars): ...%s", response[len(response)-500:])
			} else {
				d.logger.Error("🏗️ Full response: %s", response)
			}
			return nil, fmt.Errorf("JSON parsing failed - likely truncated due to token limit (%d tokens, %.1f%% of %d limit): %w",
				responseTokens, float64(responseTokens)/float64(maxTokens)*100, maxTokens, err)
		}

		// Not a truncation issue, provide standard error with response details
		d.logger.Error("🏗️ JSON parsing failed - response tokens: %d, limit: %d (%.1f%%)",
			responseTokens, maxTokens, float64(responseTokens)/float64(maxTokens)*100)
		if len(response) > 2000 {
			d.logger.Error("🏗️ Response preview (first 1000 chars): %s...", response[:1000])
			d.logger.Error("🏗️ Response preview (last 1000 chars): ...%s", response[len(response)-1000:])
		} else {
			d.logger.Error("🏗️ Full response: %s", response)
		}

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
		d.logger.Info("🏗️ Raw story_type from JSON for '%s': '%s' (empty: %t)", requirement.Title, req.StoryType, req.StoryType == "")

		// Validate story type and set default if invalid or missing
		if !proto.IsValidStoryType(requirement.StoryType) {
			if requirement.StoryType == "" {
				d.logger.Info("🏗️ Story '%s' has EMPTY story_type, defaulting to 'app'", requirement.Title)
			} else {
				d.logger.Info("🏗️ Story '%s' has INVALID story_type '%s', defaulting to 'app'", requirement.Title, requirement.StoryType)
			}
			requirement.StoryType = string(proto.StoryTypeApp) // Default to app when uncertain
		} else {
			d.logger.Info("🏗️ Story '%s' classified as '%s'", requirement.Title, requirement.StoryType)
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
