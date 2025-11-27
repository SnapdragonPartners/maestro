package pm

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
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
			d.SetStateData("architect_feedback", resultMsg)
		}
	default:
		// No feedback yet, continue working
	}

	// Get conversation state
	stateData := d.GetStateData()
	turnCount, _ := stateData["turn_count"].(int)
	expertise, _ := stateData[StateKeyUserExpertise].(string)
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
	d.SetStateData("turn_count", turnCount+1)

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
func (d *Driver) setupInterviewContext() error {
	d.logger.Info("📝 Setting up interview context")

	// Get state data
	stateData := d.GetStateData()

	// Get expertise level
	expertise, _ := stateData[StateKeyUserExpertise].(string)
	if expertise == "" {
		expertise = DefaultExpertise
	}

	// Get conversation history if any
	conversationHistory, _ := stateData["conversation"].([]map[string]string)

	// Check if spec was uploaded (vs being generated through interview)
	specUploaded, _ := stateData["spec_uploaded"].(bool)
	uploadedSpec, _ := stateData["draft_spec_markdown"].(string)

	// Check for bootstrap requirements (this checks ALL components)
	bootstrapReqs := d.GetBootstrapRequirements()

	// Check if bootstrap markdown already exists in state (indicates bootstrap config is complete)
	bootstrapMarkdown, hasBootstrapMarkdown := stateData[StateKeyBootstrapRequirements].(string)

	// Get current config to check for existing values
	cfg, cfgErr := config.GetConfig()

	// Build base template data with existing config values
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"Expertise":           expertise,
			"ConversationHistory": conversationHistory,
		},
	}

	// If spec was uploaded, add it to template data for parsing
	if specUploaded && uploadedSpec != "" {
		templateData.Extra["UploadedSpec"] = uploadedSpec
		d.logger.Info("📄 Uploaded spec detected (%d bytes) - will extract bootstrap info from it", len(uploadedSpec))
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

	// Inject bootstrap markdown into spec_submit tool if it exists in state
	if bootstrapMarkdown, ok := d.GetStateData()[StateKeyBootstrapRequirements].(string); ok && bootstrapMarkdown != "" {
		if submitTool, ok := specSubmitTool.(*tools.SpecSubmitTool); ok {
			submitTool.SetBootstrapMarkdown(bootstrapMarkdown)
			d.logger.Info("📝 Injected bootstrap markdown into spec_submit tool (%d bytes)", len(bootstrapMarkdown))
		}
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
		ContextManager: d.contextManager,
		InitialPrompt:  prompt,
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
		MaxIterations:  10,
		MaxTokens:      agent.ArchitectMaxTokens, // TODO: Add PMMaxTokens constant to config
		AgentID:        d.GetAgentID(),           // Agent ID for tool context
		DebugLogging:   true,                     // Enable for debugging: shows messages sent to LLM
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("pm_working_%s", d.GetAgentID()),
			SoftLimit: 8,  // Warn at 8 iterations
			HardLimit: 10, // Require user status update at 10 iterations
			OnSoftLimit: func(count int) {
				d.logger.Warn("⚠️  PM iteration soft limit reached (%d iterations)", count)
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

	out := toolloop.Run(loop, ctx, cfg)

	// Switch on outcome kind first
	switch out.Kind {
	case toolloop.OutcomeProcessEffect:
		// Tool returned ProcessEffect to pause the loop
		d.logger.Info("🔔 Tool returned ProcessEffect with signal: %s", out.Signal)

		// Route based on signal
		switch out.Signal {
		case tools.SignalBootstrapComplete:
			// bootstrap tool was called - extract data from ProcessEffect.Data
			effectData, ok := out.EffectData.(map[string]any)
			if !ok {
				return "", fmt.Errorf("BOOTSTRAP_COMPLETE effect data is not map[string]any: %T", out.EffectData)
			}

			// Extract bootstrap data from ProcessEffect.Data
			projectName, _ := effectData["project_name"].(string)
			gitURL, _ := effectData["git_url"].(string)
			platform, _ := effectData["platform"].(string)
			bootstrapMarkdown, _ := effectData["bootstrap_markdown"].(string)
			resetContext, _ := effectData["reset_context"].(bool)

			// Store in state
			bootstrapParams := map[string]string{
				"project_name": projectName,
				"git_url":      gitURL,
				"platform":     platform,
			}
			d.SetStateData("bootstrap_params", bootstrapParams)
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
				if specUploaded, _ := d.GetStateData()["spec_uploaded"].(bool); specUploaded {
					if uploadedSpec, ok := d.GetStateData()["draft_spec_markdown"].(string); ok && uploadedSpec != "" {
						specMsg := fmt.Sprintf("The user has provided the following specification document. Please extract the **user feature requirements** from it (ignore any infrastructure/bootstrap requirements as those have been handled):\n\n```markdown\n%s\n```", uploadedSpec)
						d.contextManager.AddMessage("user", specMsg)
						d.logger.Info("📄 Re-injected uploaded spec (%d bytes) after context reset", len(uploadedSpec))
					}
				}
			}

			return "", nil

		case tools.SignalSpecPreview:
			// spec_submit was called - extract data from ProcessEffect.Data
			effectData, ok := out.EffectData.(map[string]any)
			if !ok {
				return "", fmt.Errorf("SPEC_PREVIEW effect data is not map[string]any: %T", out.EffectData)
			}

			// Extract spec data from ProcessEffect.Data (infrastructure and user specs are separate)
			infrastructureSpec, _ := effectData["infrastructure_spec"].(string)
			userSpec, _ := effectData["user_spec"].(string)
			summary, _ := effectData["summary"].(string)
			metadata, _ := effectData["metadata"].(map[string]any)

			// Store both specs separately in state for PREVIEW state and later submission to architect
			d.SetStateData("infrastructure_spec", infrastructureSpec)
			d.SetStateData("user_spec", userSpec)
			d.SetStateData("spec_metadata", metadata)

			// For backward compatibility with WebUI preview, concatenate for display
			// (WebUI expects draft_spec_markdown for preview display)
			draftSpecMarkdown := userSpec
			if infrastructureSpec != "" {
				draftSpecMarkdown = infrastructureSpec + "\n\n" + userSpec
			}
			d.SetStateData("draft_spec_markdown", draftSpecMarkdown)

			d.logger.Info("📋 Stored spec for preview (infrastructure: %d bytes, user: %d bytes, summary: %s)",
				len(infrastructureSpec), len(userSpec), summary)

			return SignalSpecPreview, nil

		case tools.SignalAwaitUser:
			// chat_ask_user was called - transition to AWAIT_USER state
			d.logger.Info("⏸️  PM waiting for user response via chat_ask_user")
			return SignalAwaitUser, nil

		default:
			return "", fmt.Errorf("unknown ProcessEffect signal: %s", out.Signal)
		}

	case toolloop.OutcomeIterationLimit:
		// PM hit iteration limit - this should not happen as PM should call chat_ask_user
		// before reaching the limit to provide status updates
		d.logger.Error("❌ PM reached iteration limit (%d iterations) without calling chat_ask_user", out.Iteration)
		return "", fmt.Errorf("PM must call chat_ask_user with status update before iteration limit: %w", out.Err)

	case toolloop.OutcomeNoToolTwice, toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
		// All other errors are treated as toolloop failures
		return "", fmt.Errorf("toolloop execution failed: %w", out.Err)

	default:
		return "", fmt.Errorf("unknown toolloop outcome kind: %v", out.Kind)
	}
}

// processPMResult processes the extracted result from PM's toolloop.
// Stores data in stateData and performs any necessary side effects (e.g., injecting messages).
//
//nolint:unused // Legacy - will be removed after verifying all PM flows use ProcessEffect pattern.
func (d *Driver) processPMResult(result WorkingResult) error {
	switch result.Signal {
	case SignalBootstrapComplete:
		// Store bootstrap params and rendered markdown
		d.SetStateData("bootstrap_params", result.BootstrapParams)
		d.SetStateData(StateKeyBootstrapRequirements, result.BootstrapMarkdown)
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
		d.SetStateData("draft_spec_markdown", result.SpecMarkdown)
		d.SetStateData("spec_metadata", result.SpecMetadata)
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

// handleIterationLimit is called when max iterations is reached.
// Asks LLM to provide update to user and returns AWAIT_USER signal.

// sendSpecApprovalRequest sends an approval REQUEST message to the architect.
func (d *Driver) sendSpecApprovalRequest(_ context.Context) error {
	// Get state data
	stateData := d.GetStateData()

	// Get infrastructure and user specs from state
	infrastructureSpec, _ := stateData["infrastructure_spec"].(string)
	userSpec, ok := stateData["user_spec"].(string)
	if !ok || userSpec == "" {
		return fmt.Errorf("no user_spec found in state")
	}

	// Create approval request payload with both specs
	// Content field contains user requirements, InfrastructureSpec is separate
	approvalPayload := &proto.ApprovalRequestPayload{
		ApprovalType:       proto.ApprovalTypeSpec,
		Content:            userSpec,           // User requirements only
		InfrastructureSpec: infrastructureSpec, // Infrastructure requirements (bootstrap) if any
		Reason:             "PM has completed specification and requests architect review",
		Context:            "Specification ready for validation and story generation",
		Confidence:         proto.ConfidenceHigh,
	}

	// Create REQUEST message
	requestMsg := &proto.AgentMsg{
		ID:        fmt.Sprintf("pm-spec-req-%d", time.Now().UnixNano()),
		Type:      proto.MsgTypeREQUEST,
		FromAgent: d.GetAgentID(),
		ToAgent:   "architect-001", // TODO: Get architect ID from config or dispatcher
		Payload:   proto.NewApprovalRequestPayload(approvalPayload),
	}

	// Send via dispatcher
	if err := d.dispatcher.DispatchMessage(requestMsg); err != nil {
		return fmt.Errorf("failed to dispatch REQUEST: %w", err)
	}

	// Store pending request ID for tracking
	d.SetStateData("pending_request_id", requestMsg.ID)
	d.logger.Info("📤 Sent spec approval REQUEST to architect (user: %d bytes, infrastructure: %d bytes, id: %s)",
		len(userSpec), len(infrastructureSpec), requestMsg.ID)

	return nil
}
