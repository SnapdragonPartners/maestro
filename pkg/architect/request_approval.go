package architect

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleIterativeApproval processes approval requests with iterative code exploration.
func (d *Driver) handleIterativeApproval(ctx context.Context, requestMsg *proto.AgentMsg, approvalPayload *proto.ApprovalRequestPayload) (*proto.AgentMsg, error) {
	approvalType := approvalPayload.ApprovalType
	storyID := proto.GetStoryID(requestMsg)

	d.logger.Info("🔍 Starting iterative approval for %s (story: %s)", approvalType, storyID)

	// Store story_id in state data for tool logging
	d.SetStateData(StateKeyCurrentStoryID, storyID)

	// Extract coder ID from request (sender)
	coderID := requestMsg.FromAgent
	if coderID == "" {
		return nil, fmt.Errorf("approval request message missing sender (FromAgent)")
	}

	// Get agent-specific context
	cm := d.getContextForAgent(coderID)

	// Create tool provider rooted at coder's workspace with review_complete and get_diff
	toolProvider := d.createReviewToolProviderForCoder(coderID, true)
	d.logger.Debug("Created review tool provider for coder %s at /mnt/coders/%s (with get_diff)", coderID, coderID)

	// Build prompt based on approval type
	var prompt string
	switch approvalType {
	case proto.ApprovalTypeCode:
		prompt = d.generateCodePrompt(requestMsg, approvalPayload, coderID, toolProvider)
	case proto.ApprovalTypeCompletion:
		prompt = d.generateCompletionPrompt(requestMsg, approvalPayload, coderID, toolProvider)
	default:
		return nil, fmt.Errorf("unsupported iterative approval type: %s", approvalType)
	}

	// Add approval prompt as user message to preserve context continuity
	cm.AddMessage("architect-approval-prompt", prompt)

	// Get review_complete tool and wrap as terminal tool
	reviewCompleteTool, err := toolProvider.Get(tools.ToolReviewComplete)
	if err != nil {
		return nil, logx.Wrap(err, "failed to get review_complete tool")
	}
	terminalTool := reviewCompleteTool

	// Get all general tools (everything except review_complete)
	allTools := toolProvider.List()
	generalTools := make([]tools.Tool, 0, len(allTools)-1)
	//nolint:gocritic // ToolMeta is 80 bytes but value semantics preferred here
	for _, meta := range allTools {
		if meta.Name != tools.ToolReviewComplete {
			tool, err := toolProvider.Get(meta.Name)
			if err != nil {
				return nil, logx.Wrap(err, fmt.Sprintf("failed to get tool %s", meta.Name))
			}
			generalTools = append(generalTools, tool)
		}
	}

	// Run toolloop for iterative approval with type-safe result extraction
	out := toolloop.Run(d.toolLoop, ctx, &toolloop.Config[ReviewCompleteResult]{
		ContextManager: cm, // Use agent-specific context
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("approval_%s", storyID),
			SoftLimit: 8,  // Warn at 8 iterations
			HardLimit: 16, // Escalate at 16 iterations
			OnSoftLimit: func(count int) {
				d.logger.Warn("⚠️  Approval iteration soft limit reached (%d iterations) for story %s", count, storyID)
			},
			OnHardLimit: func(_ context.Context, key string, count int) error {
				d.logger.Error("❌ Approval iteration hard limit reached (%d iterations) for story %s - escalating", count, storyID)
				d.logger.Info("Escalation key: %s", key)
				// Set escalation state data for state machine
				d.SetStateData(StateKeyEscalationRequestID, requestMsg.ID)
				d.SetStateData(StateKeyEscalationStoryID, storyID)
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
		MaxIterations:      20, // Allow multiple inspection iterations
		MaxTokens:          agent.ArchitectMaxTokens,
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
		StoryID:            storyID,
	})

	// Handle outcome
	if out.Kind == toolloop.OutcomeIterationLimit {
		// OnHardLimit already stored escalation state data
		d.logger.Info("📊 Iteration limit reached (%d iterations), returning escalation sentinel", out.Iteration)
		return nil, ErrEscalationTriggered
	}
	if out.Kind != toolloop.OutcomeProcessEffect {
		return nil, fmt.Errorf("iterative approval failed: %w", out.Err)
	}

	if out.Signal != tools.SignalReviewComplete {
		return nil, fmt.Errorf("expected REVIEW_COMPLETE signal, got: %s", out.Signal)
	}

	// Extract review data from ProcessEffect.Data
	effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
	if !ok {
		return nil, fmt.Errorf("REVIEW_COMPLETE effect data is not map[string]any: %T", out.EffectData)
	}

	status := utils.GetMapFieldOr[string](effectData, "status", "")
	feedback := utils.GetMapFieldOr[string](effectData, "feedback", "")

	d.logger.Info("✅ Architect completed iterative review with status: %s", status)

	// Clean up state data
	d.SetStateData(StateKeyCurrentStoryID, nil)

	// Build and return approval response
	return d.buildApprovalResponseFromReviewComplete(ctx, requestMsg, approvalPayload, status, feedback)
}
