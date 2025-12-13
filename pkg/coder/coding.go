package coder

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleCoding processes the CODING state with priority-based work handling.
func (c *Coder) handleCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Default: Continue with initial coding
	return c.handleInitialCoding(ctx, sm)
}

// handleInitialCoding handles the main coding workflow.
func (c *Coder) handleInitialCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	const maxCodingIterations = 8
	if budgetReviewEff, budgetExceeded := c.checkLoopBudget(sm, string(stateDataKeyCodingIterations), maxCodingIterations, StateCoding); budgetExceeded {
		c.logger.Info("Coding budget exceeded, triggering BUDGET_REVIEW")
		// Store effect for BUDGET_REVIEW state to execute
		sm.SetStateData(KeyBudgetReviewEffect, budgetReviewEff)
		return StateBudgetReview, false, nil
	}

	// Continue coding with main template
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario": "initial_coding",
		"message":  "Continue with code implementation based on your plan",
	})
}

// executeCodingWithTemplate is the shared implementation for all coding scenarios.
func (c *Coder) executeCodingWithTemplate(ctx context.Context, sm *agent.BaseStateMachine, templateData map[string]any) (proto.State, bool, error) {
	const maxCodingIterations = 8
	logx.DebugState(ctx, "coder", "enter", string(StateCoding))

	// Get story type for template selection
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	// Create ToolProvider for this coding session
	if c.codingToolProvider == nil {
		c.codingToolProvider = c.createCodingToolProvider(storyType)
		c.logger.Debug("Created coding ToolProvider for story type: %s", storyType)
	}

	// Select appropriate coding template based on story type
	var codingTemplate templates.StateTemplate
	if storyType == string(proto.StoryTypeDevOps) {
		codingTemplate = templates.DevOpsCodingTemplate
	} else {
		codingTemplate = templates.AppCodingTemplate
	}

	// Get task content.
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Get plan from state data (stored during PLANNING phase).
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")

	// Create enhanced template data with state-specific tool documentation.
	// Get container information from config
	var containerName, containerDockerfile string
	if cfg, err := config.GetConfig(); err == nil && cfg.Container != nil {
		containerName = cfg.Container.Name
		containerDockerfile = cfg.Container.Dockerfile
		c.logger.Debug("🐳 Coding template container info - Name: '%s', Dockerfile: '%s'", containerName, containerDockerfile)
	} else {
		c.logger.Debug("🐳 Coding template container info not available: %v", err)
	}

	// Get todo status for template
	todoStatus := ""
	if c.todoList != nil {
		todoStatus = c.getTodoListStatus()
	}

	enhancedTemplateData := &templates.TemplateData{
		TaskContent:         taskContent,
		Plan:                plan, // Include plan from PLANNING state
		WorkDir:             c.workDir,
		ToolDocumentation:   c.codingToolProvider.GenerateToolDocumentation(),
		ContainerName:       containerName,
		ContainerDockerfile: containerDockerfile,
		Extra: map[string]any{
			"story_type": storyType,  // Include story type for template logic
			"TodoStatus": todoStatus, // Include current todo status
		},
	}

	// Merge in additional template data from caller.
	for key, value := range templateData {
		enhancedTemplateData.Extra[key] = value
	}

	// Render enhanced coding template.
	if c.renderer == nil {
		return proto.StateError, false, logx.Errorf("template renderer not available")
	}
	prompt, err := c.renderer.RenderWithUserInstructions(codingTemplate, enhancedTemplateData, c.workDir, "CODER")
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to render coding template")
	}

	// Reset context for new template (only if template type changed)
	// ResetForNewTemplate will preserve context if template name unchanged
	templateName := fmt.Sprintf("coding-%s", codingTemplate)
	c.contextManager.ResetForNewTemplate(templateName, prompt)

	// Log the rendered prompt for debugging
	c.logger.Info("🧑‍💻 Starting coding phase for story_type '%s'", storyType)

	// Get done tool and wrap it as terminal tool
	doneTool, err := c.codingToolProvider.Get(tools.ToolDone)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to get done tool")
	}
	terminalTool := doneTool

	// Get all general tools (everything except done)
	// ask_question is now a general tool that returns ProcessEffect
	allTools := c.codingToolProvider.List()
	generalTools := make([]tools.Tool, 0, len(allTools)-1)
	//nolint:gocritic // ToolMeta is 80 bytes but value semantics preferred here
	for _, meta := range allTools {
		if meta.Name != tools.ToolDone {
			tool, err := c.codingToolProvider.Get(meta.Name)
			if err != nil {
				return proto.StateError, false, logx.Wrap(err, fmt.Sprintf("failed to get tool %s", meta.Name))
			}
			generalTools = append(generalTools, tool)
		}
	}

	// Use toolloop for LLM iteration with coding tools
	loop := toolloop.New(c.LLMClient, c.logger)

	//nolint:dupl // Similar config in planning.go - intentional per-state configuration
	cfg := &toolloop.Config[CodingResult]{
		ContextManager: c.contextManager,
		InitialPrompt:  "", // Prompt already in context via ResetForNewTemplate
		GeneralTools:   generalTools,
		TerminalTool:   terminalTool,
		MaxIterations:  maxCodingIterations,
		MaxTokens:      8192, // Increased for comprehensive code generation
		AgentID:        c.GetAgentID(),
		DebugLogging:   config.GetDebugLLMMessages(),
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("coding_%s", utils.GetStateValueOr[string](sm, KeyStoryID, "unknown")),
			SoftLimit: maxCodingIterations - 2, // Warn 2 iterations before limit
			HardLimit: maxCodingIterations,
			OnHardLimit: func(_ context.Context, key string, count int) error {
				c.logger.Info("⚠️  Coding reached max iterations (%d, key: %s), triggering budget review", count, key)

				// Render full budget review content with template (same as checkLoopBudget)
				content := c.getBudgetReviewContent(sm, StateCoding, count, maxCodingIterations)
				if content == "" {
					return logx.Errorf("failed to generate budget review content - cannot proceed without proper context for architect")
				}

				budgetEff := effect.NewBudgetReviewEffect(
					content,
					"Coding workflow needs additional iterations to complete",
					string(StateCoding),
				)
				// Set story ID for dispatcher validation
				budgetEff.StoryID = utils.GetStateValueOr[string](sm, KeyStoryID, "")
				sm.SetStateData(KeyBudgetReviewEffect, budgetEff)
				// Store origin state so budget review knows where to return
				sm.SetStateData(KeyOrigin, string(StateCoding))
				c.logger.Info("🔍 Toolloop iteration limit: stored origin=%q in state data", string(StateCoding))
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Switch on outcome kind first
	switch out.Kind {
	case toolloop.OutcomeProcessEffect:
		// Tool returned ProcessEffect to pause the loop for async effect processing
		c.logger.Info("🔔 Tool returned ProcessEffect with signal: %s", out.Signal)

		// Route based on signal (state constant)
		switch out.Signal {
		case string(proto.StateQuestion):
			// ask_question was called - extract question data from ProcessEffect
			if err := c.storePendingQuestionFromProcessEffect(sm, out); err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to store pending question")
			}
			c.logger.Info("🧑‍💻 Question submitted, transitioning to QUESTION state")
			return StateQuestion, false, nil
		case tools.SignalTesting:
			// done tool was called - extract summary from ProcessEffect.Data
			effectData, ok := utils.SafeAssert[map[string]any](out.EffectData)
			if !ok {
				return proto.StateError, false, logx.Errorf("TESTING effect data is not map[string]any: %T", out.EffectData)
			}
			summary := utils.GetMapFieldOr[string](effectData, "summary", "")
			c.logger.Info("🧑‍💻 Done tool detected: %s", summary)
			c.logger.Info("🧑‍💻 Advancing to TESTING state")
			return StateTesting, false, nil
		default:
			return proto.StateError, false, logx.Errorf("unknown ProcessEffect signal: %s", out.Signal)
		}

	case toolloop.OutcomeIterationLimit:
		// OnHardLimit already stored BudgetReviewEffect in state
		c.logger.Info("📊 Iteration limit reached (%d iterations), transitioning to BUDGET_REVIEW", out.Iteration)
		return StateBudgetReview, false, nil

	case toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
		// Check if this is an empty response error
		if c.isEmptyResponseError(out.Err) {
			req := agent.CompletionRequest{MaxTokens: 8192}
			return c.handleEmptyResponseError(sm, prompt, req, StateCoding)
		}
		return proto.StateError, false, logx.Wrap(out.Err, "toolloop execution failed")

	case toolloop.OutcomeNoToolTwice:
		// LLM failed to use tools - treat as error
		return proto.StateError, false, logx.Wrap(out.Err, "LLM did not use tools in coding")

	default:
		return proto.StateError, false, logx.Errorf("unknown toolloop outcome kind: %v", out.Kind)
	}
}

// storePendingQuestionFromProcessEffect stores question details from ProcessEffect.Data in state for QUESTION state.
func (c *Coder) storePendingQuestionFromProcessEffect(sm *agent.BaseStateMachine, out toolloop.Outcome[CodingResult]) error {
	// Extract question data from ProcessEffect.Data
	effectData, ok := out.EffectData.(map[string]string)
	if !ok {
		return logx.Errorf("ProcessEffect.Data is not map[string]string: %T", out.EffectData)
	}

	question, ok := effectData["question"]
	if !ok || question == "" {
		return logx.Errorf("ProcessEffect.Data missing 'question' field")
	}

	context := effectData["context"] // Optional, may be empty

	// Store in state for QUESTION state to use
	questionData := map[string]any{
		"question": question,
		"context":  context,
		"origin":   string(StateCoding),
	}

	sm.SetStateData(KeyPendingQuestion, questionData)
	c.logger.Info("🧑‍💻 Stored pending question: %s", question)
	return nil
}

// isEmptyResponseError checks if an error is an empty response error that should trigger budget review.
func (c *Coder) isEmptyResponseError(err error) bool {
	return llmerrors.Is(err, llmerrors.ErrorTypeEmptyResponse)
}

// handleEmptyResponseError handles empty response errors with budget review escalation and loop prevention.
//
//nolint:gocritic // 80 bytes is reasonable for error handling
func (c *Coder) handleEmptyResponseError(sm *agent.BaseStateMachine, prompt string, req agent.CompletionRequest, originState proto.State) (proto.State, bool, error) {
	// Log debugging info for troubleshooting
	c.logEmptyLLMResponse(prompt, req)

	// Check if we've already attempted budget review for empty response
	if utils.GetStateValueOr[bool](sm, KeyEmptyResponse, false) {
		c.logger.Error("🧑‍💻 Second empty response after budget review - transitioning to ERROR")
		return proto.StateError, false, fmt.Errorf("received empty response even after budget review guidance")
	}

	// Set flag to track that we're handling empty response
	sm.SetStateData(KeyEmptyResponse, true)

	// Create empty response budget review effect
	budgetReviewEff := effect.NewEmptyResponseBudgetReviewEffect(string(originState), 1)

	// Set story ID for dispatcher validation
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	budgetReviewEff.StoryID = storyID

	// Store origin state and effect for BUDGET_REVIEW state to execute
	sm.SetStateData(KeyOrigin, string(originState))
	c.logger.Info("🔍 Empty response: stored origin=%q in state data", string(originState))
	sm.SetStateData(KeyBudgetReviewEffect, budgetReviewEff)

	// Note: Don't add fabricated assistant messages - only LLM responses should be assistant messages
	// The context will naturally have proper alternation from the previous LLM call

	c.logger.Info("🧑‍💻 Empty response in %s - escalating to budget review", originState)
	return StateBudgetReview, false, nil
}

// logEmptyLLMResponse logs comprehensive debugging info for empty LLM responses.
//
//nolint:gocritic // 80 bytes is reasonable for logging
func (c *Coder) logEmptyLLMResponse(prompt string, req agent.CompletionRequest) {
	// Log the entire prompt and context for debugging empty responses
	c.logger.Error("🚨 EMPTY RESPONSE FROM LLM - DEBUGGING INFO:")
	c.logger.Error("📝 Complete prompt sent to LLM:")
	c.logger.Error("%s", strings.Repeat("=", 80))
	c.logger.Error("%s", prompt)
	c.logger.Error("%s", strings.Repeat("=", 80))

	if c.contextManager != nil {
		messages := c.contextManager.GetMessages()
		c.logger.Error("💬 Context Manager Messages (%d total):", len(messages))
		for i := range messages {
			msg := &messages[i]
			c.logger.Error("  [%d] Role: %s, Content: %s", i, msg.Role, msg.Content)
		}
	} else {
		c.logger.Error("💬 Context Manager: nil")
	}

	c.logger.Error("🔍 Request Details:")
	c.logger.Error("  - Temperature: %v", req.Temperature)
	c.logger.Error("  - Max Tokens: %v", req.MaxTokens)
	c.logger.Error("  - Tool Choice: %v", req.ToolChoice)
	c.logger.Error("  - Tools Count: %d", len(req.Tools))
	c.logger.Error("🚨 END EMPTY RESPONSE DEBUG")
}
