package architect

import (
	"fmt"

	"orchestrator/pkg/coder"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// generateBudgetPrompt creates an enhanced prompt for budget review requests using templates.
func (d *Driver) generateBudgetPrompt(requestMsg *proto.AgentMsg) string {
	// Extract data from typed payload
	typedPayload := requestMsg.GetTypedPayload()
	if typedPayload == nil {
		d.logger.Warn("Budget review request missing typed payload, using defaults")
		return "Budget review request missing data"
	}

	payloadData, err := typedPayload.ExtractGeneric()
	if err != nil {
		d.logger.Warn("Failed to extract budget review payload: %v", err)
		return "Budget review request data extraction failed"
	}

	// Extract fields with safe type assertions and defaults
	storyID, _ := payloadData["story_id"].(string)
	origin, _ := payloadData["origin"].(string)
	loops, _ := payloadData["loops"].(int)
	maxLoops, _ := payloadData["max_loops"].(int)
	contextSize, _ := payloadData["context_size"].(int)
	phaseTokens, _ := payloadData["phase_tokens"].(int)
	phaseCostUSD, _ := payloadData["phase_cost_usd"].(float64)
	totalLLMCalls, _ := payloadData["total_llm_calls"].(int)
	recentActivity, _ := payloadData["recent_activity"].(string)
	issuePattern, _ := payloadData["issue_pattern"].(string)

	// Get story information from queue
	var storyTitle, storyType, specContent, approvedPlan string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			storyType = story.StoryType
			// For CODING state reviews, include the approved plan for context
			if origin == string(coder.StateCoding) && story.ApprovedPlan != "" {
				approvedPlan = story.ApprovedPlan
			}
			// TODO: For now, we add a placeholder for spec content
			// In a future enhancement, we could fetch the actual spec content
			// using the story.SpecID and the persistence channel
			specContent = fmt.Sprintf("Spec ID: %s (full context available on request)", story.SpecID)
		}
	}

	// Fallback values
	if storyTitle == "" {
		storyTitle = "Unknown Story"
	}
	if storyType == "" {
		storyType = defaultStoryType // default
	}
	if recentActivity == "" {
		recentActivity = "No recent activity data available"
	}
	if issuePattern == "" {
		issuePattern = "No issue pattern detected"
	}
	if specContent == "" {
		specContent = "Spec context not available"
	}

	// Select template based on current state
	var templateName templates.StateTemplate
	if origin == string(coder.StatePlanning) {
		templateName = templates.BudgetReviewPlanningTemplate
	} else {
		templateName = templates.BudgetReviewCodingTemplate
	}

	// Create template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"StoryID":        storyID,
			"StoryTitle":     storyTitle,
			"StoryType":      storyType,
			"CurrentState":   origin,
			"Loops":          loops,
			"MaxLoops":       maxLoops,
			"ContextSize":    contextSize,
			"PhaseTokens":    phaseTokens,
			"PhaseCostUSD":   phaseCostUSD,
			"TotalLLMCalls":  totalLLMCalls,
			"RecentActivity": recentActivity,
			"IssuePattern":   issuePattern,
			"SpecContent":    specContent,
			"ApprovedPlan":   approvedPlan, // Include approved plan for CODING state context
		},
	}

	// Check if we have a renderer
	if d.renderer == nil {
		// Fallback to simple text if no renderer available
		return fmt.Sprintf(`Budget Review Request

Story: %s (ID: %s)
Type: %s
Current State: %s
Budget Exceeded: %d/%d iterations

Recent Activity:
%s

Issue Analysis:
%s

Please review and provide guidance: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, storyID, storyType, origin, loops, maxLoops, recentActivity, issuePattern)
	}

	// Render template
	prompt, err := d.renderer.Render(templateName, templateData)
	if err != nil {
		// Fallback to simple text
		return fmt.Sprintf(`Budget Review Request

Story: %s (ID: %s)
Type: %s
Current State: %s
Budget Exceeded: %d/%d iterations

Recent Activity:
%s

Issue Analysis:
%s

Please review and provide guidance: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, storyID, storyType, origin, loops, maxLoops, recentActivity, issuePattern)
	}

	return prompt
}
