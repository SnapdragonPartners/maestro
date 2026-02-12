package architect

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleHotfixRequest processes a HOTFIX request from PM. The requirements are
// structurally validated (fields, dependencies), then reviewed by the architect LLM
// with read tools for codebase inspection. On approval, requirements are converted
// to stories and queued for the hotfix coder.
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

	// --- Phase 1: Structural validation (fast, no LLM) ---

	if len(hotfixPayload.Requirements) == 0 {
		return d.buildHotfixNeedsChangesResponse(requestMsg,
			"No requirements provided in hotfix request. Please specify what needs to be changed.")
	}

	for i, req := range hotfixPayload.Requirements {
		reqMap, ok := req.(map[string]any)
		if !ok {
			return d.buildHotfixNeedsChangesResponse(requestMsg,
				fmt.Sprintf("Requirement %d is not a valid object", i+1))
		}

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

				if d.queue != nil {
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

	// --- Phase 2: LLM review with read tools ---

	reviewDecision, reviewFeedback, err := d.runHotfixReview(ctx, requestMsg, hotfixPayload)
	if err != nil {
		d.logger.Error("Hotfix LLM review failed: %v", err)
		// On LLM failure, fall through to approve (don't block hotfixes on LLM issues)
		d.logger.Warn("Proceeding with hotfix approval despite review failure")
		reviewDecision = reviewStatusApproved
		reviewFeedback = "Review skipped due to LLM error"
	}

	// Handle non-approval decisions
	if reviewDecision != reviewStatusApproved {
		d.logger.Info("📝 Architect hotfix review: %s - %s", reviewDecision, reviewFeedback)
		return d.buildHotfixNeedsChangesResponse(requestMsg, reviewFeedback)
	}

	d.logger.Info("✅ Architect approved hotfix requirements")

	// --- Phase 3: Convert requirements to stories and queue ---

	storiesCreated := 0
	for _, req := range hotfixPayload.Requirements {
		reqMap, ok := req.(map[string]any)
		if !ok {
			continue
		}

		title, _ := reqMap["title"].(string)
		description, _ := reqMap["description"].(string)
		storyType, _ := reqMap["story_type"].(string)
		if storyType == "" {
			storyType = "app"
		}

		var dependencies []string
		if deps, ok := reqMap["dependencies"].([]any); ok {
			for _, dep := range deps {
				if depStr, ok := dep.(string); ok {
					dependencies = append(dependencies, depStr)
				}
			}
		}

		var acceptanceCriteria []string
		if criteria, ok := reqMap["acceptance_criteria"].([]any); ok {
			for _, c := range criteria {
				if cStr, ok := c.(string); ok {
					acceptanceCriteria = append(acceptanceCriteria, cStr)
				}
			}
		}

		content := description
		if len(acceptanceCriteria) > 0 {
			content += "\n\n## Acceptance Criteria\n"
			for _, ac := range acceptanceCriteria {
				content += fmt.Sprintf("- %s\n", ac)
			}
		}

		// Append review feedback as context for the coder
		if reviewFeedback != "" {
			content += "\n\n## Architect Review Notes\n" + reviewFeedback
		}

		story := &QueuedStory{
			Story: persistence.Story{
				ID:        fmt.Sprintf("hotfix-%d", time.Now().UnixNano()),
				SpecID:    "hotfix",
				Title:     title,
				Content:   content,
				Priority:  100,
				DependsOn: dependencies,
				StoryType: storyType,
				Express:   true,
				IsHotfix:  true,
			},
		}

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

	d.SetStateData(StateKeyHotfixQueued, true)
	d.SetStateData(StateKeyHotfixCount, storiesCreated)

	return d.buildHotfixApprovalResponse(requestMsg, storiesCreated)
}

// runHotfixReview runs an LLM toolloop to review hotfix requirements.
// Returns (decision, feedback, error). Decision is reviewStatusApproved, "NEEDS_CHANGES", or "REJECTED".
func (d *Driver) runHotfixReview(ctx context.Context, requestMsg *proto.AgentMsg, hotfixPayload *proto.HotfixRequestPayload) (string, string, error) {
	// Serialize requirements for the template
	reqJSON, err := json.MarshalIndent(hotfixPayload.Requirements, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("failed to serialize requirements: %w", err)
	}

	// Get agent-specific context
	agentID := requestMsg.FromAgent
	cm := d.getContextForAgent(agentID)

	// Render hotfix review template
	templateData := &templates.TemplateData{
		TaskContent: string(reqJSON),
	}

	prompt, err := d.renderer.RenderWithUserInstructions(templates.HotfixReviewTemplate, templateData, d.workDir, "ARCHITECT")
	if err != nil {
		return "", "", fmt.Errorf("failed to render hotfix review template: %w", err)
	}

	cm.AddMessage("user", prompt)

	// Set up tools: review_complete (terminal) + read tools (general)
	reviewTools := d.getHotfixReviewTools()

	var terminalTool tools.Tool
	var generalTools []tools.Tool

	for _, tool := range reviewTools {
		if tool.Name() == tools.ToolReviewComplete {
			terminalTool = tool
		} else {
			generalTools = append(generalTools, tool)
		}
	}

	if terminalTool == nil {
		return "", "", fmt.Errorf("review_complete tool not found")
	}

	// Run toolloop
	d.logger.Info("🔍 Starting hotfix requirements review loop")
	reviewOut := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager:     cm,
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      10, // Hotfix reviews should be quick
		MaxTokens:          agent.ArchitectMaxTokens,
		Temperature:        config.GetTemperature(config.TempRoleArchitect),
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		OnLLMError:         d.makeOnLLMErrorCallback("hotfix_review"),
	})

	if reviewOut.Kind != toolloop.OutcomeProcessEffect {
		return "", "", fmt.Errorf("hotfix review toolloop failed: %w", reviewOut.Err)
	}

	if reviewOut.Signal != tools.SignalReviewComplete {
		return "", "", fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", reviewOut.Signal)
	}

	// Extract review decision
	effectData, ok := utils.SafeAssert[map[string]any](reviewOut.EffectData)
	if !ok {
		return "", "", fmt.Errorf("REVIEW_COMPLETE effect data is not map[string]any: %T", reviewOut.EffectData)
	}

	status := utils.GetMapFieldOr[string](effectData, "status", "")
	feedback := utils.GetMapFieldOr[string](effectData, "feedback", "")

	return status, feedback, nil
}

// getHotfixReviewTools returns tools for hotfix review: review_complete + read tools.
func (d *Driver) getHotfixReviewTools() []tools.Tool {
	toolsList := []tools.Tool{
		tools.NewReviewCompleteTool(),
	}

	if d.executor != nil {
		toolsList = append(toolsList,
			tools.NewReadFileTool(d.executor, "/mnt/architect", 1048576),
			tools.NewListFilesTool(d.executor, "/mnt/architect", 1000),
		)
	} else {
		d.logger.Warn("No executor available for read tools in hotfix review")
	}

	return toolsList
}

// buildHotfixNeedsChangesResponse builds a needs_changes response for a hotfix request.
func (d *Driver) buildHotfixNeedsChangesResponse(requestMsg *proto.AgentMsg, reason string) (*proto.AgentMsg, error) {
	d.logger.Info("🔧 Hotfix needs changes: %s", reason)

	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.ID,
		Type:       proto.ApprovalTypeHotfix,
		Status:     proto.ApprovalStatusNeedsChanges,
		Feedback:   reason,
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	return response, nil
}

// buildHotfixApprovalResponse builds an approval response for a successful hotfix request.
func (d *Driver) buildHotfixApprovalResponse(requestMsg *proto.AgentMsg, storiesCreated int) (*proto.AgentMsg, error) {
	d.logger.Info("✅ Hotfix approved: %d stories queued", storiesCreated)

	approvalResult := &proto.ApprovalResult{
		ID:         proto.GenerateApprovalID(),
		RequestID:  requestMsg.ID,
		Type:       proto.ApprovalTypeHotfix,
		Status:     proto.ApprovalStatusApproved,
		Feedback:   fmt.Sprintf("Hotfix approved and queued for immediate processing. %d stories created.", storiesCreated),
		ReviewedBy: d.GetAgentID(),
		ReviewedAt: time.Now().UTC(),
	}

	response := proto.NewAgentMsg(proto.MsgTypeRESPONSE, d.GetAgentID(), requestMsg.FromAgent)
	response.ParentMsgID = requestMsg.ID
	response.SetTypedPayload(proto.NewApprovalResponsePayload(approvalResult))

	return response, nil
}
