package architect

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// handleHotfixRequest processes a HOTFIX request from PM. The requirements are already
// structured (from PM's submit_stories call with hotfix=true). Architect validates
// dependencies and dispatches to the hotfix coder. Unlike spec reviews which may
// iterate, hotfix handling is single-pass: extract requirements, validate dependencies
// are complete, convert to stories, and return approval/needs_changes to PM.
func (d *Driver) handleHotfixRequest(ctx context.Context, requestMsg *proto.AgentMsg) (*proto.AgentMsg, error) {
	// Check for context cancellation
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("hotfix request cancelled: %w", ctx.Err())
	default:
	}

	// Extract hotfix payload from typed message
	typedPayload := requestMsg.GetTypedPayload()
	if typedPayload == nil {
		return nil, fmt.Errorf("hotfix request message missing typed payload")
	}

	hotfixPayload, err := typedPayload.ExtractHotfixRequest()
	if err != nil {
		return nil, fmt.Errorf("failed to extract hotfix request: %w", err)
	}

	d.logger.Info("🔧 Processing hotfix request from PM: %d requirements for platform %s",
		len(hotfixPayload.Requirements), hotfixPayload.Platform)

	// Validate requirements exist
	if len(hotfixPayload.Requirements) == 0 {
		return d.buildHotfixNeedsChangesResponse(requestMsg,
			"No requirements provided in hotfix request. Please specify what needs to be changed.")
	}

	// Validate each requirement and check dependencies
	for i, req := range hotfixPayload.Requirements {
		reqMap, ok := req.(map[string]any)
		if !ok {
			return d.buildHotfixNeedsChangesResponse(requestMsg,
				fmt.Sprintf("Requirement %d is not a valid object", i+1))
		}

		// Check required fields
		title, _ := reqMap["title"].(string)
		if title == "" {
			return d.buildHotfixNeedsChangesResponse(requestMsg,
				fmt.Sprintf("Requirement %d is missing a title", i+1))
		}

		// Validate dependencies are complete
		if deps, ok := reqMap["dependencies"].([]any); ok && len(deps) > 0 {
			for _, dep := range deps {
				depStr, ok := dep.(string)
				if !ok {
					continue
				}

				// Check if this dependency exists and is complete
				if d.queue != nil {
					// Look up story by title (dependencies reference titles)
					story := d.queue.FindStoryByTitle(depStr)
					if story == nil {
						return d.buildHotfixNeedsChangesResponse(requestMsg,
							fmt.Sprintf("Hotfix requirement '%s' depends on unknown story: '%s'. Hotfixes should have minimal or no dependencies.", title, depStr))
					}
					if story.GetStatus() != StatusDone {
						return d.buildHotfixNeedsChangesResponse(requestMsg,
							fmt.Sprintf("Hotfix requirement '%s' depends on incomplete story '%s' (status: %s). Please wait for it to complete or remove this dependency.", title, depStr, story.GetStatus()))
					}
				}
			}
		}
	}

	// All validations passed - convert requirements to stories and dispatch to hotfix queue
	storiesCreated := 0
	for _, req := range hotfixPayload.Requirements {
		reqMap, ok := req.(map[string]any)
		if !ok {
			continue // Already validated above, this shouldn't happen
		}

		title, _ := reqMap["title"].(string)
		description, _ := reqMap["description"].(string)
		storyType, _ := reqMap["story_type"].(string)
		if storyType == "" {
			storyType = "app" // Default to app stories
		}

		// Extract dependencies
		var dependencies []string
		if deps, ok := reqMap["dependencies"].([]any); ok {
			for _, dep := range deps {
				if depStr, ok := dep.(string); ok {
					dependencies = append(dependencies, depStr)
				}
			}
		}

		// Extract acceptance criteria
		var acceptanceCriteria []string
		if criteria, ok := reqMap["acceptance_criteria"].([]any); ok {
			for _, c := range criteria {
				if cStr, ok := c.(string); ok {
					acceptanceCriteria = append(acceptanceCriteria, cStr)
				}
			}
		}

		// Build story content from description and acceptance criteria
		content := description
		if len(acceptanceCriteria) > 0 {
			content += "\n\n## Acceptance Criteria\n"
			for _, ac := range acceptanceCriteria {
				content += fmt.Sprintf("- %s\n", ac)
			}
		}

		// Create the hotfix story using embedded persistence.Story
		story := &QueuedStory{
			Story: persistence.Story{
				ID:        fmt.Sprintf("hotfix-%d", time.Now().UnixNano()),
				SpecID:    "hotfix", // Special spec ID for hotfixes
				Title:     title,
				Content:   content,
				Priority:  100, // High priority for hotfixes
				DependsOn: dependencies,
				StoryType: storyType,
				Express:   true, // Hotfixes default to express (skip planning)
				IsHotfix:  true, // Mark as hotfix for routing
			},
		}

		// Add to the hotfix queue
		if d.queue != nil {
			if err := d.queue.AddHotfixStory(story); err != nil {
				d.logger.Error("Failed to add hotfix story: %v", err)
				return d.buildHotfixNeedsChangesResponse(requestMsg,
					fmt.Sprintf("Failed to queue hotfix story '%s': %v", title, err))
			}
			d.logger.Info("✅ Added hotfix story: %s (ID: %s)", title, story.ID)
			storiesCreated++
		} else {
			d.logger.Error("Queue is nil, cannot add hotfix story")
			return d.buildHotfixNeedsChangesResponse(requestMsg,
				"Internal error: story queue not available")
		}
	}

	d.logger.Info("🎉 Hotfix request processed: %d stories created and queued for hotfix coder", storiesCreated)

	// Set state to trigger DISPATCHING for hotfix stories
	d.SetStateData(StateKeyHotfixQueued, true)
	d.SetStateData(StateKeyHotfixCount, storiesCreated)

	// Return approval response to PM
	return d.buildHotfixApprovalResponse(requestMsg, storiesCreated)
}

// buildHotfixNeedsChangesResponse builds a needs_changes response for a hotfix request.
func (d *Driver) buildHotfixNeedsChangesResponse(requestMsg *proto.AgentMsg, reason string) (*proto.AgentMsg, error) {
	d.logger.Info("🔧 Hotfix needs changes: %s", reason)

	// Create approval result with NEEDS_CHANGES status
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.ID,
		Type:       proto.ApprovalTypeHotfix,
		Status:     proto.ApprovalStatusNeedsChanges,
		Feedback:   reason,
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	return response, nil
}

// buildHotfixApprovalResponse builds an approval response for a successful hotfix request.
func (d *Driver) buildHotfixApprovalResponse(requestMsg *proto.AgentMsg, storiesCreated int) (*proto.AgentMsg, error) {
	d.logger.Info("✅ Hotfix approved: %d stories queued", storiesCreated)

	// Create approval result with APPROVED status
	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.ID,
		Type:       proto.ApprovalTypeHotfix,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   fmt.Sprintf("Hotfix approved and queued for immediate processing. %d stories created.", storiesCreated),
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	// Create response message
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	return response, nil
}
