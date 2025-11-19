package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
)

// handleWorking manages PM's active work: interviewing, drafting, and submitting.
// PM has access to all tools (chat_post, read_file, list_files, spec_submit) and decides
// when to transition back to WAITING by successfully calling submit_spec.
//
//nolint:revive,unparam // ctx will be used for LLM calls and cancellation handling
func (d *Driver) handleWorking(ctx context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM working (interviewing/drafting/submitting)")

	// Check for non-blocking architect feedback
	select {
	case resultMsg := <-d.replyCh:
		// Architect provided feedback asynchronously
		if resultMsg != nil {
			d.logger.Info("📨 Received async feedback from architect")
			// Store feedback in context for next LLM call
			d.stateData["architect_feedback"] = resultMsg
		}
	default:
		// No feedback yet, continue working
	}

	// Get conversation state
	turnCount, _ := d.stateData["turn_count"].(int)
	expertise, _ := d.stateData[StateKeyUserExpertise].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get max turns from config
	cfg, err := config.GetConfig()
	if err != nil {
		return proto.StateError, fmt.Errorf("failed to get config: %w", err)
	}
	maxTurns := cfg.PM.MaxInterviewTurns

	d.logger.Info("PM working (turn %d/%d, expertise: %s)", turnCount, maxTurns, expertise)

	// On first turn, set up the interview context with bootstrap awareness
	if turnCount == 0 {
		if setupErr := d.setupInterviewContext(); setupErr != nil {
			d.logger.Warn("Failed to set up interview context: %v", setupErr)
			// Continue anyway - non-fatal
		}
	}

	// System prompt was set at interview start - just use context manager's conversation
	// No need to render a new prompt every turn
	signal, err := d.callLLMWithTools(ctx, "")
	if err != nil {
		d.logger.Error("❌ PM LLM call failed: %v", err)
		return proto.StateError, fmt.Errorf("LLM call failed: %w", err)
	}

	// Increment turn count
	d.stateData["turn_count"] = turnCount + 1

	// Handle terminal signals from tool processing
	if signal == "SPEC_PREVIEW" {
		// Transition to PREVIEW state for user review
		d.logger.Info("📋 PM transitioning to PREVIEW state for user review")
		return StatePreview, nil
	}

	// Handle AWAIT_USER signal - transition to AWAIT_USER state
	if signal == string(StateAwaitUser) {
		d.logger.Info("⏸️  PM transitioning to AWAIT_USER state")
		return StateAwaitUser, nil
	}

	// Stay in WORKING - PM continues interviewing/drafting
	return StateWorking, nil
}

// setupInterviewContext renders the appropriate interview template based on project state.
// If bootstrap requirements are detected, uses focused bootstrap gate template.
// Otherwise uses full interview start template.
func (d *Driver) setupInterviewContext() error {
	d.logger.Info("📝 Setting up interview context")

	// Get expertise level
	expertise, _ := d.stateData[StateKeyUserExpertise].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get conversation history if any
	conversationHistory, _ := d.stateData["conversation"].([]map[string]string)

	// Check for bootstrap requirements (this checks ALL components)
	bootstrapReqs := d.GetBootstrapRequirements()

	// Get current config to check for existing values
	cfg, cfgErr := config.GetConfig()

	// Build base template data with existing config values
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Expertise":           expertise,
			"ConversationHistory": conversationHistory,
		},
	}

	// Add existing config values if available (so PM doesn't ask for them again)
	if cfgErr == nil {
		if cfg.Project.Name != "" {
			templateData.Extra["ExistingProjectName"] = cfg.Project.Name
		}
		if cfg.Project.PrimaryPlatform != "" {
			templateData.Extra["ExistingPlatform"] = cfg.Project.PrimaryPlatform
		}
		if cfg.Git.RepoURL != "" {
			templateData.Extra["ExistingGitURL"] = cfg.Git.RepoURL
		}
	} else {
		d.logger.Warn("Failed to get config: %v", cfgErr)
	}

	// Select template based on bootstrap requirements
	// Single source of truth: use bootstrap detector's methods
	var templateName templates.StateTemplate
	if bootstrapReqs != nil && bootstrapReqs.HasAnyMissingComponents() {
		if bootstrapReqs.NeedsBootstrapGate() {
			// Project metadata (name/platform/git) is missing - use focused bootstrap gate template
			templateName = templates.PMBootstrapGateTemplate
			d.logger.Info("📋 Using bootstrap gate template (needs project metadata: project_config=%v, git_repo=%v)",
				bootstrapReqs.NeedsProjectConfig, bootstrapReqs.NeedsGitRepo)
		} else {
			// Project metadata is complete, but other components missing - use full interview with bootstrap context
			templateName = templates.PMInterviewStartTemplate
			templateData.Extra["BootstrapRequired"] = true
			templateData.Extra["MissingComponents"] = bootstrapReqs.MissingComponents
			templateData.Extra["DetectedPlatform"] = bootstrapReqs.DetectedPlatform
			templateData.Extra["PlatformConfidence"] = int(bootstrapReqs.PlatformConfidence * 100)
			templateData.Extra["HasRepository"] = !bootstrapReqs.NeedsGitRepo
			templateData.Extra["NeedsDockerfile"] = bootstrapReqs.NeedsDockerfile
			templateData.Extra["NeedsMakefile"] = bootstrapReqs.NeedsMakefile
			templateData.Extra["NeedsKnowledgeGraph"] = bootstrapReqs.NeedsKnowledgeGraph

			d.logger.Info("📋 Using full interview template with bootstrap context: %d missing components, platform: %s",
				len(bootstrapReqs.MissingComponents), bootstrapReqs.DetectedPlatform)
		}
	} else {
		// No bootstrap requirements - use full interview template
		templateName = templates.PMInterviewStartTemplate
		d.logger.Info("📋 Using full interview template (no bootstrap requirements)")
	}

	// Render selected template
	interviewPrompt, renderErr := d.renderer.Render(templateName, templateData)
	if renderErr != nil {
		return fmt.Errorf("failed to render interview template: %w", renderErr)
	}

	// Add as system message to guide the interview
	d.contextManager.AddMessage("system", interviewPrompt)

	d.logger.Info("✅ Interview context configured")
	return nil
}

// renderWorkingPrompt renders the PM working template with current state.
//
//nolint:unused // Reserved for future PM template rendering functionality
func (d *Driver) renderWorkingPrompt() (string, error) {
	// Get conversation history from state
	conversationHistory, _ := d.stateData["conversation"].([]map[string]string)
	expertise, _ := d.stateData["expertise"].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get architect feedback if present
	architectFeedback, _ := d.stateData["architect_feedback"].(string)

	// Get draft spec if present
	draftSpec, _ := d.stateData["draft_spec"].(string)

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Expertise":           expertise,
			"ConversationHistory": conversationHistory,
			"ArchitectFeedback":   architectFeedback,
			"DraftSpec":           draftSpec,
		},
	}

	// Render template with user instructions
	prompt, err := d.renderer.RenderWithUserInstructions(
		templates.PMWorkingTemplate,
		templateData,
		d.workDir,
		"PM",
	)
	if err != nil {
		return "", fmt.Errorf("failed to render PM working template: %w", err)
	}

	return prompt, nil
}

// callLLMWithTools calls the LLM with PM tools in an iteration loop.
// Similar to architect's callLLMWithTools but with PM-specific tool handling.
//
//nolint:cyclop,maintidx // Complex tool iteration logic, refactoring would reduce readability
func (d *Driver) callLLMWithTools(ctx context.Context, prompt string) (string, error) {
	// Use toolloop abstraction for LLM tool calling loop
	loop := toolloop.New(d.llmClient, d.logger)

	cfg := &toolloop.Config[WorkingResult]{
		ContextManager: d.contextManager,
		InitialPrompt:  prompt,
		ToolProvider:   d.toolProvider, // PM's tool provider
		MaxIterations:  10,
		MaxTokens:      agent.ArchitectMaxTokens, // TODO: Add PMMaxTokens constant to config
		AgentID:        d.pmID,                   // Agent ID for tool context
		DebugLogging:   true,                     // Enable for debugging: shows messages sent to LLM
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			// Process results and check for terminal signals
			return d.checkTerminalTools(ctx, calls, results)
		},
		ExtractResult: ExtractPMWorkingResult,
	}

	signal, result, err := toolloop.Run(loop, ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("toolloop execution failed: %w", err)
	}

	// Process extracted result based on signal
	if err := d.processPMResult(result); err != nil {
		return "", fmt.Errorf("failed to process PM result: %w", err)
	}

	// Handle terminal signals from tool processing
	return signal, nil
}

// processPMResult processes the extracted result from PM's toolloop.
// Stores data in stateData and performs any necessary side effects (e.g., injecting messages).
func (d *Driver) processPMResult(result WorkingResult) error {
	switch result.Signal {
	case SignalBootstrapComplete:
		// Store bootstrap params
		d.stateData["bootstrap_params"] = result.BootstrapParams
		d.logger.Info("✅ Bootstrap params stored: project=%s, platform=%s, git=%s",
			result.BootstrapParams["project_name"],
			result.BootstrapParams["platform"],
			result.BootstrapParams["git_url"])

		// Inject system message to transition from bootstrap mode to full interview mode
		transitionMsg := fmt.Sprintf(`# Bootstrap Complete

Project configuration saved successfully:
- Project: %s
- Platform: %s
- Repository: %s

You can now proceed with full requirements gathering for this project. You have access to additional tools:
- **read_file** - Read file contents from the codebase
- **list_files** - List files in the codebase (path, pattern, recursive)

Begin the feature requirements interview by asking the user about what they want to build.`,
			result.BootstrapParams["project_name"],
			result.BootstrapParams["platform"],
			result.BootstrapParams["git_url"])

		d.contextManager.AddMessage("system", transitionMsg)
		d.logger.Info("📝 Injected transition message to switch from bootstrap mode to full interview")

	case SignalSpecPreview:
		// Store draft spec and metadata for PREVIEW state
		d.stateData["draft_spec_markdown"] = result.SpecMarkdown
		d.stateData["spec_metadata"] = result.SpecMetadata
		d.logger.Info("📋 Stored spec for preview (%d bytes)", len(result.SpecMarkdown))

	case SignalAwaitUser:
		// No data to store for await_user, just log
		d.logger.Info("⏸️  PM waiting for user response")

	case "":
		// No signal - this is fine, toolloop will continue
		return nil

	default:
		return fmt.Errorf("unknown PM signal: %s", result.Signal)
	}

	return nil
}

// checkTerminalTools examines tool execution results for terminal signals.
// Returns non-empty signal to trigger state transition.
//
//nolint:cyclop // Multiple terminal conditions (bootstrap, spec_submit, await_user) adds complexity
func (d *Driver) checkTerminalTools(_ context.Context, _ []agent.ToolCall, results []any) string {
	// Check results for terminal signals only - data extraction happens in ExtractPMWorkingResult
	sawAwaitUser := false
	sawBootstrap := false

	for i := range results {
		// Only process successful results
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			continue
		}

		// Check for errors in result
		if success, ok := resultMap["success"].(bool); ok && !success {
			continue // Skip error results
		}

		// Check for bootstrap_configured signal
		if bootstrapConfigured, ok := resultMap["bootstrap_configured"].(bool); ok && bootstrapConfigured {
			d.logger.Info("🔧 PM bootstrap tool succeeded")
			sawBootstrap = true
			// Don't return yet - continue checking for other signals
		}

		// Check for spec_submit signal (PREVIEW flow) - this is terminal
		if previewReady, ok := resultMap["preview_ready"].(bool); ok && previewReady {
			d.logger.Info("📋 PM spec_submit succeeded, transitioning to PREVIEW")
			return "SPEC_PREVIEW"
		}

		// Check for await_user signal
		if awaitUser, ok := resultMap["await_user"].(bool); ok && awaitUser {
			d.logger.Info("⏸️  PM await_user tool called")
			sawAwaitUser = true
		}
	}

	// Bootstrap is not terminal - PM continues after bootstrap completes
	// The data will be available in the result and processed by calling code
	if sawBootstrap {
		d.logger.Info("Bootstrap complete, continuing PM workflow")
	}

	// If we saw await_user, return that signal
	if sawAwaitUser {
		return string(StateAwaitUser)
	}

	return "" // Continue loop
}

// handleIterationLimit is called when max iterations is reached.
// Asks LLM to provide update to user and returns AWAIT_USER signal.

// sendSpecApprovalRequest sends an approval REQUEST message to the architect.
func (d *Driver) sendSpecApprovalRequest(_ context.Context) error {
	// Get spec markdown from state
	specMarkdown, ok := d.stateData["spec_markdown"].(string)
	if !ok || specMarkdown == "" {
		return fmt.Errorf("no spec_markdown found in state")
	}

	// Create approval request payload
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType: proto.ApprovalTypeSpec,
		Content:      specMarkdown,
		Reason:       "PM has completed specification and requests architect review",
		Context:      "Specification ready for validation and story generation",
		Confidence:   proto.ConfidenceHigh,
	}

	// Create REQUEST message
	requestMsg := &proto.AgentMsg{
		ID:        fmt.Sprintf("pm-spec-req-%d", time.Now().UnixNano()),
		Type:      proto.MsgTypeREQUEST,
		FromAgent: d.pmID,
		ToAgent:   "architect-001", // TODO: Get architect ID from config or dispatcher
		Payload:   proto.NewApprovalRequestPayload(approvalPayload),
	}

	// Send via dispatcher
	if err := d.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch REQUEST: %w", err)
	}

	// Store pending request ID for tracking
	d.stateData["pending_request_id"] = requestMsg.ID
	d.logger.Info("📤 Sent spec approval REQUEST to architect (id: %s)", requestMsg.ID)

	return nil
}
