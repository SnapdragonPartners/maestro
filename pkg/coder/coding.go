package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llmerrors"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handleCoding processes the CODING state with priority-based work handling.
func (c *Coder) handleCoding(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Check for merge conflict (highest priority)
	if conflictData, exists := sm.GetStateValue(KeyMergeConflictDetails); exists {
		c.logger.Info("🧑‍💻 Handling merge conflict in CODING state")
		return c.handleMergeConflictCoding(ctx, sm, conflictData)
	}

	// Check for code review feedback (second priority)
	if reviewData, exists := sm.GetStateValue(KeyCodeReviewRejectionFeedback); exists {
		c.logger.Info("🧑‍💻 Handling code review feedback in CODING state")
		return c.handleCodeReviewCoding(ctx, sm, reviewData)
	}

	// Check for test failures (third priority)
	if testData, exists := sm.GetStateValue(KeyTestFailureOutput); exists {
		c.logger.Info("🧑‍💻 Handling test failures in CODING state")
		return c.handleTestFixCoding(ctx, sm, testData)
	}

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

// handleMergeConflictCoding handles merge conflict resolution during coding.
func (c *Coder) handleMergeConflictCoding(ctx context.Context, sm *agent.BaseStateMachine, conflictData any) (proto.State, bool, error) {
	// Clear merge conflict data after handling.
	sm.SetStateData(KeyMergeConflictDetails, nil)

	// Execute coding with merge conflict context.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario":      "merge_conflict",
		"conflict_data": conflictData,
		"message":       "Resolve the merge conflicts and continue implementation",
	})
}

// handleCodeReviewCoding handles code review feedback during coding.
func (c *Coder) handleCodeReviewCoding(ctx context.Context, sm *agent.BaseStateMachine, reviewData any) (proto.State, bool, error) {
	// Clear review feedback data after handling.
	sm.SetStateData(KeyCodeReviewRejectionFeedback, nil)

	// Execute coding with review feedback context.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario":    "code_review_feedback",
		"review_data": reviewData,
		"message":     "Address the code review feedback and continue implementation",
	})
}

// handleTestFixCoding handles test failure fixes during coding.
func (c *Coder) handleTestFixCoding(ctx context.Context, sm *agent.BaseStateMachine, testData any) (proto.State, bool, error) {
	// Clear test failure data after handling.
	sm.SetStateData(KeyTestFailureOutput, nil)

	// Execute coding with test failure context.
	return c.executeCodingWithTemplate(ctx, sm, map[string]any{
		"scenario":  "test_failures",
		"test_data": testData,
		"message":   "Fix the test failures and continue implementation",
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
	enhancedTemplateData := &templates.TemplateData{
		TaskContent:       taskContent,
		Plan:              plan, // Include plan from PLANNING state
		WorkDir:           c.workDir,
		ToolDocumentation: c.codingToolProvider.GenerateToolDocumentation(),
		Extra: map[string]any{
			"story_type": storyType, // Include story type for template logic
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
		Messages:  messages,
		MaxTokens: 8192,                     // Increased for comprehensive code generation
		Tools:     c.getCodingToolsForLLM(), // Use state-specific tools
	}

	// Use base agent retry mechanism.
	resp, llmErr := c.llmClient.Complete(ctx, req)
	if llmErr != nil {
		// Check if this is an empty response error that should trigger budget review
		if c.isEmptyResponseError(llmErr) {
			return c.handleEmptyResponseForBudgetReview(ctx, sm, prompt, req)
		}

		// For other errors, continue with normal error handling
		return proto.StateError, false, logx.Wrap(llmErr, "failed to get LLM coding response")
	}

	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		// This is a fallback check for cases where the LLM client didn't catch empty response
		return c.handleEmptyResponseForBudgetReview(ctx, sm, prompt, req)
	}

	// Reset consecutive empty response counter on successful response
	sm.SetStateData(KeyConsecutiveEmptyResponses, 0)
	c.logger.Debug("🧑‍💻 Successful LLM response - reset consecutive empty counter")

	// Execute tool calls (MCP tools).
	var filesCreated int
	if len(resp.ToolCalls) > 0 {
		filesCreated = c.executeMCPToolCalls(ctx, sm, resp.ToolCalls)
	} else {
		// No tool calls found - this shouldn't happen with proper MCP tool usage
		c.logger.Warn("🧑‍💻 No tool calls found in LLM response - expecting MCP tools for file operations")
		filesCreated = 0
	}

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

		// Handle done tool using Effects pattern.
		if toolCall.Name == tools.ToolDone {
			c.logger.Info("🧑‍💻 Done tool called - signaling task completion")

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

		result, err := tool.Exec(ctx, toolCall.Parameters)
		if err != nil {
			// Tool execution failures are recoverable - add comprehensive error to context for LLM to react.
			c.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
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

// handleEmptyResponseForBudgetReview handles empty LLM responses with two-tier approach.
// First empty response: provide guidance and stay in CODING.
// Second consecutive empty response: escalate to budget review.
func (c *Coder) handleEmptyResponseForBudgetReview(_ context.Context, sm *agent.BaseStateMachine, prompt string, req agent.CompletionRequest) (proto.State, bool, error) {
	c.logEmptyLLMResponse(prompt, req)

	// Check consecutive empty response count
	consecutiveCount := utils.GetStateValueOr[int](sm, KeyConsecutiveEmptyResponses, 0)

	// Increment consecutive empty response counter
	sm.SetStateData(KeyConsecutiveEmptyResponses, consecutiveCount+1)

	if consecutiveCount == 0 {
		// First empty response: provide guidance and continue in CODING
		c.logger.Info("🧑‍💻 First empty response - providing guidance on completion")

		// Add placeholder assistant message to maintain alternation
		placeholderResponse := sanitizeEmptyResponse("")
		c.contextManager.AddAssistantMessage(placeholderResponse)

		// Add guidance user message
		guidanceMessage := "If you are done working and ready for testing and review, use the 'done' tool. If you are stuck for any other reason, use the 'ask_question' tool to get guidance on how to proceed."
		c.contextManager.AddMessage("empty-response-guidance", guidanceMessage)

		// Flush the user buffer so the guidance message is immediately available
		if err := c.contextManager.FlushUserBuffer(); err != nil {
			c.logger.Error("🧑‍💻 Failed to flush user buffer after adding guidance: %v", err)
		}

		c.logger.Info("🧑‍💻 Added completion guidance, continuing in CODING state")
		return StateCoding, false, nil
	}

	// Second or subsequent empty response: escalate to budget review
	c.logger.Info("🧑‍💻 Consecutive empty response #%d - escalating to budget review", consecutiveCount+1)

	// Create empty response budget review effect
	budgetReviewEff := effect.NewEmptyResponseBudgetReviewEffect(string(StateCoding), consecutiveCount+1)

	// Set story ID for dispatcher validation
	storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
	budgetReviewEff.StoryID = storyID

	// Store origin state and effect for BUDGET_REVIEW state to execute
	sm.SetStateData(KeyOrigin, string(StateCoding))
	sm.SetStateData("budget_review_effect", budgetReviewEff)

	// Add requesting permission message to preserve alternation
	c.contextManager.AddAssistantMessage("requesting permission to continue")

	// Transition to BUDGET_REVIEW state to wait for architect response
	return StateBudgetReview, false, nil
}
