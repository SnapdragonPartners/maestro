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

// handleScoping processes the scoping phase using a clean linear flow.
// 1. Create and persist spec record immediately
// 2. Check for container context and add if needed
// 3. Parse spec with LLM to get detailed requirements
// 4. Convert requirements directly to stories with rich content
// 5. Flush stories to database.
func (d *Driver) handleScoping(ctx context.Context) (proto.State, error) {
	// Extract spec file path from the SPEC message
	specFile := d.getSpecFileFromMessage()
	if specFile == "" {
		return StateError, fmt.Errorf("no spec file path found in SPEC message")
	}

	// Get spec content - either from file or direct content
	var rawSpecContent []byte
	var err error

	// Check if we have direct content from bootstrap
	var hasContent bool
	if specMsgData, exists := d.stateData["spec_message"]; exists {
		if currentSpecMsg, ok := specMsgData.(*proto.AgentMsg); ok {
			// Extract spec content from typed payload
			if typedPayload := currentSpecMsg.GetTypedPayload(); typedPayload != nil {
				if payloadData, extractErr := typedPayload.ExtractGeneric(); extractErr == nil {
					if contentStr, ok := payloadData["spec_content"].(string); ok {
						rawSpecContent = []byte(contentStr)
						hasContent = true
					}
				}
			}
		}
	}

	if !hasContent {
		// Fallback to file-based spec reading
		rawSpecContent, err = os.ReadFile(specFile)
		if err != nil {
			return StateError, fmt.Errorf("failed to read spec file %s: %w", specFile, err)
		}
	}

	// 1. Create and persist spec record immediately (for recovery)
	specID := persistence.GenerateSpecID()
	spec := &persistence.Spec{
		ID:        specID,
		Content:   string(rawSpecContent),
		CreatedAt: time.Now(),
	}
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil,
	}

	// 2. Check for container context and add if needed (with retry enhancement)
	containerContext := ""
	retryCount := 0
	if retryData, exists := d.stateData["container_retry_count"]; exists {
		if count, ok := retryData.(int); ok {
			retryCount = count
		}
	}

	if !config.IsValidTargetImage() {
		if retryCount == 0 {
			// First attempt - standard container guidance
			containerContext = `

IMPORTANT CONSTRAINT: No valid target container image exists in this project. This means:
- App stories require a containerized development environment to run properly
- You MUST create at least one DevOps story first to build the target container
- The first story (in dependency order) must be a DevOps story that creates a valid container

Please ensure that:
1. At least one DevOps story exists to build the target container environment
2. The first story in dependency order is a DevOps story 
3. DevOps stories handle container setup, build environment, or infrastructure
4. App stories handle application code, features, and business logic within containers`
		} else {
			// Retry attempt - ENHANCED guidance
			containerContext = `

CRITICAL REQUIREMENT: Container Environment Setup - RETRY WITH ENHANCED GUIDANCE

This project currently has NO VALID TARGET CONTAINER and your previous response did not include any DevOps stories.

YOU MUST CREATE AT LEAST ONE DEVOPS STORY or the system cannot function. This is MANDATORY.

DEVOPS STORY REQUIREMENTS:
1. Story type MUST be "devops" (not "app")
2. Must handle container setup, Dockerfile creation, or build environment
3. Must be first in dependency order
4. Example titles: "Set up development container", "Configure build environment", "Create Dockerfile"

APP STORIES CANNOT RUN without a container environment. Every app story needs a DevOps story to run first.

REQUIRED: Your response must include at least one DevOps story for container setup.`
		}
	}

	// 3. Parse spec with LLM to get detailed requirements
	if d.llmClient == nil {
		return StateError, fmt.Errorf("LLM client not available - spec analysis requires LLM")
	}

	requirements, err := d.parseSpecWithLLM(ctx, string(rawSpecContent)+containerContext, specFile)
	if err != nil {
		return StateError, fmt.Errorf("LLM spec analysis failed: %w", err)
	}

	// 4. Convert requirements directly to stories with rich content
	storyIDs := make([]string, 0, len(requirements))
	for i := range requirements {
		req := &requirements[i]
		// Generate unique story ID
		storyID, err := persistence.GenerateStoryID()
		if err != nil {
			return StateError, fmt.Errorf("failed to generate story ID: %w", err)
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
			d.logger.Info("🔄 Container validation triggered retry - clearing queue and restarting scoping")
			d.queue.ClearAll()
			return StateScoping, nil // Return to SCOPING state for retry
		}
		return StateError, fmt.Errorf("container validation failed: %w", err)
	}

	// 6. Flush stories to database
	d.queue.FlushToDatabase()

	// Mark spec as processed
	spec.ProcessedAt = &[]time.Time{time.Now()}[0]
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpUpsertSpec,
		Data:      spec,
		Response:  nil,
	}

	// Store completion state
	d.stateData["spec_id"] = specID
	d.stateData["story_ids"] = storyIDs
	d.stateData["stories_generated"] = true
	d.stateData["stories_count"] = len(storyIDs)

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

	// Extract from typed payload
	typedPayload := specMsg.GetTypedPayload()
	if typedPayload == nil {
		return ""
	}

	payloadData, err := typedPayload.ExtractGeneric()
	if err != nil {
		return ""
	}

	// Check if we have spec_content (bootstrap mode) - no file needed
	if _, hasContent := payloadData["spec_content"]; hasContent {
		return "<bootstrap-content>" // Return placeholder since actual content is handled elsewhere
	}

	// Extract spec file path from payload - try different keys.
	if specFileStr, ok := payloadData["spec_file"].(string); ok {
		return specFileStr
	}
	if specFileStr, ok := payloadData["file_path"].(string); ok {
		return specFileStr
	}
	if specFileStr, ok := payloadData["filepath"].(string); ok {
		return specFileStr
	}

	return ""
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

// All story generation now uses the clean linear flow in handleScoping()

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
