package coder

import (
	"context"
	"fmt"
	"os"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/coder/claude"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	claudetemplates "orchestrator/pkg/templates/claude"
	"orchestrator/pkg/utils"
)

// handleClaudeCodePlanning processes the PLANNING state using Claude Code subprocess.
func (c *Coder) handleClaudeCodePlanning(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", string(StatePlanning)+"-claudecode")

	// Get required data from state
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	storyTitle := "Story " + storyID // Simple title from ID

	if taskContent == "" {
		return proto.StateError, false, logx.Errorf("no task content available for planning")
	}

	// Check for pending container switch (from SignalContainerSwitch)
	containerSwitchTarget := utils.GetStateValueOr[string](sm, KeyContainerSwitchTarget, "")
	if containerSwitchTarget != "" {
		c.logger.Info("🐳 Processing pending container switch to: %s", containerSwitchTarget)

		// Clear the pending switch target
		sm.SetStateData(KeyContainerSwitchTarget, nil)

		// Perform the container switch
		if err := c.performContainerSwitch(ctx, containerSwitchTarget); err != nil {
			c.logger.Error("Container switch failed: %v", err)
			// Don't fail the story - fall back to current container
			c.logger.Warn("Continuing with current container after switch failure")
		} else {
			c.logger.Info("✅ Container switch to %s completed successfully", containerSwitchTarget)
		}

		// Reinitialize tool provider to use new container executor
		c.planningToolProvider = nil // Force recreation
	}

	// Check for existing session ID (for resume) and resume input (feedback from plan review)
	// Note: We reuse KeyCodingSessionID and KeyResumeInput since planning and coding never run simultaneously
	existingSessionID := utils.GetStateValueOr[string](sm, KeyCodingSessionID, "")
	resumeInput := utils.GetStateValueOr[string](sm, KeyResumeInput, "")
	shouldResume := existingSessionID != "" && resumeInput != ""

	// Ensure planning tool provider is initialized
	// Uses Claude Code-specific provider that includes container_switch (handled via SignalContainerSwitch)
	if c.planningToolProvider == nil {
		storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))
		c.planningToolProvider = c.createClaudeCodePlanningToolProvider(storyType)
		c.logger.Debug("Created Claude Code planning ToolProvider for story type: %s", storyType)
	}

	// Create runner with tool provider for MCP integration
	runner := claude.NewRunner(c.longRunningExecutor, c.containerName, c.planningToolProvider, c.logger)

	// Build run options
	opts := claude.DefaultRunOptions()
	opts.Mode = claude.ModePlanning
	opts.WorkDir = "/workspace"
	opts.Model = config.GetEffectiveCoderModel()
	opts.EnvVars = map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv(config.EnvAnthropicAPIKey),
	}

	if shouldResume {
		// Resume existing session with feedback from plan review
		opts.SessionID = existingSessionID
		opts.Resume = true
		opts.ResumeInput = resumeInput

		// Clear the resume input after using it
		sm.SetStateData(KeyResumeInput, nil)

		c.logger.Info("🔄 Resuming Claude Code planning session %s for story %s", existingSessionID, storyID)
	} else {
		// New session - render full prompts
		renderer, err := claudetemplates.NewRenderer()
		if err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to create claude template renderer")
		}

		// Build template data
		knowledgePack := utils.GetStateValueOr[string](sm, string(stateDataKeyKnowledgePack), "")
		templateData := claudetemplates.TemplateData{
			StoryID:       storyID,
			StoryTitle:    storyTitle,
			StoryContent:  taskContent,
			WorkspacePath: c.workDir,
			KnowledgePack: knowledgePack,
		}

		// Check if we're resuming from a question (inject Q&A context - legacy path)
		if qaData, exists := sm.GetStateValue(KeyLastQA); exists && qaData != nil {
			if qaMap, ok := qaData.(map[string]string); ok {
				templateData.LastQA = &claudetemplates.QAPair{
					Question: qaMap["question"],
					Answer:   qaMap["answer"],
				}
				// Clear the Q&A after using it
				sm.SetStateData(KeyLastQA, nil)
			}
		}

		// Render system prompt
		var systemPrompt string
		systemPrompt, err = renderer.RenderPlanningPrompt(&templateData)
		if err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to render planning system prompt")
		}

		// Render initial input
		initialInput := renderer.RenderPlanningInput(&templateData)

		opts.SystemPrompt = systemPrompt
		opts.InitialInput = initialInput

		c.logger.Info("🧑‍💻 Starting Claude Code planning for story %s", storyID)
	}

	// Run Claude Code
	result, err := runner.RunWithInactivityTimeout(ctx, &opts)
	if err != nil {
		c.logger.Error("Claude Code planning failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "Claude Code execution failed")
	}

	// Store session ID for potential resume (always store, even on error, for debugging)
	if result.SessionID != "" {
		sm.SetStateData(KeyCodingSessionID, result.SessionID)
	}

	// Track if Claude Code was upgraded in-place (container image needs rebuild)
	if result.ContainerUpgradeNeeded {
		c.containerUpgradeNeeded = true
	}

	// Process result based on signal
	return c.processClaudeCodePlanningResult(sm, &result, storyID)
}

// processClaudeCodePlanningResult handles the result from Claude Code planning.
func (c *Coder) processClaudeCodePlanningResult(sm *agent.BaseStateMachine, result *claude.Result, _ string) (proto.State, bool, error) {
	c.logger.Info("Claude Code planning completed: signal=%s duration=%s responses=%d",
		result.Signal, result.Duration, result.ResponseCount)

	switch result.Signal {
	case claude.SignalPlanComplete:
		// Plan submitted - store and transition to PLAN_REVIEW
		if result.Plan == "" {
			return proto.StateError, false, logx.Errorf("Claude Code submitted empty plan")
		}

		sm.SetStateData(string(stateDataKeyPlan), result.Plan)
		sm.SetStateData(KeyPlanningCompletedAt, time.Now().UTC())

		// Create approval request for PLAN_REVIEW
		c.pendingApprovalRequest = &ApprovalRequest{
			ID:      proto.GenerateApprovalID(),
			Content: result.Plan,
			Reason:  "Claude Code generated plan requires approval",
			Type:    proto.ApprovalTypePlan,
		}

		c.logger.Info("✅ Claude Code plan submitted (%d chars), transitioning to PLAN_REVIEW", len(result.Plan))
		return StatePlanReview, false, nil

	case claude.SignalQuestion:
		// Need to ask architect a question
		if result.Question == nil {
			return proto.StateError, false, logx.Errorf("Claude Code signaled question but provided no question")
		}

		// Store question data in the format expected by handleQuestion
		questionData := map[string]any{
			"question": result.Question.Question,
			"context":  result.Question.Context,
			"origin":   string(StatePlanning),
		}
		sm.SetStateData(KeyPendingQuestion, questionData)
		c.logger.Info("❓ Claude Code needs clarification: %s", result.Question.Question)
		return StateQuestion, false, nil

	case claude.SignalTimeout:
		c.logger.Warn("⏰ Claude Code planning timed out after %s", result.Duration)
		return proto.StateError, false, logx.Errorf("Claude Code planning timed out after %s with %d responses", result.Duration, result.ResponseCount)

	case claude.SignalInactivity:
		c.logger.Warn("⏰ Claude Code planning stalled (no output for inactivity timeout)")
		return proto.StateError, false, logx.Errorf("Claude Code planning stalled - no output received (%d responses before stall)", result.ResponseCount)

	case claude.SignalError:
		errMsg := "unknown error"
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		return proto.StateError, false, logx.Errorf("Claude Code planning error: %s", errMsg)

	case claude.SignalContainerSwitch:
		// Claude Code requested a container switch during planning
		if result.ContainerSwitchTarget == "" {
			return proto.StateError, false, logx.Errorf("container_switch called without target container name")
		}

		c.logger.Info("🐳 Claude Code requested container switch to: %s", result.ContainerSwitchTarget)

		// Store target container for the switch (will be processed on next PLANNING entry)
		sm.SetStateData(KeyContainerSwitchTarget, result.ContainerSwitchTarget)

		// Set resume input to inform Claude Code about the container switch
		resumeMsg := fmt.Sprintf("Container switch completed. You are now running in container '%s'. Continue with your planning work.",
			result.ContainerSwitchTarget)
		sm.SetStateData(KeyResumeInput, resumeMsg)

		// Return to PLANNING state - the container switch will happen at state entry
		return StatePlanning, false, nil

	default:
		return proto.StateError, false, logx.Errorf("unexpected Claude Code signal: %s", result.Signal)
	}
}
