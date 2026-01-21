package pm

import (
	"context"
	"fmt"
	"os"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/chat"
	"orchestrator/pkg/config"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
	"orchestrator/pkg/workspace"
)

// handleWorking manages PM's active work: interviewing, drafting, and submitting.
// PM has access to all tools (chat_post, read_file, list_files, spec_submit) and decides
// when to transition back to WAITING by successfully calling submit_spec.
//
//nolint:revive,unparam // ctx will be used for LLM calls and cancellation handling
func (d *Driver) handleWorking(ctx context.Context) (proto.State, error) {
	d.logger.Info("🎯 PM working (interviewing/drafting/submitting)")

	// MAESTRO.md generation check - runs before interview setup
	// Ensures project has a MAESTRO.md file for agent context
	if err := d.ensureMaestroMd(ctx); err != nil {
		d.logger.Warn("⚠️ Failed to ensure MAESTRO.md: %v (continuing anyway)", err)
		// Non-fatal - continue with interview even if MAESTRO.md generation fails
	}

	// Check for non-blocking architect notifications (story completions, all-stories-complete, etc.)
	select {
	case msg := <-d.replyCh:
		if msg != nil {
			// Process the notification using the same handler as AWAIT_USER
			// This handles all_stories_complete (clears in_flight), story_complete, etc.
			//nolint:contextcheck // Handler uses context.Background() for quick local bootstrap detection
			nextState, err := d.handleArchitectNotification(msg)
			if err != nil {
				d.logger.Warn("⚠️ Error handling architect notification in WORKING: %v", err)
				// Continue working - don't fail on notification errors
			} else if nextState != StateAwaitUser {
				// If the notification triggers a state change (other than back to working via AWAIT_USER),
				// return that state. Currently handleArchitectNotification returns StateWorking after
				// injecting context, so we continue working.
				d.logger.Info("📨 Processed architect notification in WORKING state")
			}
		}
	default:
		// No notifications, continue working
	}

	// Get conversation state
	turnCount := utils.GetStateValueOr[int](d.BaseStateMachine, StateKeyTurnCount, 0)
	expertise := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserExpertise, DefaultExpertise)

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
	d.SetStateData(StateKeyTurnCount, turnCount+1)

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
// If spec was uploaded, adds it to context for parsing.
// Otherwise uses full interview start template.
//
//nolint:cyclop // Complex setup logic with multiple conditional paths is inherent to this function
func (d *Driver) setupInterviewContext() error {
	d.logger.Info("📝 Setting up interview context")

	// Get state data
	// Get expertise level
	expertise := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserExpertise, DefaultExpertise)

	// Get conversation history if any
	conversationHistory, _ := utils.GetStateValue[[]map[string]string](d.BaseStateMachine, "conversation")

	// Check if spec was uploaded (vs being generated through interview)
	specUploaded := utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeySpecUploaded, false)
	uploadedSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")

	// Check for bootstrap requirements (this checks ALL components)
	bootstrapReqs := d.GetBootstrapRequirements()

	// Check if bootstrap markdown already exists in state (indicates bootstrap config is complete)
	bootstrapMarkdown, hasBootstrapMarkdown := utils.GetStateValue[string](d.BaseStateMachine, StateKeyBootstrapRequirements)

	// Get current config to check for existing values
	cfg, cfgErr := config.GetConfig()

	// Build base template data with existing config values
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Expertise":           expertise,
			"ConversationHistory": conversationHistory,
		},
	}

	// Add MAESTRO.md content if available (formatted with trust boundary)
	maestroMdContent := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyMaestroMdContent, "")
	if maestroMdContent != "" {
		templateData.Extra["MaestroMd"] = utils.FormatMaestroMdForPrompt(maestroMdContent)
	}

	// If spec was uploaded, add it to template data for parsing
	if specUploaded && uploadedSpec != "" {
		templateData.Extra["UploadedSpec"] = uploadedSpec
		d.logger.Info("📄 Uploaded spec detected (%d bytes) - will extract bootstrap info from it", len(uploadedSpec))
	}

	// Add existing config values if available (so PM doesn't ask for them again)
	if cfgErr == nil {
		if cfg.Project != nil && cfg.Project.Name != "" {
			templateData.Extra["ExistingProjectName"] = cfg.Project.Name
		}
		if cfg.Project != nil && cfg.Project.PrimaryPlatform != "" {
			templateData.Extra["ExistingPlatform"] = cfg.Project.PrimaryPlatform
		}
		if cfg.Git != nil && cfg.Git.RepoURL != "" {
			templateData.Extra["ExistingGitURL"] = cfg.Git.RepoURL
		}
	}

	// Select template based on bootstrap requirements
	// Single source of truth: use bootstrap detector's methods
	// BUT: if bootstrap markdown already exists in state, config is complete - skip bootstrap context
	var templateName templates.StateTemplate
	if bootstrapReqs != nil && bootstrapReqs.HasAnyMissingComponents() && !hasBootstrapMarkdown {
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
			templateData.Extra["NeedsClaudeCode"] = bootstrapReqs.NeedsClaudeCode

			d.logger.Info("📋 Using full interview template with bootstrap context: %d missing components, platform: %s",
				len(bootstrapReqs.MissingComponents), bootstrapReqs.DetectedPlatform)
		}
	} else {
		// Either no bootstrap needed OR bootstrap config already complete (markdown exists in state)
		// Use clean interview template without bootstrap context
		templateName = templates.PMInterviewStartTemplate
		if hasBootstrapMarkdown {
			d.logger.Info("📋 Using clean interview template (bootstrap config complete, markdown exists: %d bytes)", len(bootstrapMarkdown))
		} else {
			d.logger.Info("📋 Using full interview template (no bootstrap requirements)")
		}
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

// callLLMWithTools calls the LLM with PM tools in an iteration loop.
// Similar to architect's callLLMWithTools but with PM-specific tool handling.
//
//nolint:cyclop,maintidx // Complex tool iteration logic, refactoring would reduce readability
func (d *Driver) callLLMWithTools(ctx context.Context, prompt string) (string, error) {
	// Get spec_submit tool
	specSubmitTool, err := d.toolProvider.Get(tools.ToolSpecSubmit)
	if err != nil {
		return "", logx.Wrap(err, "failed to get spec_submit tool")
	}

	// Inject state into spec_submit tool
	if submitTool, ok := specSubmitTool.(*tools.SpecSubmitTool); ok {
		// Inject bootstrap requirement IDs if bootstrap is needed
		// The architect will render the full technical specification from these IDs
		if reqs := d.GetBootstrapRequirements(); reqs != nil && reqs.HasAnyMissingComponents() {
			reqIDs := reqs.ToRequirementIDs()
			if len(reqIDs) > 0 {
				submitTool.SetBootstrapRequirements(reqIDs)
				d.logger.Info("📋 Injected bootstrap requirements into spec_submit: %v", reqIDs)
			}
		}

		// Inject in_flight flag to enforce hotfix-only mode during development
		inFlight := utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeyInFlight, false)
		submitTool.SetInFlight(inFlight)
		d.logger.Info("📝 Injected in_flight=%v into spec_submit tool", inFlight)
	}

	terminalTool := specSubmitTool

	// Get all general tools (everything except spec_submit)
	allTools := d.toolProvider.List()
	generalTools := make([]tools.Tool, 0, len(allTools)-1)
	//nolint:gocritic // ToolMeta is 80 bytes but value semantics preferred here
	for _, meta := range allTools {
		if meta.Name != tools.ToolSpecSubmit {
			tool, err := d.toolProvider.Get(meta.Name)
			if err != nil {
				return "", logx.Wrap(err, fmt.Sprintf("failed to get tool %s", meta.Name))
			}
			generalTools = append(generalTools, tool)
		}
	}

	// Use toolloop abstraction for LLM tool calling loop
	loop := toolloop.New(d.LLMClient, d.logger)

	cfg := &toolloop.Config[WorkingResult]{
		ContextManager:     d.contextManager,
		InitialPrompt:      prompt,
		GeneralTools:       generalTools,
		TerminalTool:       terminalTool,
		MaxIterations:      10,
		MaxTokens:          agent.PMMaxTokens,
		AgentID:            d.GetAgentID(),               // Agent ID for tool context
		DebugLogging:       config.GetDebugLLMMessages(), // Controlled via config.json debug.llm_messages
		PersistenceChannel: d.persistenceChannel,         // For tool execution logging
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("pm_working_%s", d.GetAgentID()),
			SoftLimit: 0,  // Disabled - soft limit nudging was counter-productive
			HardLimit: 10, // Require user confirmation at 10 iterations
			OnSoftLimit: func(_ int) {
				// Disabled - was causing premature user prompts
			},
			OnHardLimit: func(_ context.Context, key string, count int) error {
				d.logger.Error("❌ PM iteration hard limit reached (%d iterations) - must call await_user with status", count)
				d.SetStateData("iteration_limit_reached", true)
				d.SetStateData("iteration_limit_key", key)
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	// Run toolloop in a loop to handle user-confirmed continuation on iteration limit
	for {
		out := toolloop.Run(loop, ctx, cfg)

		// Switch on outcome kind first
		switch out.Kind {
		case toolloop.OutcomeIterationLimit:
			// PM hit iteration limit - ask user if they want to continue or provide guidance
			d.logger.Info("⏸️  PM reached iteration limit (%d iterations), asking user for confirmation", out.Iteration)

			if d.chatService == nil {
				d.logger.Error("❌ Chat service not available for user confirmation")
				return "", fmt.Errorf("PM iteration limit reached and chat service unavailable: %w", out.Err)
			}

			// Ask user for confirmation to continue (PM uses "product" channel)
			action, err := d.chatService.AskUserConfirmation(
				ctx,
				d.GetAgentID(),
				fmt.Sprintf("PM has completed %d iterations gathering project information. Click Continue to allow more iterations, or Provide Guidance to give new instructions.", out.Iteration),
				"product",     // PM chat channel
				5*time.Second, // Poll every 5 seconds
			)
			if err != nil {
				d.logger.Error("❌ Failed to get user confirmation: %v", err)
				return "", fmt.Errorf("PM iteration limit reached and user confirmation failed: %w", err)
			}

			switch action {
			case chat.ConfirmationContinue:
				d.logger.Info("✅ User confirmed continuation, restarting toolloop")
				// Continue the for loop - toolloop will restart with fresh iteration count
				// Context is preserved in contextManager
				continue

			case chat.ConfirmationCancel:
				// User wants to provide guidance - return AWAIT_USER signal
				// handleWorking will catch this and transition to AWAIT_USER state
				d.logger.Info("📝 User chose to provide guidance, returning AWAIT_USER signal")
				return string(StateAwaitUser), nil
			}

		case toolloop.OutcomeProcessEffect:
			// Tool returned ProcessEffect to pause the loop
			d.logger.Info("🔔 Tool returned ProcessEffect with signal: %s", out.Signal)

			// Route based on signal
			switch out.Signal {
			case tools.SignalBootstrapComplete:
				// bootstrap tool was called - extract data from ProcessEffect.Data
				effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
				if !ok {
					return "", fmt.Errorf("BOOTSTRAP_COMPLETE effect data is not map[string]any: %T", out.EffectData)
				}

				// Extract bootstrap data from ProcessEffect.Data
				projectName := utils.GetMapFieldOr[string](effectData, "project_name", "")
				gitURL := utils.GetMapFieldOr[string](effectData, "git_url", "")
				platform := utils.GetMapFieldOr[string](effectData, "platform", "")
				bootstrapMarkdown := utils.GetMapFieldOr[string](effectData, "bootstrap_markdown", "")
				resetContext := utils.GetMapFieldOr[bool](effectData, "reset_context", false)

				// Store in state
				bootstrapParams := map[string]string{
					"project_name": projectName,
					"git_url":      gitURL,
					"platform":     platform,
				}
				d.SetStateData(StateKeyBootstrapParams, bootstrapParams)
				d.SetStateData(StateKeyBootstrapRequirements, bootstrapMarkdown)
				d.logger.Info("✅ Bootstrap params stored: project=%s, platform=%s, git=%s", projectName, platform, gitURL)

				// Reset context if tool requested it (now safe - Clear() properly clears pendingToolResults)
				if resetContext {
					d.logger.Info("🔄 Resetting context as requested by bootstrap tool")
					d.contextManager.Clear()

					// Rebuild context from interview template
					if setupErr := d.setupInterviewContext(); setupErr != nil {
						d.logger.Warn("Failed to rebuild interview context: %v", setupErr)
					} else {
						d.logger.Info("✅ PM context rebuilt for user requirements gathering")
					}

					// If spec was uploaded, re-inject it after context reset
					if utils.GetStateValueOr[bool](d.BaseStateMachine, StateKeySpecUploaded, false) {
						uploadedSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
						if uploadedSpec != "" {
							specMsg := fmt.Sprintf("The user has provided the following specification document. Please extract the **user feature requirements** from it (ignore any infrastructure/bootstrap requirements as those have been handled):\n\n```markdown\n%s\n```", uploadedSpec)
							d.contextManager.AddMessage("user", specMsg)
							d.logger.Info("📄 Re-injected uploaded spec (%d bytes) after context reset", len(uploadedSpec))
						}
					}
				}

				return "", nil

			case tools.SignalSpecPreview:
				// spec_submit was called - extract data from ProcessEffect.Data
				effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
				if !ok {
					return "", fmt.Errorf("SPEC_PREVIEW effect data is not map[string]any: %T", out.EffectData)
				}

				// Extract spec data from ProcessEffect.Data
				userSpec := utils.GetMapFieldOr[string](effectData, "user_spec", "")
				summary := utils.GetMapFieldOr[string](effectData, "summary", "")
				metadata, _ := utils.SafeAssert[map[string]any](effectData["metadata"])
				isHotfix := utils.GetMapFieldOr[bool](effectData, "is_hotfix", false)

				// Extract bootstrap requirements (typed slice from spec_submit)
				var bootstrapReqIDs []string
				if reqs, ok := effectData["bootstrap_requirements"]; ok && reqs != nil {
					// Convert from []workspace.BootstrapRequirementID to []string for storage
					if typedReqs, ok := reqs.([]workspace.BootstrapRequirementID); ok {
						for _, r := range typedReqs {
							bootstrapReqIDs = append(bootstrapReqIDs, string(r))
						}
					}
				}

				// Store specs using canonical state keys
				d.SetStateData(StateKeyUserSpecMd, userSpec)
				d.SetStateData(StateKeySpecMetadata, metadata)
				d.SetStateData(StateKeyIsHotfix, isHotfix)

				// Only store bootstrap requirements if not a hotfix (hotfixes don't include bootstrap)
				if !isHotfix && len(bootstrapReqIDs) > 0 {
					d.SetStateData(StateKeyBootstrapRequirements, bootstrapReqIDs)
				}

				d.logger.Info("📋 Stored spec for preview (bootstrap reqs: %v, user: %d bytes, hotfix: %v, summary: %s)",
					bootstrapReqIDs, len(userSpec), isHotfix, summary)

				return SignalSpecPreview, nil

			case tools.SignalAwaitUser:
				// chat_ask_user was called - transition to AWAIT_USER state
				d.logger.Info("⏸️  PM waiting for user response via chat_ask_user")
				return SignalAwaitUser, nil

			default:
				return "", fmt.Errorf("unknown ProcessEffect signal: %s", out.Signal)
			}

		case toolloop.OutcomeNoToolTwice, toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
			// All other errors are treated as toolloop failures
			return "", fmt.Errorf("toolloop execution failed: %w", out.Err)

		default:
			return "", fmt.Errorf("unknown toolloop outcome kind: %v", out.Kind)
		}
	}
}

// handleIterationLimit is called when max iterations is reached.
// Asks LLM to provide update to user and returns AWAIT_USER signal.

// sendSpecApprovalRequest sends an approval REQUEST message to the architect.
func (d *Driver) sendSpecApprovalRequest(_ context.Context) error {
	// Get state data
	// Get user spec from state using canonical key
	userSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
	if userSpec == "" {
		return fmt.Errorf("no user_spec_md found in state")
	}

	// Get bootstrap requirements from state (stored as []string)
	// Architect will render the full technical spec from these IDs
	bootstrapReqs, _ := utils.GetStateValue[[]string](d.BaseStateMachine, StateKeyBootstrapRequirements)

	// Create approval request payload
	// Content field contains user requirements, BootstrapRequirements contains requirement IDs
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType:          proto.ApprovalTypeSpec,
		Content:               userSpec,      // User requirements only
		BootstrapRequirements: bootstrapReqs, // Bootstrap requirement IDs (architect renders spec)
		Reason:                "PM has completed specification and requests architect review",
		Context:               "Specification ready for validation and story generation",
		Confidence:            proto.ConfidenceHigh,
	}

	// Create REQUEST message
	requestMsg := &proto.AgentMsg{
		ID:        fmt.Sprintf("pm-spec-req-%d", time.Now().UnixNano()),
		Type:      proto.MsgTypeREQUEST,
		FromAgent: d.GetAgentID(),
		ToAgent:   "architect", // Dispatcher resolves to "architect-001"
		Payload:   proto.NewApprovalRequestPayload(approvalPayload),
	}

	// Send via dispatcher
	if err := d.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch REQUEST: %w", err)
	}

	d.logger.Info("📤 Sent spec approval REQUEST to architect (user: %d bytes, bootstrap reqs: %v, id: %s)",
		len(userSpec), bootstrapReqs, requestMsg.ID)

	// Checkpoint state for crash recovery (spec submission is a stable boundary)
	if cfg, err := config.GetConfig(); err == nil && cfg.SessionID != "" {
		d.Checkpoint(cfg.SessionID)
	}

	return nil
}

// sendHotfixRequest sends a HOTFIX REQUEST message to the architect.
// Hotfixes go through preview but jump the line when submitted, bypassing the normal spec queue.
// The payload contains the hotfix content from user_spec_md formatted as a single requirement.
func (d *Driver) sendHotfixRequest(_ context.Context) error {
	// Get hotfix content from canonical state var (same as regular specs)
	userSpec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, "")
	if userSpec == "" {
		return fmt.Errorf("no user_spec_md found in state for hotfix")
	}

	// Get platform from detected platform or default to unknown
	platform := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyDetectedPlatform, "unknown")

	// Create hotfix request payload with the spec content as a single requirement
	// The architect will parse and validate this before dispatching to hotfix coder
	hotfixPayload := &proto.HotfixRequestPayload{
		Analysis: "Hotfix request from user",
		Platform: platform,
		Requirements: []any{
			map[string]any{
				"title":       "Hotfix",
				"description": userSpec,
				"story_type":  "app",
			},
		},
		Urgency: "normal",
	}

	// Create REQUEST message
	requestMsg := &proto.AgentMsg{
		ID:        fmt.Sprintf("pm-hotfix-req-%d", time.Now().UnixNano()),
		Type:      proto.MsgTypeREQUEST,
		FromAgent: d.GetAgentID(),
		ToAgent:   "architect", // Dispatcher resolves to "architect-001"
		Payload:   proto.NewHotfixRequestPayload(hotfixPayload),
	}

	// Send via dispatcher
	if err := d.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch HOTFIX REQUEST: %w", err)
	}

	d.logger.Info("🔧 Sent hotfix REQUEST to architect (platform: %s, id: %s)", platform, requestMsg.ID)

	// Checkpoint state for crash recovery (hotfix submission is a stable boundary)
	if cfg, err := config.GetConfig(); err == nil && cfg.SessionID != "" {
		d.Checkpoint(cfg.SessionID)
	}

	return nil
}

// ensureMaestroMd ensures MAESTRO.md content is available in state.
// Loads from repo if file exists, otherwise runs generation phase.
// Called at the start of WORKING state to ensure project context is available.
//
// Freshness policy: Always check repo first to avoid stale content.
// If file exists in repo, use it (may have been updated externally).
// If not in repo but in state, that's pending generated content (will commit via spec_submit).
// If not in repo and not in state, run generation phase.
func (d *Driver) ensureMaestroMd(ctx context.Context) error {
	// Try to load from repo via mirror manager
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Skip if git is not configured (bootstrap not complete)
	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		d.logger.Debug("Git not configured, skipping MAESTRO.md check")
		return nil
	}

	// Always check repo first for fresh content (don't trust persisted state)
	mirrorMgr := mirror.NewManager(d.workDir)
	repoContent, err := mirrorMgr.LoadMaestroMd(ctx)
	if err != nil {
		d.logger.Warn("Failed to load MAESTRO.md from repo: %v", err)
		// Continue to check state/generation
	} else if repoContent != "" {
		// Found in repo - use it (may have been updated externally)
		d.SetStateData(StateKeyMaestroMdContent, repoContent)
		d.logger.Info("📄 Loaded MAESTRO.md from repo (%d bytes)", len(repoContent))
		return nil
	}

	// Not in repo - check if we have pending generated content in state
	existingContent := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyMaestroMdContent, "")
	if existingContent != "" {
		d.logger.Debug("MAESTRO.md content in state (%d bytes) - pending commit via spec_submit", len(existingContent))
		return nil
	}

	// No MAESTRO.md exists anywhere - run generation phase
	d.logger.Info("📝 MAESTRO.md not found, running generation phase...")
	return d.runMaestroMdGeneration(ctx)
}

// runMaestroMdGeneration runs the MAESTRO.md generation toolloop.
// Uses PMMaestroMdTools (read_file, list_files, maestro_md_submit).
func (d *Driver) runMaestroMdGeneration(ctx context.Context) error {
	// Render generation template
	templateData := &templates.TemplateData{
		Extra: map[string]any{},
	}

	// Try to load README.md for context
	readmePath := d.workDir + "/README.md"
	if readme, readErr := os.ReadFile(readmePath); readErr == nil && len(readme) > 0 {
		templateData.Extra["ExistingReadme"] = string(readme)
	}

	prompt, err := d.renderer.Render(templates.PMMaestroGenerationTemplate, templateData)
	if err != nil {
		return fmt.Errorf("failed to render MAESTRO.md generation template: %w", err)
	}

	// Create a separate context manager for generation phase
	// This keeps generation conversation separate from interview
	genContextMgr := contextmgr.NewContextManagerWithModel(d.contextManager.GetModelName())
	genContextMgr.AddMessage("system", prompt)

	// Create tool provider with MAESTRO.md generation tools
	genToolProvider := tools.NewProvider(&tools.AgentContext{
		Executor:   d.executor, // Required for read_file/list_files tools
		WorkDir:    d.workDir,
		AgentID:    d.GetAgentID(),
		ProjectDir: d.workDir,
	}, tools.PMMaestroMdTools)

	// Get tools for toolloop
	var genTools []tools.Tool
	//nolint:gocritic // rangeValCopy: Direct access is clearer than pointer dereferencing
	for _, meta := range genToolProvider.List() {
		if meta.Name != tools.ToolMaestroMdSubmit {
			tool, getErr := genToolProvider.Get(meta.Name)
			if getErr != nil {
				return fmt.Errorf("failed to get tool %s: %w", meta.Name, getErr)
			}
			genTools = append(genTools, tool)
		}
	}

	submitTool, err := genToolProvider.Get(tools.ToolMaestroMdSubmit)
	if err != nil {
		return fmt.Errorf("failed to get maestro_md_submit tool: %w", err)
	}

	// Run generation toolloop
	loop := toolloop.New(d.LLMClient, d.logger)
	genCfg := &toolloop.Config[MaestroMdResult]{
		ContextManager:     genContextMgr,
		InitialPrompt:      "",
		GeneralTools:       genTools,
		TerminalTool:       submitTool,
		MaxIterations:      10,
		MaxTokens:          agent.PMMaxTokens,
		AgentID:            d.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: d.persistenceChannel,
	}

	out := toolloop.Run(loop, ctx, genCfg)

	// Handle outcome
	switch out.Kind {
	case toolloop.OutcomeProcessEffect:
		if out.Signal == tools.SignalMaestroMdComplete {
			// Extract content from effect data
			effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
			if !ok {
				return fmt.Errorf("MAESTRO_MD_COMPLETE effect data is not map[string]any: %T", out.EffectData)
			}
			content := utils.GetMapFieldOr[string](effectData, "content", "")
			if content == "" {
				return fmt.Errorf("MAESTRO.md content is empty")
			}

			// Store in state
			d.SetStateData(StateKeyMaestroMdContent, content)
			d.logger.Info("✅ MAESTRO.md generated (%d bytes)", len(content))
			return nil
		}
		return fmt.Errorf("unexpected signal in MAESTRO.md generation: %s", out.Signal)

	case toolloop.OutcomeMaxIterations, toolloop.OutcomeLLMError, toolloop.OutcomeNoToolTwice:
		return fmt.Errorf("MAESTRO.md generation failed: %w", out.Err)

	default:
		return fmt.Errorf("unexpected outcome in MAESTRO.md generation: %v", out.Kind)
	}
}

// MaestroMdResult is the result type for MAESTRO.md generation toolloop.
type MaestroMdResult struct {
	Content string
}
