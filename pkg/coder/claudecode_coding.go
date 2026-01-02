package coder

import (
	"context"
	"os"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/coder/claude"
	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	claudetemplates "orchestrator/pkg/templates/claude"
	"orchestrator/pkg/utils"
)

// handleClaudeCodeCoding processes the CODING state using Claude Code subprocess.
func (c *Coder) handleClaudeCodeCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", string(StateCoding)+"-claudecode")

	// Get required data from state
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	storyTitle := "Story " + storyID // Simple title from ID
	plan := utils.GetStateValueOr[string](sm, string(stateDataKeyPlan), "")

	if plan == "" {
		return proto.StateError, false, logx.Errorf("no approved plan available for coding")
	}

	// Check for existing session ID (for resume) and resume input (feedback)
	existingSessionID := utils.GetStateValueOr[string](sm, KeyCodingSessionID, "")
	resumeInput := utils.GetStateValueOr[string](sm, KeyResumeInput, "")
	shouldResume := existingSessionID != "" && resumeInput != ""

	// Ensure coding tool provider is initialized
	// Use Claude Code-specific provider that excludes container_switch to prevent session destruction
	if c.codingToolProvider == nil {
		storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))
		c.codingToolProvider = c.createClaudeCodeCodingToolProvider(storyType)
		c.logger.Debug("Created Claude Code coding ToolProvider for story type: %s (container_switch excluded)", storyType)
	}

	// Create runner with tool provider for MCP integration
	runner := claude.NewRunner(c.longRunningExecutor, c.containerName, c.codingToolProvider, c.logger)

	// Build run options
	opts := claude.DefaultRunOptions()
	opts.Mode = claude.ModeCoding
	opts.WorkDir = "/workspace"
	opts.Model = config.GetEffectiveCoderModel()
	opts.EnvVars = map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv(config.EnvAnthropicAPIKey),
	}

	var renderer *claudetemplates.Renderer

	if shouldResume {
		// Resume existing session with feedback
		opts.SessionID = existingSessionID
		opts.Resume = true
		opts.ResumeInput = resumeInput

		// Clear the resume input after using it
		sm.SetStateData(KeyResumeInput, nil)

		c.logger.Info("🔄 Resuming Claude Code session %s for story %s", existingSessionID, storyID)
	} else {
		// New session - render full prompts
		var err error
		renderer, err = claudetemplates.NewRenderer()
		if err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to create claude template renderer")
		}

		// Build template data
		templateData := claudetemplates.TemplateData{
			StoryID:       storyID,
			StoryTitle:    storyTitle,
			Plan:          plan,
			WorkspacePath: c.workDir,
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
		systemPrompt, err = renderer.RenderCodingPrompt(&templateData)
		if err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to render coding system prompt")
		}

		// Render initial input
		initialInput := renderer.RenderCodingInput(&templateData)

		opts.SystemPrompt = systemPrompt
		opts.InitialInput = initialInput

		c.logger.Info("🧑‍💻 Starting Claude Code coding for story %s", storyID)
	}

	// Run Claude Code
	result, err := runner.RunWithInactivityTimeout(ctx, &opts)
	if err != nil {
		c.logger.Error("Claude Code coding failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "Claude Code execution failed")
	}

	// Store session ID for potential resume (always store, even on error, for debugging)
	if result.SessionID != "" {
		sm.SetStateData(KeyCodingSessionID, result.SessionID)
	}

	// Process result based on signal
	return c.processClaudeCodeCodingResult(sm, &result, storyID)
}

// processClaudeCodeCodingResult handles the result from Claude Code coding.
func (c *Coder) processClaudeCodeCodingResult(sm *agent.BaseStateMachine, result *claude.Result, _ string) (proto.State, bool, error) {
	c.logger.Info("Claude Code coding completed: signal=%s duration=%s responses=%d",
		result.Signal, result.Duration, result.ResponseCount)

	switch result.Signal {
	case claude.SignalDone:
		// Coding complete - store summary and transition to TESTING
		sm.SetStateData(KeyCodeGenerated, true)
		sm.SetStateData(KeyCodingCompletedAt, time.Now().UTC())

		if result.Summary != "" {
			sm.SetStateData(KeyCompletionDetails, result.Summary)
		}

		c.logger.Info("✅ Claude Code coding complete, transitioning to TESTING")
		return StateTesting, false, nil

	case claude.SignalQuestion:
		// Need to ask architect a question
		if result.Question == nil {
			return proto.StateError, false, logx.Errorf("Claude Code signaled question but provided no question")
		}

		// Store question data in the format expected by handleQuestion
		questionData := map[string]any{
			"question": result.Question.Question,
			"context":  result.Question.Context,
			"origin":   string(StateCoding),
		}
		sm.SetStateData(KeyPendingQuestion, questionData)
		c.logger.Info("❓ Claude Code needs clarification: %s", result.Question.Question)
		return StateQuestion, false, nil

	case claude.SignalTimeout:
		c.logger.Warn("⏰ Claude Code coding timed out after %s", result.Duration)
		return StateBudgetReview, false, nil

	case claude.SignalInactivity:
		c.logger.Warn("⏰ Claude Code coding stalled (no output)")
		return StateBudgetReview, false, nil

	case claude.SignalError:
		errMsg := "unknown error"
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		return proto.StateError, false, logx.Errorf("Claude Code coding error: %s", errMsg)

	case claude.SignalPlanComplete:
		// Unexpected - we're in coding mode, not planning
		c.logger.Warn("Claude Code sent plan_complete signal during coding mode")
		return proto.StateError, false, logx.Errorf("unexpected plan_complete signal in coding mode")

	default:
		return proto.StateError, false, logx.Errorf("unexpected Claude Code signal: %s", result.Signal)
	}
}

// isClaudeCodeMode returns true if the coder is configured to use Claude Code mode
// AND Claude Code is actually available in the current container.
// If Claude Code is configured but not available, logs a warning and falls back to standard mode.
func (c *Coder) isClaudeCodeMode(ctx context.Context) bool {
	cfg, err := config.GetConfig()
	if err != nil {
		return false
	}

	// Not configured for Claude Code mode
	if cfg.Agents.CoderMode != config.CoderModeClaudeCode {
		return false
	}

	// Check if Claude Code is available (cache this check per-story)
	if !c.claudeCodeAvailabilityChecked {
		c.claudeCodeAvailable = c.checkClaudeCodeAvailable(ctx)
		c.claudeCodeAvailabilityChecked = true

		if !c.claudeCodeAvailable {
			c.logger.Warn("⚠️ Claude Code not found in container %s. Will run in standard mode to install it and continue using Claude Code after installation is complete.",
				c.containerName)
		}
	}

	return c.claudeCodeAvailable
}

// checkClaudeCodeAvailable probes the container to see if Claude Code is installed.
func (c *Coder) checkClaudeCodeAvailable(ctx context.Context) bool {
	if c.longRunningExecutor == nil || c.containerName == "" {
		c.logger.Debug("No container available for Claude Code check")
		return false
	}

	// Use a timeout context for the check
	checkCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	// Run "claude --version" in the container
	result, err := c.longRunningExecutor.Run(checkCtx, []string{"claude", "--version"}, &exec.Opts{})
	if err != nil {
		c.logger.Debug("Claude Code availability check failed: %v", err)
		return false
	}

	if result.ExitCode != 0 {
		c.logger.Debug("Claude Code not available (exit code %d): %s", result.ExitCode, result.Stderr)
		return false
	}

	c.logger.Info("✅ Claude Code available: %s", result.Stdout)
	return true
}
