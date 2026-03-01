package coder

import (
	"context"
	"fmt"
	"os"
	"strings"
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

	// Express/hotfix stories skip planning, so use the task content as the plan.
	if plan == "" {
		taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
		if taskContent == "" {
			return proto.StateError, false, logx.Errorf("no plan or task content available for coding")
		}
		plan = taskContent
		c.logger.Info("📋 Using story content as plan (express/hotfix story)")
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
		c.codingToolProvider = nil // Force recreation
	}

	// Check for existing session ID (for resume) and resume input (feedback)
	existingSessionID := utils.GetStateValueOr[string](sm, KeyCodingSessionID, "")
	resumeInput := utils.GetStateValueOr[string](sm, KeyResumeInput, "")
	shouldResume := existingSessionID != "" && resumeInput != ""

	// Ensure coding tool provider is initialized
	// Uses Claude Code-specific provider that includes container_switch (handled via SignalContainerSwitch)
	if c.codingToolProvider == nil {
		storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))
		c.codingToolProvider = c.createClaudeCodeCodingToolProvider(storyType, storyID)
		c.logger.Debug("Created Claude Code coding ToolProvider for story type: %s", storyType)
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

	// Track if Claude Code was upgraded in-place (container image needs rebuild)
	if result.ContainerUpgradeNeeded {
		c.containerUpgradeNeeded = true
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
		return proto.StateError, false, logx.Errorf("Claude Code coding timed out after %s with %d responses", result.Duration, result.ResponseCount)

	case claude.SignalInactivity:
		c.logger.Warn("⏰ Claude Code coding stalled (no output for inactivity timeout)")
		return proto.StateError, false, logx.Errorf("Claude Code coding stalled - no output received (%d responses before stall)", result.ResponseCount)

	case claude.SignalError:
		errMsg := "unknown error"
		if result.Error != nil {
			errMsg = result.Error.Error()
		}
		return proto.StateError, false, logx.Errorf("Claude Code coding error: %s", errMsg)

	case claude.SignalStoryComplete:
		// done tool detected empty diff (Case A) - story already implemented
		sm.SetStateData(KeyCodingCompletedAt, time.Now().UTC())

		// Store evidence from Claude Code result
		if result.Evidence != "" {
			sm.SetStateData(KeyCompletionDetails, result.Evidence)
		}

		// Build effect data for processStoryCompleteDataFromEffect
		effectData := map[string]any{
			"evidence":            result.Evidence,
			"exploration_summary": result.ExplorationSummary,
		}
		// Confidence is required by processStoryCompleteDataFromEffect but
		// Claude Code may not provide it; default to HIGH since the coder is confident
		if result.Evidence != "" {
			effectData["confidence"] = "HIGH"
		}

		if err := c.processStoryCompleteDataFromEffect(sm, effectData); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to process story complete data from Claude Code")
		}

		c.logger.Info("✅ Story completion claim from Claude Code coding, transitioning to PLAN_REVIEW")
		return StatePlanReview, false, nil

	case claude.SignalPlanComplete:
		// Unexpected - we're in coding mode, not planning
		c.logger.Warn("Claude Code sent plan_complete signal during coding mode")
		return proto.StateError, false, logx.Errorf("unexpected plan_complete signal in coding mode")

	case claude.SignalContainerSwitch:
		// Claude Code requested a container switch
		if result.ContainerSwitchTarget == "" {
			return proto.StateError, false, logx.Errorf("container_switch called without target container name")
		}

		c.logger.Info("🐳 Claude Code requested container switch to: %s", result.ContainerSwitchTarget)

		// Store target container for the switch (will be processed on next CODING entry)
		sm.SetStateData(KeyContainerSwitchTarget, result.ContainerSwitchTarget)

		// The session ID is already stored (line 120-122 above)
		// Set resume input to inform Claude Code about the container switch
		resumeMsg := fmt.Sprintf("Container switch completed. You are now running in container '%s'. Continue with your work.",
			result.ContainerSwitchTarget)
		sm.SetStateData(KeyResumeInput, resumeMsg)

		// Return to CODING state - the container switch will happen at state entry
		return StateCoding, false, nil

	default:
		return proto.StateError, false, logx.Errorf("unexpected Claude Code signal: %s", result.Signal)
	}
}

// performContainerSwitch switches to a new container image for Claude Code mode.
// This is called when Claude Code requests a container switch via SignalContainerSwitch.
func (c *Coder) performContainerSwitch(ctx context.Context, targetContainer string) error {
	if c.longRunningExecutor == nil {
		return fmt.Errorf("no executor available for container switch")
	}

	c.logger.Info("🐳 Switching container from %s to %s", c.containerName, targetContainer)

	// Check if Claude Code mode is configured (for session persistence volume)
	isClaudeCodeConfigured := false
	if cfg, cfgErr := config.GetConfig(); cfgErr == nil && cfg.Agents != nil {
		isClaudeCodeConfigured = cfg.Agents.CoderMode == config.CoderModeClaudeCode
	}

	// Stop current container
	if c.containerName != "" {
		c.logger.Info("Stopping current container: %s", c.containerName)
		stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := c.longRunningExecutor.StopContainer(stopCtx, c.containerName); err != nil {
			c.logger.Warn("Failed to stop container %s: %v (continuing anyway)", c.containerName, err)
		}
		c.containerName = ""
	}

	// Update docker image to target
	c.SetDockerImage(targetContainer)

	// Create execution options for new container (read-write for coding)
	execOpts := exec.Opts{
		WorkDir:         c.workDir,
		ReadOnly:        false, // Coding requires write access
		NetworkDisabled: false, // Network enabled
		User:            "1000:1000",
		Env:             []string{"HOME=/tmp"},
		Timeout:         0, // No timeout for long-running container
		ClaudeCodeMode:  isClaudeCodeConfigured,
		ResourceLimits: &exec.ResourceLimits{
			CPUs:   "2",
			Memory: "2g",
			PIDs:   1024,
		},
	}

	// Start new container
	agentID := c.GetID()
	sanitizedAgentID := utils.SanitizeContainerName(agentID)

	containerName, err := c.longRunningExecutor.StartContainer(ctx, sanitizedAgentID, &execOpts)
	if err != nil {
		// Try falling back to bootstrap container
		c.logger.Warn("Failed to start target container %s, falling back to bootstrap: %v",
			targetContainer, err)

		c.SetDockerImage(config.BootstrapContainerTag)
		containerName, err = c.longRunningExecutor.StartContainer(ctx, sanitizedAgentID, &execOpts)
		if err != nil {
			return fmt.Errorf("failed to start container (including fallback): %w", err)
		}
		c.logger.Info("Started fallback container: %s", containerName)
	}

	c.containerName = containerName
	c.logger.Info("✅ Container switch complete: now running %s", containerName)

	// Reset Claude Code availability check (new container may have different capabilities)
	c.claudeCodeAvailabilityChecked = false
	c.claudeCodeAvailable = false

	return nil
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

	// Check if Claude Code is available and meets minimum version (cache per-story)
	if !c.claudeCodeAvailabilityChecked {
		c.claudeCodeAvailable = c.checkClaudeCodeAvailable(ctx)
		c.claudeCodeAvailabilityChecked = true

		if !c.claudeCodeAvailable {
			c.logger.Warn("⚠️ Claude Code unavailable in container %s — falling back to standard mode",
				c.containerName)
		}
	}

	return c.claudeCodeAvailable
}

// checkClaudeCodeAvailable probes the container to see if Claude Code is installed
// and meets the minimum version requirement. If installed but below minimum, attempts
// an in-place upgrade. Returns false (triggering standard mode fallback) if Claude Code
// is missing or below minimum and upgrade fails.
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

	versionOutput := strings.TrimSpace(result.Stdout)
	c.logger.Info("✅ Claude Code available: %s", versionOutput)

	// Check version against minimum requirement
	installedVer := claude.ParseVersion(versionOutput)
	if installedVer == "" {
		c.logger.Warn("⚠️ Could not parse Claude Code version from: %s", versionOutput)
		return true // Can't parse — assume OK, EnsureClaudeCode will recheck
	}

	if claude.CompareVersions(installedVer, config.MinClaudeCodeVersion) >= 0 {
		return true // Version meets minimum
	}

	// Below minimum — attempt in-place upgrade
	c.logger.Warn("⚠️ Claude Code %s is below minimum %s", installedVer, config.MinClaudeCodeVersion)

	installer := claude.NewInstaller(c.longRunningExecutor, c.containerName, c.logger)
	upgradeCtx, upgradeCancel := context.WithTimeout(ctx, 5*time.Minute)
	defer upgradeCancel()

	if upgradeErr := installer.UpgradeClaudeCode(upgradeCtx); upgradeErr != nil {
		// Upgrade failed — fall back to standard mode, signal maintenance
		c.logger.Warn("⚠️ In-place upgrade failed — falling back to standard mode: %v", upgradeErr)
		c.containerUpgradeNeeded = true
		return false
	}

	// Upgrade succeeded — verify new version
	c.containerUpgradeNeeded = true // Signal maintenance to permanently fix image
	verifyCtx, verifyCancel := context.WithTimeout(ctx, 10*time.Second)
	defer verifyCancel()
	verifyResult, verifyErr := c.longRunningExecutor.Run(verifyCtx, []string{"claude", "--version"}, &exec.Opts{})
	if verifyErr == nil && verifyResult.ExitCode == 0 {
		newVer := claude.ParseVersion(strings.TrimSpace(verifyResult.Stdout))
		c.logger.Info("✅ Claude Code upgraded: %s → %s", installedVer, newVer)
	}
	return true
}
