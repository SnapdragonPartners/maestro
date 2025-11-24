package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
)

// handleSpecReview processes a spec review approval request from PM.
// Uses two toolloops: iterative review (review_complete) and story generation (submit_stories).
func (d *Driver) handleSpecReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	d.logger.Info("🔍 Architect reviewing spec from PM")

	// Extract spec markdown from Content (the critical field for approval requests)
	specMarkdown := approvalPayload.Content
	if specMarkdown == "" {
		return nil, fmt.Errorf("spec markdown not found in approval request Content field")
	}

	d.logger.Info("📄 Spec content length: %d bytes", len(specMarkdown))

	// Prepare template data for spec review
	templateData := &templates.TemplateData{
		TaskContent: specMarkdown,
		Extra: map[string]any{
			"mode":   "spec_review",
			"reason": approvalPayload.Reason,
		},
	}

	// Render spec review template
	prompt, err := d.renderer.RenderWithUserInstructions(templates.SpecAnalysisTemplate, templateData, d.workDir, "ARCHITECT")
	if err != nil {
		return nil, fmt.Errorf("failed to render spec review template: %w", err)
	}

	// Get agent-specific context (PM agent)
	agentID := requestMsg.FromAgent
	cm := d.getContextForAgent(agentID)

	// Add spec review prompt as user message to preserve context continuity
	cm.AddMessage("architect-spec-review-prompt", prompt)

	// Add initial user message to start the conversation properly
	// This prevents FlushUserBuffer from adding a fallback message
	cm.AddMessage("user", "Please analyze this specification.")

	// FIRST TOOLLOOP: Iterative spec review with review_complete
	// Get review_complete tool and general tools (read_file, list_files)
	specReviewTools := d.getSpecReviewTools()

	var reviewCompleteTool tools.Tool
	var generalTools []tools.Tool

	for _, tool := range specReviewTools {
		if tool.Name() == tools.ToolReviewComplete {
			reviewCompleteTool = tool
		} else if tool.Name() != tools.ToolSubmitStories {
			// Include general tools (read_file, list_files), exclude terminal tools
			generalTools = append(generalTools, tool)
		}
	}

	if reviewCompleteTool == nil {
		return nil, fmt.Errorf("review_complete tool not found in spec review tools")
	}

	// Wrap review_complete as terminal tool
	terminalTool := NewReviewCompleteTool(reviewCompleteTool)

	// Run first toolloop: iterative review with review_complete
	d.logger.Info("🔍 Starting iterative spec review loop")
	reviewOut := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager: cm,
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
		MaxIterations:  20, // Allow exploration of project files
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        d.GetAgentID(),
		DebugLogging:   true,
	})

	// Handle review outcome
	if reviewOut.Kind != toolloop.OutcomeSuccess {
		return nil, fmt.Errorf("spec review failed: %w", reviewOut.Err)
	}

	if reviewOut.Signal != signalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", reviewOut.Signal)
	}

	d.logger.Info("✅ Spec review completed with status: %s", reviewOut.Value.Status)

	// Extract review decision
	status := reviewOut.Value.Status
	feedback := reviewOut.Value.Feedback
	approved := (status == "APPROVED")

	// If NOT approved, return feedback immediately (no story generation)
	if !approved {
		d.logger.Info("📝 Architect requested spec changes: %s", feedback)

		response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
		response.ParentMsgID = requestMsg.ID

		var approvalStatus proto.ApprovalStatus
		if status == "REJECTED" {
			approvalStatus = proto.ApprovalStatusRejected
		} else {
			approvalStatus = proto.ApprovalStatusNeedsChanges
		}

		approvalResult := &proto.ApprovalResult{
			ID:         proto.GenerateApprovalID(),
			RequestID:  requestMsg.Metadata["approval_id"],
			Type:       proto.ApprovalTypeSpec,
			Status:     approvalStatus,
			Feedback:   feedback,
			ReviewedBy: d.GetAgentID(),
			ReviewedAt: response.Timestamp,
		}

		response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))
		response.SetMetadata("approval_id", approvalResult.ID)

		return response, nil
	}

	// SECOND TOOLLOOP: Single-pass story generation with submit_stories
	d.logger.Info("📝 Spec approved, starting story generation loop")

	// Get submit_stories tool
	var submitStoriesTool tools.Tool
	for _, tool := range specReviewTools {
		if tool.Name() == tools.ToolSubmitStories {
			submitStoriesTool = tool
			break
		}
	}

	if submitStoriesTool == nil {
		return nil, fmt.Errorf("submit_stories tool not found")
	}

	// Create story generation prompt
	storyGenPrompt := fmt.Sprintf(`You have approved the specification. Now generate implementation stories using the submit_stories tool.

The specification you approved:
%s

Generate clear, actionable stories that break down the implementation into manageable tasks. Each story should have:
- Clear title and description
- Specific acceptance criteria
- Appropriate dependencies

Call submit_stories with the generated stories.`, specMarkdown)

	// Add story generation prompt to context
	cm.AddMessage("architect-story-generation", storyGenPrompt)

	// Wrap submit_stories as terminal tool
	storiesTerminal := NewSubmitStoriesTool(submitStoriesTool)

	// Run second toolloop: single-pass story generation
	storiesOut := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitStoriesResult]{
		ContextManager: cm,
		GeneralTools:   nil, // No general tools - just generate and submit
		TerminalTool:   storiesTerminal,
		MaxIterations:  5,    // Should complete quickly
		SingleTurn:     true, // Enforce single-turn completion
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        d.GetAgentID(),
		DebugLogging:   true,
	})

	// Handle story generation outcome
	if storiesOut.Kind != toolloop.OutcomeSuccess {
		return nil, fmt.Errorf("story generation failed: %w", storiesOut.Err)
	}

	if storiesOut.Signal != signalSubmitStoriesComplete {
		return nil, fmt.Errorf("expected SUBMIT_STORIES signal, got: %s", storiesOut.Signal)
	}

	d.logger.Info("✅ Stories generated successfully")

	// Load stories into queue
	specID, storyIDs, err := d.loadStoriesFromSubmitResult(ctx, specMarkdown)
	if err != nil {
		return nil, fmt.Errorf("failed to load stories after approval: %w", err)
	}

	feedback = fmt.Sprintf("Spec approved - %d stories generated successfully (spec_id: %s)", len(storyIDs), specID)
	d.logger.Info("📦 Loaded %d stories into queue", len(storyIDs))

	// Mark that we now own this spec and should transition to DISPATCHING
	d.SetStateData("spec_approved_and_loaded", true)

	// Create RESPONSE message with approval result
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.Metadata["approval_id"],
		Type:       proto.ApprovalTypeSpec,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   feedback,
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: response.Timestamp,
	}

	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))
	response.SetMetadata("approval_id", approvalResult.ID)

	d.logger.Info("✅ Spec approved and stories generated - sending RESULT to PM")
	return response, nil
}
