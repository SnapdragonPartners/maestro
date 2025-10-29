package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/llm"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
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
		planContent := c.getPlanContentForReview(sm)
		taskContent := c.getTaskContentForReview(sm)
		storyID := c.GetStoryID() // Use the getter method I created
		eff = effect.NewPlanApprovalEffectWithStoryID(planContent, taskContent, storyID)
		c.contextManager.AddAssistantMessage("Plan review phase: requesting architect approval")

	case proto.ApprovalTypeCompletion:
		summary := c.getCompletionSummaryForReview(sm)
		filesCreated := c.getFilesCreatedForReview(sm)
		storyID := c.GetStoryID() // Use the getter method I created
		eff = effect.NewCompletionApprovalEffectWithStoryID(summary, filesCreated, storyID)
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
		c.logger.Info("🧑‍💻 %s needs changes, returning to PLANNING with feedback", approvalType)
		if approvalResult.Feedback != "" {
			c.contextManager.AddMessage("architect", fmt.Sprintf("Feedback: %s", approvalResult.Feedback))
		}
		return StatePlanning, false, nil

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

// getPlanContentForReview extracts plan content from state for review.
func (c *Coder) getPlanContentForReview(sm *agent.BaseStateMachine) string {
	planContent := utils.GetStateValueOr[string](sm, string(stateDataKeyPlan), "")
	if planContent == "" {
		// Fallback to context if plan content not in state
		planContent = c.getLastAssistantMessage()
	}
	return planContent
}

// getTaskContentForReview extracts task content from state for review.
func (c *Coder) getTaskContentForReview(sm *agent.BaseStateMachine) string {
	return utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")
}

// getCompletionSummaryForReview extracts completion summary from state for review.
func (c *Coder) getCompletionSummaryForReview(_ *agent.BaseStateMachine) string {
	// Since there's no specific completion summary in state, generate one from context
	summary := c.getLastAssistantMessage()
	if summary == "" {
		summary = "Story implementation completed. Ready for final review."
	}
	return summary
}

// getFilesCreatedForReview extracts files created information from state for review.
func (c *Coder) getFilesCreatedForReview(sm *agent.BaseStateMachine) string {
	filesCreated := utils.GetStateValueOr[[]string](sm, KeyFilesCreated, []string{})
	if len(filesCreated) == 0 {
		return "No files created information available"
	}
	return strings.Join(filesCreated, ", ")
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

	// Build messages with the prompt
	messages := []agent.CompletionMessage{
		{Role: agent.RoleUser, Content: prompt},
	}

	// Get tools for LLM
	toolMetas := todoToolProvider.List()
	toolDefinitions := make([]tools.ToolDefinition, 0, len(toolMetas))
	for i := range toolMetas {
		toolDefinitions = append(toolDefinitions, tools.ToolDefinition(toolMetas[i]))
	}

	// Make LLM call
	req := agent.CompletionRequest{
		Messages:    messages,
		MaxTokens:   4096,
		Temperature: llm.TemperatureDeterministic, // Deterministic output for todo collection
		Tools:       toolDefinitions,
	}

	resp, err := c.llmClient.Complete(ctx, req)
	if err != nil {
		c.logger.Error("📋 [TODO] Failed to get LLM response for todo collection: %v", err)
		return proto.StateError, false, logx.Wrap(err, "failed to get LLM response for todo collection")
	}

	c.logger.Info("📋 [TODO] LLM responded with %d tool calls", len(resp.ToolCalls))

	// Check if todos_add was called
	var todosAddCalled bool
	for i := range resp.ToolCalls {
		toolCall := &resp.ToolCalls[i]
		c.logger.Info("📋 [TODO] Tool call %d: %s with params: %v", i+1, toolCall.Name, toolCall.Parameters)

		if toolCall.Name == tools.ToolTodosAdd {
			todosAddCalled = true

			// Log the todos parameter
			if todosParam, ok := toolCall.Parameters["todos"]; ok {
				c.logger.Info("📋 [TODO] Received todos parameter: %v (type: %T)", todosParam, todosParam)
			} else {
				c.logger.Warn("📋 [TODO] No 'todos' parameter in tool call")
			}

			// Execute the todos_add tool
			tool, getErr := todoToolProvider.Get(tools.ToolTodosAdd)
			if getErr != nil {
				c.logger.Error("📋 [TODO] Failed to get todos_add tool: %v", getErr)
				return proto.StateError, false, logx.Wrap(getErr, "failed to get todos_add tool")
			}

			result, execErr := tool.Exec(ctx, toolCall.Parameters)
			if execErr != nil {
				c.logger.Error("📋 [TODO] Tool execution failed: %v", execErr)
				// Retry once - add error to context and ask again
				return c.retryTodoCollection(ctx, sm, fmt.Sprintf("Error: %v. Please try again with valid todos.", execErr))
			}

			// Process the result using handler
			if resultMap, ok := result.(map[string]any); ok {
				nextState, _, handlerErr := c.handleTodosAdd(ctx, sm, resultMap)
				if handlerErr != nil {
					return proto.StateError, false, logx.Wrap(handlerErr, "failed to process todos_add result")
				}
				return nextState, false, nil
			}
		}
	}

	if !todosAddCalled {
		c.logger.Warn("📋 [TODO] LLM did not call todos_add tool, retrying once")
		return c.retryTodoCollection(ctx, sm, "You must use the todos_add tool to submit your implementation todos. Please call todos_add now.")
	}

	// Should not reach here
	return proto.StateError, false, logx.Errorf("unexpected state in requestTodoList")
}

// retryTodoCollection retries todo collection once if LLM doesn't use the tool.
func (c *Coder) retryTodoCollection(ctx context.Context, sm *agent.BaseStateMachine, errorMsg string) (proto.State, bool, error) {
	// Check if we've already retried
	if utils.GetStateValueOr[bool](sm, "todo_collection_retried", false) {
		return proto.StateError, false, logx.Errorf("LLM failed to provide todos even after retry")
	}

	// Mark that we've retried
	sm.SetStateData("todo_collection_retried", true)

	// Add error to context
	c.contextManager.AddMessage("system", errorMsg)

	// Create retry prompt
	prompt := "Please use the todos_add tool now to submit 3-10 implementation todos based on your approved plan."

	// Create tool provider
	todoToolProvider := c.createTodoCollectionToolProvider()

	// Build messages
	messages := c.buildMessagesWithContext(prompt)

	// Get tools for LLM
	toolMetas := todoToolProvider.List()
	toolDefinitions := make([]tools.ToolDefinition, 0, len(toolMetas))
	for i := range toolMetas {
		toolDefinitions = append(toolDefinitions, tools.ToolDefinition(toolMetas[i]))
	}

	// Make LLM call
	req := agent.CompletionRequest{
		Messages:    messages,
		MaxTokens:   4096,
		Temperature: llm.TemperatureDeterministic, // Deterministic output for todo collection
		Tools:       toolDefinitions,
	}

	resp, err := c.llmClient.Complete(ctx, req)
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to get LLM response for todo collection retry")
	}

	// Check if todos_add was called
	for i := range resp.ToolCalls {
		toolCall := &resp.ToolCalls[i]
		if toolCall.Name == tools.ToolTodosAdd {
			// Execute the todos_add tool
			tool, getErr := todoToolProvider.Get(tools.ToolTodosAdd)
			if getErr != nil {
				return proto.StateError, false, logx.Wrap(getErr, "failed to get todos_add tool")
			}

			result, execErr := tool.Exec(ctx, toolCall.Parameters)
			if execErr != nil {
				// Second failure - transition to ERROR
				return proto.StateError, false, logx.Wrap(execErr, "todos_add tool failed on retry")
			}

			// Process the result using handler
			if resultMap, ok := result.(map[string]any); ok {
				nextState, _, handlerErr := c.handleTodosAdd(ctx, sm, resultMap)
				if handlerErr != nil {
					return proto.StateError, false, logx.Wrap(handlerErr, "failed to process todos_add result on retry")
				}
				return nextState, false, nil
			}
		}
	}

	// Still no todos_add call after retry - transition to ERROR
	return proto.StateError, false, logx.Errorf("LLM failed to call todos_add even after retry")
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
	return tools.NewProvider(agentCtx, todoTools)
}
