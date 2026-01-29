package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
	"orchestrator/pkg/workspace"
)

// handleSpecReview processes a spec review approval request from PM.
// Uses two toolloops: iterative review (review_complete) and story generation (submit_stories).
func (d *Driver) handleSpecReview(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	d.logger.Info("🔍 Architect reviewing spec from PM")

	// Extract spec markdown from Content (user requirements)
	userSpec := approvalPayload.Content
	if userSpec == "" {
		return nil, fmt.Errorf("user spec not found in approval request Content field")
	}

	// Render infrastructure spec from bootstrap requirements (architect owns technical details)
	// This converts requirement IDs to full technical specification using language pack
	var infrastructureSpec string
	if len(approvalPayload.BootstrapRequirements) > 0 {
		// Convert string IDs back to typed IDs
		reqIDs := make([]workspace.BootstrapRequirementID, 0, len(approvalPayload.BootstrapRequirements))
		for _, id := range approvalPayload.BootstrapRequirements {
			reqIDs = append(reqIDs, workspace.BootstrapRequirementID(id))
		}

		d.logger.Info("📋 Received bootstrap requirements from PM: %v", reqIDs)

		// Render the full technical spec
		rendered, err := RenderBootstrapSpec(reqIDs, d.logger)
		if err != nil {
			d.logger.Warn("Failed to render bootstrap spec: %v (continuing without bootstrap)", err)
		} else {
			infrastructureSpec = rendered
		}
	} else if approvalPayload.InfrastructureSpec != "" {
		// Fallback for legacy payloads with pre-rendered markdown (deprecated)
		d.logger.Warn("Using deprecated InfrastructureSpec field - PM should send BootstrapRequirements instead")
		infrastructureSpec = approvalPayload.InfrastructureSpec
	}

	d.logger.Info("📄 Spec content - user: %d bytes, infrastructure: %d bytes", len(userSpec), len(infrastructureSpec))

	// Get agent-specific context (PM agent)
	agentID := requestMsg.FromAgent
	cm := d.getContextForAgent(agentID)

	// Check if this is a resubmission using state flag (more robust than checking message count)
	specReviewKey := fmt.Sprintf(StateKeyPatternSpecReviewInitialized, agentID)
	_, isResubmission := d.GetStateValue(specReviewKey)

	// Only add spec review prompt for initial submission
	if !isResubmission {
		// Prepare template data for spec review
		templateData := &templates.TemplateData{
			TaskContent: userSpec, // User requirements in main content
			Extra: map[string]any{
				"reason":              approvalPayload.Reason,
				"infrastructure_spec": infrastructureSpec, // Infrastructure requirements (bootstrap) if any
			},
		}

		// Render spec review template (first toolloop phase)
		prompt, err := d.renderer.RenderWithUserInstructions(templates.SpecReviewTemplate, templateData, d.workDir, "ARCHITECT")
		if err != nil {
			return nil, fmt.Errorf("failed to render spec review template: %w", err)
		}

		// Add spec review prompt as user message to start the conversation
		cm.AddMessage("user", prompt)
		d.logger.Info("📝 Added spec review prompt to context (initial submission)")

		// Mark spec review as initialized for this agent
		d.SetStateData(specReviewKey, true)
	} else {
		// Resubmission - include both updated user spec AND infrastructure spec (may have changed)
		var resubmitMsg string
		if infrastructureSpec != "" {
			resubmitMsg = fmt.Sprintf("The PM has revised the specification based on your feedback. Please review the updated version:\n\n## Infrastructure Requirements\n\n%s\n\n## User Requirements\n\n%s", infrastructureSpec, userSpec)
		} else {
			resubmitMsg = fmt.Sprintf("The PM has revised the specification based on your feedback. Please review the updated version:\n\n%s", userSpec)
		}
		cm.AddMessage("user", resubmitMsg)
		d.logger.Info("📝 Added revised spec to context (resubmission)")
	}

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
	terminalTool := reviewCompleteTool

	// Run first toolloop: iterative review with review_complete
	d.logger.Info("🔍 Starting iterative spec review loop")
	reviewOut := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager:     cm,
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      20, // Allow exploration of project files
		MaxTokens:          agent.ArchitectMaxTokens,
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		OnLLMError:         d.makeOnLLMErrorCallback("spec_review"),
	})

	// Handle review outcome
	if reviewOut.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("spec review failed: %w", reviewOut.Err)
	}

	if reviewOut.Signal != tools.SignalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", reviewOut.Signal)
	}

	// Extract review data from ProcessEffect.Data
	effectData, ok := utils.SafeAssert[map[string]any](reviewOut.EffectData)
	if !ok {
		return nil, fmt.Errorf("REVIEW_COMPLETE effect data is not map[string]any: %T", reviewOut.EffectData)
	}

	status := utils.GetMapFieldOr[string](effectData, "status", "")
	feedback := utils.GetMapFieldOr[string](effectData, "feedback", "")

	d.logger.Info("✅ Spec review completed with status: %s", status)

	// Extract review decision
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

	// For story generation, concatenate infrastructure + user specs (stories need complete spec)
	completeSpec := userSpec
	if infrastructureSpec != "" {
		completeSpec = infrastructureSpec + "\n\n" + userSpec
	}

	// Prepare template data for story generation
	storyGenData := &templates.TemplateData{
		TaskContent: completeSpec,
		Extra:       map[string]any{},
	}

	// Render story generation template (second toolloop phase)
	storyGenPrompt, err := d.renderer.RenderWithUserInstructions(templates.SpecAnalysisTemplate, storyGenData, d.workDir, "ARCHITECT")
	if err != nil {
		return nil, fmt.Errorf("failed to render story generation template: %w", err)
	}

	// Add story generation prompt to context
	cm.AddMessage("user", storyGenPrompt)

	// Wrap submit_stories as terminal tool
	storiesTerminal := submitStoriesTool

	// Run second toolloop: single-pass story generation
	storiesOut := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[SubmitStoriesResult]{
		ContextManager:     cm,
		GeneralTools:       nil, // No general tools - just generate and submit
		TerminalTool:       storiesTerminal,
		MaxIterations:      5,    // Should complete quickly
		SingleTurn:         true, // Enforce single-turn completion
		MaxTokens:          agent.ArchitectMaxTokens,
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		OnLLMError:         d.makeOnLLMErrorCallback("story_generation"),
	})

	// Handle story generation outcome
	if storiesOut.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("story generation failed: %w", storiesOut.Err)
	}

	if storiesOut.Signal != tools.SignalStoriesSubmitted {
		return nil, fmt.Errorf("expected STORIES_SUBMITTED signal, got: %s", storiesOut.Signal)
	}

	// Extract stories data from ProcessEffect.Data
	effectData, ok = storiesOut.EffectData.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("STORIES_SUBMITTED effect data is not map[string]any: %T", storiesOut.EffectData)
	}

	d.logger.Info("✅ Stories generated successfully")

	// Load stories into queue (pass effectData and complete spec instead of out.Value)
	specID, storyIDs, loadErr := d.loadStoriesFromSubmitResultData(ctx, completeSpec, effectData)
	if loadErr != nil {
		return nil, fmt.Errorf("failed to load stories after approval: %w", loadErr)
	}

	feedback = fmt.Sprintf("Spec approved - %d stories generated successfully (spec_id: %s)", len(storyIDs), specID)
	d.logger.Info("📦 Loaded %d stories into queue", len(storyIDs))

	// Mark that we now own this spec and should transition to DISPATCHING
	d.SetStateData(StateKeySpecApprovedLoad, true)

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
