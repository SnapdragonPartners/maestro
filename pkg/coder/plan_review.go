package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
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
		// Note: Don't add assistant message here - would violate alternation after submit_plan tool result

	case proto.ApprovalTypeCompletion:
		completionContent := c.getCompletionContent(sm)
		storyID := c.GetStoryID()                                                           // Use the getter method I created
		eff = effect.NewCompletionApprovalEffectWithStoryID(completionContent, "", storyID) // Files created is now in completionContent template
		// Note: Don't add assistant message here - would violate alternation after done tool result

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
				sm.SetStateData(KeyTodoList, c.todoList)
			}
		}

		// Add feedback to context for visibility (as user role for proper alternation)
		if approvalResult.Feedback != "" {
			c.contextManager.AddMessage("user", fmt.Sprintf("Architect feedback: %s", approvalResult.Feedback))
		}

		// Return to appropriate state based on approval type
		if approvalType == proto.ApprovalTypeCompletion {
			// Set resume input for coding session resume (if using Claude Code mode)
			if approvalResult.Feedback != "" {
				sm.SetStateData(KeyResumeInput, fmt.Sprintf("Code review feedback - changes requested:\n\n%s\n\nPlease address these issues and continue implementation.", approvalResult.Feedback))
			}
			return StateCoding, false, nil // Completion needs changes, go back to coding
		}
		// Plan needs changes - set resume input for planning session resume (if using Claude Code mode)
		// Note: We reuse KeyResumeInput since planning and coding never run simultaneously
		if approvalResult.Feedback != "" {
			sm.SetStateData(KeyResumeInput, fmt.Sprintf("Plan review feedback - changes requested:\n\n%s\n\nPlease revise your plan and resubmit.", approvalResult.Feedback))
		}
		return StatePlanning, false, nil // Plan needs changes, go back to planning

	case proto.ApprovalStatusRejected:
		if approvalType == proto.ApprovalTypeCompletion {
			c.logger.Error("🧑‍💻 Completion request rejected by architect: %s", approvalResult.Feedback)
			return proto.StateError, false, logx.Errorf("completion rejected by architect: %s", approvalResult.Feedback)
		} else {
			c.logger.Info("🧑‍💻 %s rejected, returning to PLANNING with feedback", approvalType)
			if approvalResult.Feedback != "" {
				c.contextManager.AddMessage("user", fmt.Sprintf("Architect feedback: %s", approvalResult.Feedback))
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
		// Regular plan approved
		c.logger.Info("🧑‍💻 Development plan approved")

		// Skip todo collection in Claude Code mode - it manages its own todos
		var nextState proto.State
		var completed bool
		if c.isClaudeCodeMode(ctx) {
			c.logger.Info("📋 Skipping todo collection in Claude Code mode (Claude Code manages its own todos)")
			nextState = StateCoding
			completed = false
		} else {
			// Standard mode - collect todos FIRST (fail-fast principle)
			c.logger.Info("📋 [TODO] Requesting todo list from LLM after plan approval")
			var err error
			nextState, completed, err = c.requestTodoList(ctx, sm)
			if err != nil {
				return proto.StateError, false, logx.Wrap(err, "failed to collect todo list")
			}
		}

		// NOW reconfigure container for coding
		c.logger.Info("🧑‍💻 Reconfiguring container for coding")

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
	loop := toolloop.New(c.LLMClient, c.logger)

	// Get todos_add tool and wrap it as terminal tool
	todosAddTool, err := todoToolProvider.Get(tools.ToolTodosAdd)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to get todos_add tool")
	}
	terminalTool := todosAddTool

	// No general tools in this phase - just the terminal tool
	cfg := &toolloop.Config[TodoCollectionResult]{
		ContextManager: c.contextManager,
		InitialPrompt:  "",             // Prompt already in context via ResetForNewTemplate
		GeneralTools:   []tools.Tool{}, // No general tools
		TerminalTool:   terminalTool,
		MaxIterations:  2,    // One call + one retry if needed
		MaxTokens:      4096, // Sufficient for todo list
		AgentID:        c.GetAgentID(),
		DebugLogging:   config.GetDebugLLMMessages(),
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("todo_collection_%s", utils.GetStateValueOr[string](sm, KeyStoryID, "unknown")),
			HardLimit: 2,
			OnHardLimit: func(_ context.Context, key string, count int) error {
				c.logger.Error("📋 [TODO] Failed to collect todos after %d iterations (key: %s)", count, key)
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Switch on outcome kind first
	switch out.Kind {
	case toolloop.OutcomeProcessEffect:
		// todos_add returned ProcessEffect with todo data
		c.logger.Info("🔔 Tool returned ProcessEffect with signal: %s", out.Signal)

		// Verify signal
		if out.Signal != tools.SignalCoding {
			return proto.StateError, false, logx.Errorf("expected CODING signal from todos_add, got: %s", out.Signal)
		}

		// Extract todos from ProcessEffect.Data
		effectData, ok := out.EffectData.(map[string]any)
		if !ok {
			return proto.StateError, false, logx.Errorf("CODING effect data is not map[string]any: %T", out.EffectData)
		}

		// Extract todos array (should be []string from tool)
		todos, err := utils.GetMapField[[]string](effectData, "todos")
		if err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to extract todos from ProcessEffect")
		}

		c.logger.Info("📋 [TODO] Extracted %d todos from ProcessEffect", len(todos))

		// Process todos into TodoList
		if err := c.processTodosFromEffect(sm, todos); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to process todos")
		}

		// Todos collected successfully - transition to CODING
		return StateCoding, false, nil

	case toolloop.OutcomeIterationLimit:
		// For todo collection, hitting limit is a failure (not budget review)
		c.logger.Error("📋 [TODO] Failed to collect todos after %d iterations", out.Iteration)
		return proto.StateError, false, logx.Errorf("LLM failed to provide todos after %d attempts", out.Iteration)

	case toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError, toolloop.OutcomeNoToolTwice:
		return proto.StateError, false, logx.Wrap(out.Err, "failed to collect todo list")

	default:
		return proto.StateError, false, logx.Errorf("unknown toolloop outcome kind: %v", out.Kind)
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

// processTodosFromEffect processes todos extracted from ProcessEffect.Data.
//
//nolint:unparam // error return reserved for future validation logic
func (c *Coder) processTodosFromEffect(sm *agent.BaseStateMachine, todos []string) error {
	if len(todos) == 0 {
		return nil
	}

	// Create todo list from extracted todos
	items := make([]TodoItem, len(todos))
	for i, todoDesc := range todos {
		items[i] = TodoItem{
			Description: todoDesc,
			Completed:   false,
		}
	}

	todoList := &TodoList{
		Items:   items,
		Current: 0,
	}

	c.todoList = todoList
	sm.SetStateData(KeyTodoList, todoList)

	c.logger.Info("📋 [TODO] Created todo list with %d items from ProcessEffect", len(todos))
	return nil
}
