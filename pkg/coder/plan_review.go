package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handlePlanReview processes the PLAN_REVIEW state using the Effects pattern.
func (c *Coder) handlePlanReview(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Determine approval type based on pending request
	var approvalType proto.ApprovalType = proto.ApprovalTypePlan // default

	if c.pendingApprovalRequest != nil {
		approvalType = c.pendingApprovalRequest.Type
	}

	// Create the appropriate approval effect based on type
	var eff effect.Effect
	switch approvalType {
	case proto.ApprovalTypePlan:
		planContent := c.getPlanApprovalContent(sm)
		storyID := c.GetStoryID()                                               // Use the getter method I created
		eff = effect.NewPlanApprovalEffectWithStoryID(planContent, "", storyID) // Task content is now in planContent template
		c.contextManager.AddAssistantMessage("Plan review phase: requesting architect approval")

	case proto.ApprovalTypeCompletion:
		completionContent := c.getCompletionContent(sm)
		storyID := c.GetStoryID()                                                           // Use the getter method I created
		eff = effect.NewCompletionApprovalEffectWithStoryID(completionContent, "", storyID) // Files created is now in completionContent template
		c.contextManager.AddAssistantMessage("Completion review phase: requesting architect approval")

	default:
		return proto.StateError, false, logx.Errorf("unsupported approval type: %s", approvalType)
	}

	// Execute the approval effect
	c.logger.Info("🧑‍💻 Requesting %s approval from architect", approvalType)
	result, err := c.ExecuteEffect(ctx, eff)
	if err != nil {
		c.logger.Error("🧑‍💻 Failed to get %s approval: %v", approvalType, err)
		return proto.StateError, false, logx.Wrap(err, fmt.Sprintf("failed to get %s approval", approvalType))
	}

	// Convert result to ApprovalResult
	approvalResult, ok := result.(*effect.ApprovalResult)
	if !ok {
		return proto.StateError, false, logx.Errorf("unexpected result type from approval effect: %T", result)
	}

	// Clear pending request since we have the result
	c.pendingApprovalRequest = nil
	sm.SetStateData(KeyPlanReviewCompletedAt, time.Now().UTC())

	// Process the approval result
	switch approvalResult.Status {
	case proto.ApprovalStatusApproved:
		c.logger.Info("🧑‍💻 %s approved by architect", approvalType)
		return c.handlePlanReviewApproval(ctx, sm, approvalType)

	case proto.ApprovalStatusNeedsChanges:
		c.logger.Info("🧑‍💻 %s needs changes, returning to appropriate state with feedback", approvalType)

		// For completion approval (when all todos were complete), add feedback as new todo
		if approvalType == proto.ApprovalTypeCompletion && c.todoList != nil {
			allComplete := c.todoList.GetCurrentTodo() == nil && c.todoList.GetCompletedCount() == c.todoList.GetTotalCount()
			if allComplete && approvalResult.Feedback != "" {
				feedbackTodo := fmt.Sprintf("Address architect feedback: %s", approvalResult.Feedback)
				c.todoList.AddTodo(feedbackTodo, -1) // -1 means append to end
				c.logger.Info("📋 Added architect feedback as new todo")
				sm.SetStateData("todo_list", c.todoList)
			}
		}

		// Add feedback to context for visibility
		if approvalResult.Feedback != "" {
			c.contextManager.AddMessage("architect", fmt.Sprintf("Feedback: %s", approvalResult.Feedback))
		}

		// Return to appropriate state based on approval type
		if approvalType == proto.ApprovalTypeCompletion {
			return StateCoding, false, nil // Completion needs changes, go back to coding
		}
		return StatePlanning, false, nil // Plan needs changes, go back to planning

	case proto.ApprovalStatusRejected:
		if approvalType == proto.ApprovalTypeCompletion {
			c.logger.Error("🧑‍💻 Completion request rejected by architect: %s", approvalResult.Feedback)
			return proto.StateError, false, logx.Errorf("completion rejected by architect: %s", approvalResult.Feedback)
		} else {
			c.logger.Info("🧑‍💻 %s rejected, returning to PLANNING with feedback", approvalType)
			if approvalResult.Feedback != "" {
				c.contextManager.AddMessage("architect", fmt.Sprintf("Feedback: %s", approvalResult.Feedback))
			}
			return StatePlanning, false, nil
		}

	default:
		return proto.StateError, false, logx.Errorf("unknown %s approval status: %s", approvalType, approvalResult.Status)
	}
}

// handlePlanReviewApproval handles approved plan review based on approval type.
func (c *Coder) handlePlanReviewApproval(ctx context.Context, sm *agent.BaseStateMachine, approvalType proto.ApprovalType) (proto.State, bool, error) {
	switch approvalType {
	case proto.ApprovalTypePlan:
		// Regular plan approved - collect todos FIRST (fail-fast principle)
		c.logger.Info("🧑‍💻 Development plan approved, collecting implementation todos")

		// Request todo list from LLM BEFORE container reconfiguration (fail-fast)
		c.logger.Info("📋 [TODO] Requesting todo list from LLM after plan approval")
		nextState, completed, err := c.requestTodoList(ctx, sm)
		if err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to collect todo list")
		}

		// Todo collection succeeded - NOW reconfigure container for coding
		c.logger.Info("🧑‍💻 Todos collected successfully, reconfiguring container for coding")

		// Reconfigure container with read-write workspace for coding phase
		// Note: configureWorkspaceMount(readonly=false) creates a new coding container with GitHub auth
		if c.longRunningExecutor != nil {
			if err := c.configureWorkspaceMount(ctx, false, "coding"); err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to configure coding container")
			}
		}

		// Update story status to CODING via dispatcher (non-blocking)
		if c.dispatcher != nil {
			storyID := c.GetStoryID() // Get story ID for status update
			if err := c.dispatcher.UpdateStoryStatus(storyID, "coding"); err != nil {
				c.logger.Warn("Failed to update story status to coding: %v", err)
				// Continue anyway - status update failure shouldn't block the workflow
			} else {
				c.logger.Info("✅ Story %s status updated to CODING", storyID)
			}
		}

		return nextState, completed, nil

	case proto.ApprovalTypeCompletion:
		// Completion request approved - story is complete
		c.logger.Info("🧑‍💻 Story completion approved by architect, transitioning to DONE")

		// Mark story as completed
		sm.SetStateData(KeyStoryCompletedAt, time.Now().UTC())
		sm.SetStateData(KeyCompletionStatus, "APPROVED")

		return proto.StateDone, true, nil

	default:
		return proto.StateError, false, logx.Errorf("unsupported approval type in plan review: %s", approvalType)
	}
}

// Helper methods to extract data for approval requests

// getPlanApprovalContent generates plan approval request content using templates.
func (c *Coder) getPlanApprovalContent(sm *agent.BaseStateMachine) string {
	// Get plan content from state
	planContent := utils.GetStateValueOr[string](sm, KeyPlan, "")
	if planContent == "" {
		// Fallback to context if plan content not in state
		planContent = c.getLastAssistantMessage()
	}

	// Get task content
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Get knowledge pack from state
	knowledgePack := utils.GetStateValueOr[string](sm, string(stateDataKeyKnowledgePack), "")

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"TaskContent":   taskContent,
			"PlanContent":   planContent,
			"KnowledgePack": knowledgePack,
		},
	}

	// Render template
	if c.renderer == nil {
		return fmt.Sprintf("## Implementation Plan\n\n%s\n\n## Task Requirements\n\n%s", planContent, taskContent)
	}

	content, err := c.renderer.Render(templates.PlanApprovalRequestTemplate, templateData)
	if err != nil {
		c.logger.Warn("Failed to render plan approval template: %v", err)
		return fmt.Sprintf("## Implementation Plan\n\n%s\n\n## Task Requirements\n\n%s", planContent, taskContent)
	}

	return content
}

// getCompletionContent generates completion request content using templates.
func (c *Coder) getCompletionContent(sm *agent.BaseStateMachine) string {
	// Get completion summary from context
	summary := c.getLastAssistantMessage()
	if summary == "" {
		summary = "Story implementation completed. Ready for final review."
	}

	// Get files created
	filesCreated := utils.GetStateValueOr[[]string](sm, KeyFilesCreated, []string{})
	filesCreatedStr := "No files created information available"
	if len(filesCreated) > 0 {
		filesCreatedStr = strings.Join(filesCreated, ", ")
	}

	// Get story ID
	storyID := c.GetStoryID()

	// Build template data
	templateData := &templates.TemplateData{
		Extra: map[string]any{
			"StoryID":      storyID,
			"Summary":      summary,
			"FilesCreated": filesCreatedStr,
		},
	}

	// Render template
	if c.renderer == nil {
		return fmt.Sprintf("## Completion Summary\n\n%s\n\n## Files Created\n\n%s", summary, filesCreatedStr)
	}

	content, err := c.renderer.Render(templates.CompletionRequestTemplate, templateData)
	if err != nil {
		c.logger.Warn("Failed to render completion request template: %v", err)
		return fmt.Sprintf("## Completion Summary\n\n%s\n\n## Files Created\n\n%s", summary, filesCreatedStr)
	}

	return content
}

// getLastAssistantMessage gets the last assistant message from context.
func (c *Coder) getLastAssistantMessage() string {
	messages := c.contextManager.GetMessages()
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "assistant" {
			return messages[i].Content
		}
	}
	return ""
}

// requestTodoList makes an LLM call to collect implementation todos after plan approval.
//
//nolint:unparam // bool parameter follows handler signature convention, always false for intermediate states
func (c *Coder) requestTodoList(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Get plan from state
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	if plan == "" {
		c.logger.Error("📋 [TODO] No plan available for todo collection")
		return proto.StateError, false, logx.Errorf("no plan available for todo collection")
	}

	// Get task content for additional context
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Create prompt requesting todos with plan context
	prompt := fmt.Sprintf(`Your implementation plan has been approved by the architect.

**Approved Plan:**
%s

**Task Requirements:**
%s

Now break down this plan into specific, actionable implementation todos using the todos_add tool.

**Guidelines:**
- Create 3-10 todos for the initial list (recommended)
- Each todo should start with an action verb (e.g., "Create", "Implement", "Add", "Update")
- Each todo should have clear completion criteria
- Keep todos atomic and focused on a single change
- Order todos by dependency (what needs to be done first)

**IMPORTANT**: You MUST call the todos_add tool with an array of todo strings. Do not return an empty array.

Example:
todos_add({"todos": ["Create main.go with basic structure", "Implement HTTP server setup", "Add error handling"]})

Use the todos_add tool NOW to submit your implementation todos.`, plan, taskContent)

	// Create tool provider with only todos_add tool
	todoToolProvider := c.createTodoCollectionToolProvider()

	// Reset context for todo collection
	c.contextManager.ResetForNewTemplate("todo_collection", prompt)

	// Use toolloop for todo collection (single-pass with retry)
	loop := toolloop.New(c.llmClient, c.logger)

	cfg := &toolloop.Config[struct{}]{
		ContextManager: c.contextManager,
		InitialPrompt:  "", // Prompt already in context via ResetForNewTemplate
		ToolProvider:   todoToolProvider,
		MaxIterations:  2,    // One call + one retry if needed
		MaxTokens:      4096, // Sufficient for todo list
		AgentID:        c.agentID,
		DebugLogging:   false,
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			return c.checkTodoCollectionTerminal(ctx, sm, calls, results)
		},
		ExtractResult: func(_ []agent.ToolCall, _ []any) (struct{}, error) {
			// No result extraction needed for todo collection
			return struct{}{}, nil
		},
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("todo_collection_%s", utils.GetStateValueOr[string](sm, KeyStoryID, "unknown")),
			HardLimit: 2,
			OnHardLimit: func(_ context.Context, key string, count int) error {
				c.logger.Error("📋 [TODO] Failed to collect todos after %d iterations (key: %s)", count, key)
				return logx.Errorf("LLM failed to provide todos after %d attempts", count)
			},
		},
	}

	signal, _, err := toolloop.Run(loop, ctx, cfg)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to collect todo list")
	}

	// Handle terminal signals
	switch signal {
	case "CODING":
		// Todos collected successfully
		return StateCoding, false, nil
	case "":
		// No signal - should not happen with MaxIterations=2
		return proto.StateError, false, logx.Errorf("todo collection completed without signal")
	default:
		c.logger.Warn("Unknown signal from todo collection: %s", signal)
		return proto.StateError, false, logx.Errorf("unexpected signal from todo collection: %s", signal)
	}
}

// createTodoCollectionToolProvider creates a tool provider with only todos_add for todo collection phase.
func (c *Coder) createTodoCollectionToolProvider() *tools.ToolProvider {
	agentCtx := tools.AgentContext{
		Executor:        nil, // No executor needed for todos_add
		Agent:           c,
		ChatService:     nil, // No chat service needed
		ReadOnly:        true,
		NetworkDisabled: true,
		WorkDir:         c.workDir,
	}

	// Only include todos_add tool
	todoTools := []string{tools.ToolTodosAdd}
	return tools.NewProvider(&agentCtx, todoTools)
}

// checkTodoCollectionTerminal checks if todos_add was called and processes it.
func (c *Coder) checkTodoCollectionTerminal(ctx context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
	// Check if todos_add was called
	for i := range calls {
		toolCall := &calls[i]
		if toolCall.Name == tools.ToolTodosAdd {
			c.logger.Info("📋 [TODO] todos_add tool called, processing result")

			// Get the result for this tool call
			if i >= len(results) {
				c.logger.Error("📋 [TODO] No result available for todos_add call")
				return "" // Let toolloop handle missing result
			}

			result := results[i]
			resultMap, ok := result.(map[string]any)
			if !ok {
				c.logger.Error("📋 [TODO] Result is not a map: %T", result)
				return "" // Let toolloop continue for retry
			}

			// Process the result using handler
			_, _, err := c.handleTodosAdd(ctx, sm, resultMap)
			if err != nil {
				c.logger.Error("📋 [TODO] Failed to process todos: %v", err)
				// Don't return ERROR - let toolloop continue for retry
				return ""
			}

			// Todos successfully collected - signal transition to CODING
			c.logger.Info("📋 [TODO] Todos collected successfully, ready for CODING")
			return "CODING"
		}
	}

	// No todos_add found - continue loop (will be retried or hit iteration limit)
	c.logger.Warn("📋 [TODO] LLM did not call todos_add, continuing loop for retry")
	return ""
}
