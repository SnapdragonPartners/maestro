package architect

import (
	"fmt"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// generatePlanPrompt creates a prompt for architect's plan review.
func (d *Driver) generatePlanPrompt(requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) string {
	storyID := requestMsg.Metadata["story_id"]
	planContent := approvalPayload.Content

	// Get story information from queue
	var storyTitle, taskContent, knowledgePack string
	if storyID != "" && d.queue != nil {
		if story, exists := d.queue.GetStory(storyID); exists {
			storyTitle = story.Title
			taskContent = story.Content
			// Get knowledge pack if available
			if story.KnowledgePack != "" {
				knowledgePack = story.KnowledgePack
			}
		}
	}

	// Fallback values
	if storyTitle == "" {
		storyTitle = "Unknown Story"
	}
	if taskContent == "" {
		taskContent = "Task content not available"
	}

	// Create template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"StoryTitle":    storyTitle,
			"TaskContent":   taskContent,
			"PlanContent":   planContent,
			"KnowledgePack": knowledgePack,
		},
	}

	// Check if we have a renderer
	if d.renderer == nil {
		// Fallback to simple text if no renderer available
		return fmt.Sprintf(`Plan Review Request

Story: %s
Task: %s

Submitted Plan:
%s

Please review and provide decision: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, taskContent, planContent)
	}

	// Render template
	prompt, err := d.renderer.Render(templates.PlanReviewArchitectTemplate, templateData)
	if err != nil {
		// Fallback to simple text
		return fmt.Sprintf(`Plan Review Request

Story: %s
Task: %s

Submitted Plan:
%s

Please review and provide decision: APPROVED, NEEDS_CHANGES, or REJECTED with specific feedback.`,
			storyTitle, taskContent, planContent)
	}

	return prompt
}
