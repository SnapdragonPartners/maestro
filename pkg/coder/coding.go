package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/agent/llmerrors"
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

	// Get LLM response with MCP tool support.
	// Flush user buffer before LLM request
	if err := c.contextManager.FlushUserBuffer(); err != nil {
		return proto.StateError, false, fmt.Errorf("failed to flush user buffer: %w", err)
	}

	// Build messages starting with the coding prompt.
	messages := c.buildMessagesWithContext(prompt)

	req := agent.CompletionRequest{
		Messages:    messages,
		MaxTokens:   8192,                         // Increased for comprehensive code generation
		Temperature: llm.TemperatureDeterministic, // Deterministic output for coding
		Tools:       c.getCodingToolsForLLM(),     // Use state-specific tools
		ToolChoice:  "any",                        // Force tool use - coder must always use tools
	}

	// Use base agent retry mechanism.
	resp, llmErr := c.llmClient.Complete(ctx, req)
	if llmErr != nil {
		// Check if this is an empty response error that should trigger budget review
		if c.isEmptyResponseError(llmErr) {
			return c.handleEmptyResponseError(sm, prompt, req, StateCoding)
		}

		// For other errors, continue with normal error handling
		return proto.StateError, false, logx.Wrap(llmErr, "failed to get LLM coding response")
	}

	// Note: Empty response detection now handled universally by validation middleware
	// No need to check len(resp.ToolCalls) == 0 here
	// Note: Consecutive empty response tracking removed - middleware handles retries

	// Execute tool calls (MCP tools) - we know there are tool calls because of the check above
	filesCreated := c.executeMCPToolCalls(ctx, sm, resp.ToolCalls)

	// Add assistant response to context.
	// Handle LLM response with proper empty response logic
	if err := c.handleLLMResponse(resp); err != nil {
		// True empty response - this is an error condition
		return proto.StateError, false, err
	}

	// Check if completion was signaled via Effects pattern - highest priority completion signal.
	if completionData, exists := sm.GetStateValue(KeyCompletionSignaled); exists {
		if completionResult, ok := completionData.(*effect.CompletionResult); ok {
			c.logger.Info("🧑‍💻 Completion signaled via Effects - transitioning to %s", completionResult.TargetState)
			// Clear the completion signal for next iteration
			sm.SetStateData(KeyCompletionSignaled, nil)
			return completionResult.TargetState, false, nil
		}
	}

	// Check for implementation completion.
	if c.isImplementationComplete(resp.Content, filesCreated, sm) {
		c.logger.Info("🧑‍💻 Implementation appears complete, proceeding to testing")
		return StateTesting, false, nil
	}

	// Continue in coding state for next iteration.
	c.logger.Info("🧑‍💻 Coding iteration completed, continuing in CODING for more work")
	return StateCoding, false, nil
}

// executeMCPToolCalls executes tool calls using the MCP tool system.
func (c *Coder) executeMCPToolCalls(ctx context.Context, sm *agent.BaseStateMachine, toolCalls []agent.ToolCall) int {
	filesCreated := 0
	c.logger.Info("🧑‍💻 Executing %d MCP tool calls", len(toolCalls))

	for i := range toolCalls {
		toolCall := &toolCalls[i]
		c.logger.Info("Executing MCP tool: %s", toolCall.Name)

		// Handle todo management tools first (before other tool processing)
		if toolCall.Name == tools.ToolTodoComplete {
			index := utils.GetMapFieldOr[int](toolCall.Parameters, "index", -1)

			if err := c.handleTodoComplete(sm, index); err != nil {
				c.logger.Error("📋 [TODO] Failed to complete todo: %v", err)
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("Error completing todo: %v", err))
				continue
			}

			// Check if all todos are now complete
			allComplete := c.todoList != nil && c.todoList.GetCurrentTodo() == nil && c.todoList.GetCompletedCount() == c.todoList.GetTotalCount()

			if allComplete {
				c.contextManager.AddMessage(roleToolMessage, "✅ All todos completed! Create a brief story completion summary and call the 'done' tool to finish this story.")
			} else if index == -1 {
				c.contextManager.AddMessage(roleToolMessage, "✅ Current todo marked complete, advanced to next todo")
			} else {
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("✅ Todo at index %d marked complete", index))
			}
			continue
		}

		if toolCall.Name == tools.ToolTodosAdd {
			// Handle adding additional todos during coding
			todosAny := utils.GetMapFieldOr[[]any](toolCall.Parameters, "todos", []any{})
			if len(todosAny) == 0 {
				c.logger.Error("📋 [TODO] todos_add called without todos")
				c.contextManager.AddMessage(roleToolMessage, "Error: todos array required for todos_add")
				continue
			}

			// Initialize todoList if nil (can happen if todos_add is called before planning completes)
			if c.todoList == nil {
				c.logger.Warn("📋 [TODO] Todo list not initialized, creating new list")
				c.todoList = &TodoList{Items: []TodoItem{}}
			}

			c.logger.Info("📋 [TODO] Adding %d todos during CODING", len(todosAny))

			// Convert and append using the tool's validation
			tool, getErr := c.codingToolProvider.Get(tools.ToolTodosAdd)
			if getErr != nil {
				c.logger.Error("📋 [TODO] Failed to get todos_add tool: %v", getErr)
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("Error: %v", getErr))
				continue
			}

			result, execErr := tool.Exec(ctx, toolCall.Parameters)
			if execErr != nil {
				c.logger.Error("📋 [TODO] todos_add validation failed: %v", execErr)
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("Error: %v", execErr))
				continue
			}

			// Process validated result
			if resultMap, ok := result.(map[string]any); ok {
				if validatedTodos, ok := resultMap["todos"].([]string); ok {
					for _, todoStr := range validatedTodos {
						c.todoList.Items = append(c.todoList.Items, TodoItem{
							Description: todoStr,
							Completed:   false,
						})
					}
					sm.SetStateData("todo_list", c.todoList)
					c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("✅ Added %d new todos", len(validatedTodos)))
					c.logger.Info("📋 [TODO] ✅ Added %d todos to list (now %d total)", len(validatedTodos), len(c.todoList.Items))
				}
			}
			continue
		}

		if toolCall.Name == tools.ToolTodoUpdate {
			index := utils.GetMapFieldOr[int](toolCall.Parameters, "index", -1)
			description := utils.GetMapFieldOr[string](toolCall.Parameters, "description", "")

			if index < 0 {
				c.logger.Error("📋 [TODO] todo_update called with invalid index")
				c.contextManager.AddMessage(roleToolMessage, "Error: valid index required for todo_update")
				continue
			}

			if err := c.handleTodoUpdate(sm, index, description); err != nil {
				c.logger.Error("📋 [TODO] Failed to update todo: %v", err)
				c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("Error updating todo: %v", err))
				continue
			}

			action := "updated"
			if description == "" {
				action = "removed"
			}
			c.contextManager.AddMessage(roleToolMessage, fmt.Sprintf("✅ Todo at index %d %s", index, action))
			c.logger.Info("📋 [TODO] ✏️  Todo at index %d %s", index, action)
			continue
		}

		// Handle done tool using Effects pattern.
		if toolCall.Name == tools.ToolDone {
			c.logger.Info("🧑‍💻 Done tool called - signaling task completion")

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
					for i, todo := range incompleteTodos {
						errorMsg += fmt.Sprintf("\n  %d. %s", i+1, todo.Description)
					}
					c.contextManager.AddMessage(roleToolMessage, errorMsg)
					c.logger.Info("📋 [TODO] Blocking done: %s", errorMsg)
					continue // Skip this tool, continue processing others
				}
			}

			// Store completion details from done tool for later use in code review
			summary := utils.GetMapFieldOr[string](toolCall.Parameters, "summary", "")

			sm.SetStateData(KeyCompletionDetails, summary)
			c.logger.Info("🧑‍💻 Stored completion summary: %q", summary)

			// Create completion effect to signal immediate transition to TESTING
			completionEff := effect.NewCompletionEffect(
				"Implementation complete - proceeding to testing phase",
				StateTesting,
			)

			// Execute the completion effect
			result, err := c.ExecuteEffect(ctx, completionEff)
			if err != nil {
				c.logger.Error("🧑‍💻 Failed to execute completion effect: %v", err)
				c.addComprehensiveToolFailureToContext(*toolCall, err)
				continue
			}

			// Process the completion result
			if completionResult, ok := result.(*effect.CompletionResult); ok {
				// Store the completion result for the state machine to use
				sm.SetStateData(KeyCompletionSignaled, completionResult)
				c.logger.Info("🧑‍💻 Completion effect executed successfully - target state: %s", completionResult.TargetState)
			} else {
				c.logger.Error("🧑‍💻 Invalid completion result type: %T", result)
			}

			// Still execute the done tool to return success message to LLM
		}

		// Handle ask_question tool using Effects pattern.
		if toolCall.Name == tools.ToolAskQuestion {
			// Extract question details from tool arguments.
			question := utils.GetMapFieldOr[string](toolCall.Parameters, "question", "")
			context := utils.GetMapFieldOr[string](toolCall.Parameters, "context", "")
			urgency := utils.GetMapFieldOr[string](toolCall.Parameters, "urgency", "medium")

			if question == "" {
				c.logger.Error("Ask question tool called without question parameter")
				continue
			}

			// Store coding context before asking question.
			c.storeCodingContext(sm)

			// Create question effect
			eff := effect.NewQuestionEffect(question, context, urgency, string(StateCoding))

			// Set story_id for dispatcher validation
			storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
			eff.StoryID = storyID

			c.logger.Info("🧑‍💻 Asking question")

			// Execute the question effect (blocks until answer received)
			result, err := c.ExecuteEffect(ctx, eff)
			if err != nil {
				c.logger.Error("🧑‍💻 Failed to get answer: %v", err)
				// Add error to context for LLM to handle
				c.addComprehensiveToolFailureToContext(*toolCall, err)
				continue
			}

			// Process the answer
			if questionResult, ok := result.(*effect.QuestionResult); ok {
				// Answer received from architect (logged to database only)

				// Add the Q&A to context so the LLM can see it
				qaContent := fmt.Sprintf("Question: %s\nAnswer: %s", question, questionResult.Answer)
				c.contextManager.AddMessage("architect-answer", qaContent)

				// Continue with coding using the answer
			} else {
				c.logger.Error("🧑‍💻 Invalid question result type: %T", result)
			}
		}

		// Get tool from ToolProvider and execute.
		tool, err := c.codingToolProvider.Get(toolCall.Name)
		if err != nil {
			c.logger.Error("Tool not found in ToolProvider: %s", toolCall.Name)
			// Add tool failure to context for LLM to react.
			c.addComprehensiveToolFailureToContext(*toolCall, err)
			continue
		}

		// Add agent_id to context for tools that need it (like chat tools)
		toolCtx := context.WithValue(ctx, tools.AgentIDContextKey, c.agentID)

		// Measure execution time and log tool execution to database
		startTime := time.Now()
		result, err := tool.Exec(toolCtx, toolCall.Parameters)
		duration := time.Since(startTime)

		// Log tool execution to database (fire-and-forget)
		c.logToolExecution(toolCall, result, err, duration)

		if err != nil {
			// Tool execution failures are recoverable - add comprehensive error to context for LLM to react.
			if toolCall.Name == tools.ToolShell {
				// For shell tool, provide cleaner logging without Docker details
				c.logger.Info("Shell command failed: %v", err)
			} else {
				c.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
			}
			c.addComprehensiveToolFailureToContext(*toolCall, err)
			continue // Continue processing other tool calls
		}

		// Track file creation for completion detection.
		// Note: Using shell commands or other tools to create files
		filesCreated++

		// Add tool execution results to context so Claude can see them.
		c.addToolResultToContext(*toolCall, result)
		c.logger.Info("MCP tool %s executed successfully", toolCall.Name)
	}

	return filesCreated
}

// isImplementationComplete checks if the current implementation appears complete.
func (c *Coder) isImplementationComplete(responseContent string, filesCreated int, sm *agent.BaseStateMachine) bool {
	// Extract todo from state machine for completion assessment.
	planTodos := utils.GetStateValueOr[[]any](sm, string(stateDataKeyPlanTodos), []any{})

	// Convert to string slice.
	todos := make([]string, 0, len(planTodos))
	for _, todo := range planTodos {
		if todoStr, ok := todo.(string); ok {
			todos = append(todos, todoStr)
		}
	}

	c.logger.Debug("🧑‍💻 Checking completion: %d files created, %d todos planned", filesCreated, len(todos))

	// Check if Claude explicitly indicates completion.
	completionIndicators := []string{
		"implementation is complete",
		"implementation is now complete",
		"all requirements have been implemented",
		"task is complete",
		"story is complete",
		"ready for testing",
		"proceed to testing",
		"implementation finished",
		"all todos completed",
		"all tasks completed",
		"nothing more to implement",
	}

	lowerResponse := strings.ToLower(responseContent)
	for _, indicator := range completionIndicators {
		if strings.Contains(lowerResponse, indicator) {
			c.logger.Info("🧑‍💻 Completion detected via explicit indicator: '%s'", indicator)
			return true
		}
	}

	// Check if sufficient work has been done (heuristic).
	if filesCreated >= 3 && len(todos) > 0 {
		// Check if most todos appear to be addressed in response.
		addressedCount := 0
		for _, todo := range todos {
			// Simple heuristic: check if key terms from todo appear in response.
			todoWords := strings.Fields(strings.ToLower(todo))
			for _, word := range todoWords {
				if len(word) > 3 && strings.Contains(lowerResponse, word) {
					addressedCount++
					break
				}
			}
		}

		completionRatio := float64(addressedCount) / float64(len(todos))
		if completionRatio >= 0.7 { // 70% of todos addressed
			c.logger.Info("🧑‍💻 Completion detected via heuristic: %d/%d todos addressed (%.1f%%), %d files created",
				addressedCount, len(todos), completionRatio*100, filesCreated)
			return true
		}
	}

	return false
}

// storeCodingContext stores the current coding context.
func (c *Coder) storeCodingContext(sm *agent.BaseStateMachine) {
	context := map[string]any{
		"coding_progress": c.getCodingProgress(),
		KeyFilesCreated:   c.getFilesCreated(),
		"current_task":    c.getCurrentTask(),
		"timestamp":       time.Now().UTC(),
	}
	sm.SetStateData(KeyCodingContextSaved, context)
	c.logger.Debug("🧑‍💻 Stored coding context for QUESTION transition")
}

// Placeholder helper methods for coding context management (to be enhanced as needed).
func (c *Coder) getCodingProgress() any { return map[string]any{} }
func (c *Coder) getFilesCreated() any   { return []string{} }
func (c *Coder) getCurrentTask() any    { return map[string]any{} }

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

	// Add requesting permission message to preserve alternation
	c.contextManager.AddAssistantMessage("requesting permission to continue")

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
