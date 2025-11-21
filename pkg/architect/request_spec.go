package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// handleSpecReview processes a spec review approval request from PM.
// Uses spec review tools (spec_feedback, submit_stories) for iterative review.
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

	// Reset context for new spec review
	templateName := fmt.Sprintf("spec-review-%s", requestMsg.ID)
	d.contextManager.ResetForNewTemplate(templateName, prompt)

	// Add initial user message to start the conversation properly
	// This prevents FlushUserBuffer from adding a fallback message
	d.contextManager.AddMessage("user", "Please analyze this specification.")

	// Get spec review tools (spec_feedback, submit_stories)
	specReviewTools := d.getSpecReviewTools()

	// Run toolloop for spec review with type-safe result extraction
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SpecReviewResult]{
		ContextManager: d.contextManager,
		ToolProvider:   newListToolProvider(specReviewTools),
		CheckTerminal:  d.checkTerminalTools,
		ExtractResult:  ExtractSpecReview,
		MaxIterations:  20, // Increased for complex spec review workflows
		MaxTokens:      agent.ArchitectMaxTokens,
		AgentID:        d.GetAgentID(),
	})

	// Handle outcome
	if out.Kind != toolloop.OutcomeSuccess {
		return nil, fmt.Errorf("failed to get LLM response for spec review: %w", out.Err)
	}

	// Process tool signal and create RESULT message
	var approved bool
	var feedback string

	switch out.Signal {
	case signalSpecFeedbackSent:
		// Architect requested changes via spec_feedback tool
		approved = out.Value.Approved // Should be false
		feedback = out.Value.Feedback
		d.logger.Info("📝 Architect requested spec changes: %s", feedback)

	case signalSubmitStoriesComplete:
		// Architect approved spec and generated stories via submit_stories tool
		approved = out.Value.Approved // Should be true
		d.logger.Info("✅ Architect approved spec and generated stories")

		// Load stories into queue from submit_stories result
		specID, storyIDs, err := d.loadStoriesFromSubmitResult(ctx, specMarkdown)
		if err != nil {
			return nil, fmt.Errorf("failed to load stories after approval: %w", err)
		}

		feedback = fmt.Sprintf("Spec approved - %d stories generated successfully (spec_id: %s)", len(storyIDs), specID)
		d.logger.Info("📦 Loaded %d stories into queue", len(storyIDs))

		// Mark that we now own this spec and should transition to DISPATCHING
		// This is checked by handleRequest to determine next state
		d.SetStateData("spec_approved_and_loaded", true)

	default:
		return nil, fmt.Errorf("unexpected signal from spec review: %s", out.Signal)
	}

	// Create RESPONSE message with approval result
	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID

	// Determine approval status
	var status proto.ApprovalStatus
	if approved {
		status = proto.ApprovalStatusApproved
	} else {
		status = proto.ApprovalStatusNeedsChanges
	}

	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.Metadata["approval_id"],
		Type:       proto.ApprovalTypeSpec,
		Status:     status,
		Feedback:   feedback,
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: response.Timestamp,
	}

	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))
	response.SetMetadata("approval_id", approvalResult.ID)

	d.logger.Info("✅ Spec review complete - sending RESULT to PM (status=%v)", status)
	return response, nil
}
