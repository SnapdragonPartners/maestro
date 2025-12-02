package coder

import (
	"context"
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

	// Create template renderer
	renderer, err := claudetemplates.NewRenderer()
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

	// Render system prompt
	systemPrompt, err := renderer.RenderCodingPrompt(&templateData)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to render coding system prompt")
	}

	// Render initial input
	initialInput := renderer.RenderCodingInput(&templateData)

	// Get config for API key and model
	cfg, err := config.GetConfig()
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to get config")
	}

	// Create runner
	runner := claude.NewRunner(c.longRunningExecutor, c.containerName, c.logger)

	// Build run options
	opts := claude.DefaultRunOptions()
	opts.Mode = claude.ModeCoding
	opts.WorkDir = "/workspace"
	opts.Model = cfg.Agents.CoderModel
	opts.SystemPrompt = systemPrompt
	opts.InitialInput = initialInput
	opts.EnvVars = map[string]string{
		"ANTHROPIC_API_KEY": os.Getenv(config.EnvAnthropicAPIKey),
	}

	c.logger.Info("🧑‍💻 Starting Claude Code coding for story %s", storyID)

	// Run Claude Code
	result, err := runner.RunWithInactivityTimeout(ctx, &opts)
	if err != nil {
		c.logger.Error("Claude Code coding failed: %v", err)
		return proto.StateError, false, logx.Wrap(err, "Claude Code execution failed")
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

		sm.SetStateData(KeyPendingQuestion, result.Question.Question)
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

// isClaudeCodeMode returns true if the coder is configured to use Claude Code mode.
func (c *Coder) isClaudeCodeMode() bool {
	cfg, err := config.GetConfig()
	if err != nil {
		return false
	}
	return cfg.Agents.CoderMode == config.CoderModeClaudeCode
}
