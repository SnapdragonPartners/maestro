package coder

import (
	"context"
	"errors"
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
		sm.SetStateData("budget_review_effect", budgetReviewEff)
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

	// Use toolloop for LLM iteration with coding tools
	loop := toolloop.New(c.LLMClient, c.logger)

	//nolint:dupl // Similar config in planning.go - intentional per-state configuration
	cfg := &toolloop.Config[CodingResult]{
		ContextManager: c.contextManager,
		InitialPrompt:  "", // Prompt already in context via ResetForNewTemplate
		ToolProvider:   c.codingToolProvider,
		MaxIterations:  maxCodingIterations,
		MaxTokens:      8192, // Increased for comprehensive code generation
		AgentID:        c.agentID,
		DebugLogging:   false,
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			return c.checkCodingTerminal(ctx, sm, calls, results)
		},
		ExtractResult: ExtractCodingResult,
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("coding_%s", utils.GetStateValueOr[string](sm, KeyStoryID, "unknown")),
			SoftLimit: maxCodingIterations - 2, // Warn 2 iterations before limit
			HardLimit: maxCodingIterations,
			OnHardLimit: func(_ context.Context, key string, count int) error {
				c.logger.Info("⚠️  Coding reached max iterations (%d, key: %s), triggering budget review", count, key)
				budgetEff := effect.NewBudgetReviewEffect(
					fmt.Sprintf("Maximum coding iterations (%d) reached", maxCodingIterations),
					"Coding workflow needs additional iterations to complete",
					string(StateCoding),
				)
				// Set story ID for dispatcher validation
				budgetEff.StoryID = utils.GetStateValueOr[string](sm, KeyStoryID, "")
				sm.SetStateData("budget_review_effect", budgetEff)
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	signal, result, err := toolloop.Run(loop, ctx, cfg)
	if err != nil {
		// Check if this is an iteration limit error (normal escalation path)
		var iterErr *toolloop.IterationLimitError
		if errors.As(err, &iterErr) {
			// OnHardLimit already stored BudgetReviewEffect in state
			c.logger.Info("📊 Iteration limit reached (%d iterations), transitioning to BUDGET_REVIEW", iterErr.Iteration)
			return StateBudgetReview, false, nil
		}

		// Check if this is an empty response error
		if c.isEmptyResponseError(err) {
			req := agent.CompletionRequest{MaxTokens: 8192}
			return c.handleEmptyResponseError(sm, prompt, req, StateCoding)
		}
		return proto.StateError, false, logx.Wrap(err, "toolloop execution failed")
	}

	// Log extracted result for visibility
	if len(result.TodosCompleted) > 0 {
		c.logger.Info("✅ Coding iteration completed %d todos", len(result.TodosCompleted))
	}

	// Handle terminal signals
	switch signal {
	case string(StateBudgetReview):
		return StateBudgetReview, false, nil
	case string(StateQuestion):
		return StateQuestion, false, nil
	case "TESTING":
		return StateTesting, false, nil
	case "":
		// No signal, continue coding
		c.logger.Info("🧑‍💻 Coding iteration completed, continuing in CODING")
		return StateCoding, false, nil
	default:
		c.logger.Warn("Unknown signal from coding toolloop: %s", signal)
		return StateCoding, false, nil
	}
}

// checkCodingTerminal examines tool calls and results for terminal signals during coding.
func (c *Coder) checkCodingTerminal(_ context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, _ []any) string {
	for i := range calls {
		toolCall := &calls[i]

		// Handle todo_complete tool - mark todo as complete
		if toolCall.Name == tools.ToolTodoComplete {
			index := utils.GetMapFieldOr[int](toolCall.Parameters, "index", -1)

			if err := c.handleTodoComplete(sm, index); err != nil {
				c.logger.Error("📋 [TODO] Failed to complete todo: %v", err)
				c.contextManager.AddMessage("tool-error", fmt.Sprintf("Error completing todo: %v", err))
				continue
			}

			if index == -1 {
				c.contextManager.AddMessage("tool", "Current todo marked complete, advanced to next todo")
			} else {
				c.contextManager.AddMessage("tool", fmt.Sprintf("Todo at index %d marked complete", index))
			}
			continue
		}

		// Handle todo_update tool - update or remove todo by index
		if toolCall.Name == tools.ToolTodoUpdate {
			index := utils.GetMapFieldOr[int](toolCall.Parameters, "index", -1)
			description := utils.GetMapFieldOr[string](toolCall.Parameters, "description", "")

			if index < 0 {
				c.logger.Error("📋 [TODO] todo_update called with invalid index")
				c.contextManager.AddMessage("tool-error", "Error: valid index required for todo_update")
				continue
			}

			if err := c.handleTodoUpdate(sm, index, description); err != nil {
				c.logger.Error("📋 [TODO] Failed to update todo: %v", err)
				c.contextManager.AddMessage("tool-error", fmt.Sprintf("Error updating todo: %v", err))
				continue
			}

			action := "updated"
			if description == "" {
				action = "removed"
			}
			c.contextManager.AddMessage("tool", fmt.Sprintf("Todo at index %d %s", index, action))
			c.logger.Info("✏️  Todo at index %d %s", index, action)
			continue
		}

		// Check for ask_question tool - transition to QUESTION state
		if toolCall.Name == tools.ToolAskQuestion {
			// Extract question details from tool call parameters
			question := utils.GetMapFieldOr[string](toolCall.Parameters, "question", "")
			contextStr := utils.GetMapFieldOr[string](toolCall.Parameters, "context", "")
			urgency := utils.GetMapFieldOr[string](toolCall.Parameters, "urgency", "medium")

			if question == "" {
				c.logger.Error("Ask question tool called without question parameter")
				continue
			}

			// Store question data in state for QUESTION state to use
			sm.SetStateData(KeyPendingQuestion, map[string]any{
				"question": question,
				"context":  contextStr,
				"urgency":  urgency,
				"origin":   string(StateCoding),
			})

			c.logger.Info("🧑‍💻 Coding detected ask_question, transitioning to QUESTION state")
			return string(StateQuestion) // Signal state transition
		}

		// Check for done tool - validate todos and transition to TESTING
		if toolCall.Name == tools.ToolDone {
			c.logger.Info("🧑‍💻 Done tool detected - validating todos before transition")

			// Check if all todos are complete before allowing story completion
			if c.todoList != nil {
				incompleteTodos := []TodoItem{}
				for _, todo := range c.todoList.Items {
					if !todo.Completed {
						incompleteTodos = append(incompleteTodos, todo)
					}
				}

				if len(incompleteTodos) > 0 {
					// Block completion - tell agent to complete todos first
					c.logger.Info("🧑‍💻 Done tool blocked: %d todos not marked complete", len(incompleteTodos))
					errorMsg := fmt.Sprintf("Cannot mark story as done: %d todos are not marked complete. If this work is already completed, use the todo_complete tool to mark them complete before marking the story as done.\n\nIncomplete todos:", len(incompleteTodos))
					for idx, todo := range incompleteTodos {
						errorMsg += fmt.Sprintf("\n  %d. %s", idx+1, todo.Description)
					}
					c.contextManager.AddMessage("tool-error", errorMsg)
					c.logger.Info("📋 [TODO] Blocking done: %s", errorMsg)
					continue // Don't signal transition, continue loop
				}
			}

			// All todos complete - store summary and signal transition
			summary := utils.GetMapFieldOr[string](toolCall.Parameters, "summary", "")
			sm.SetStateData(KeyCompletionDetails, summary)
			c.logger.Info("🧑‍💻 Done tool validated - transitioning to TESTING with summary: %q", summary)

			return "TESTING" // Signal transition to TESTING
		}
	}

	// No terminal signal, continue loop
	return ""
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
	sm.SetStateData("budget_review_effect", budgetReviewEff)

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
