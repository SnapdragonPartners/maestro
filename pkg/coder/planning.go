package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/effect"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

// handlePlanning processes the PLANNING state with enhanced codebase exploration.
func (c *Coder) handlePlanning(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", string(StatePlanning))
	// LLM response will be the assistant message

	// Check planning budget using unified budget review mechanism
	const maxPlanningIterations = 10
	if budgetReviewEff, budgetExceeded := c.checkLoopBudget(sm, string(stateDataKeyPlanningIterations), maxPlanningIterations, StatePlanning); budgetExceeded {
		c.logger.Info("Planning budget exceeded, triggering BUDGET_REVIEW")
		// Store effect for BUDGET_REVIEW state to execute
		sm.SetStateData("budget_review_effect", budgetReviewEff)
		return StateBudgetReview, false, nil
	}

	// State transitions handled in handleToolStateTransition

	// Continue with iterative planning using LLM + tools
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Generate tree output for template (cached for efficiency)
	_, exists := sm.GetStateValue(KeyTreeOutputCached)
	if !exists {
		tree := "Project structure not available"
		if c.longRunningExecutor != nil && c.containerName != "" {
			// Try tree command first, fallback to find
			c.logger.Debug("Attempting to get workspace structure")
			if treeResult, err := c.executeShellCommand(ctx, "tree", "/workspace", "-L", "3", "-I", "node_modules|.git|*.log"); err == nil {
				c.logger.Debug("tree command succeeded")
				tree = treeResult
			} else {
				// Fallback: use find to show directory structure
				c.logger.Info("tree command failed, using find fallback: %v", err)
				if findResult, findErr := c.executeShellCommand(ctx, "find", "/workspace", "-maxdepth", "3", "-type", "d"); findErr == nil {
					c.logger.Info("find fallback succeeded")
					tree = "Directory structure (find fallback):\n" + findResult
				} else {
					c.logger.Warn("find fallback failed, trying ls: %v", findErr)
					// Ultimate fallback: basic ls
					if lsResult, lsErr := c.executeShellCommand(ctx, "ls", "-la", "/workspace"); lsErr == nil {
						c.logger.Info("ls fallback succeeded")
						tree = "Basic workspace listing:\n" + lsResult
					} else {
						c.logger.Error("All workspace listing commands failed: ls error: %v", lsErr)
					}
				}
			}
		}
		sm.SetStateData(KeyTreeOutputCached, tree)
	}

	// Get story type for template selection
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	// Create ToolProvider for this planning session
	if c.planningToolProvider == nil {
		c.planningToolProvider = c.createPlanningToolProvider(storyType)
		c.logger.Debug("Created planning ToolProvider for story type: %s", storyType)
	}

	// Select appropriate planning template based on story type
	var planningTemplate templates.StateTemplate
	if storyType == string(proto.StoryTypeDevOps) {
		planningTemplate = templates.DevOpsPlanningTemplate
	} else {
		planningTemplate = templates.AppPlanningTemplate
	}

	// Get container information from config
	var containerName, containerDockerfile string
	if cfg, err := config.GetConfig(); err == nil && cfg.Container != nil {
		containerName = cfg.Container.Name
		containerDockerfile = cfg.Container.Dockerfile
		c.logger.Debug("🐳 Planning template container info - Name: '%s', Dockerfile: '%s'", containerName, containerDockerfile)
	} else {
		c.logger.Debug("🐳 Planning template container info not available: %v", err)
	}

	// Create enhanced template data with state-specific tool documentation
	templateData := &templates.TemplateData{
		TaskContent:         taskContent,
		TreeOutput:          utils.GetStateValueOr[string](sm, KeyTreeOutputCached, "Project structure not available"),
		ToolDocumentation:   c.planningToolProvider.GenerateToolDocumentation(),
		ContainerName:       containerName,
		ContainerDockerfile: containerDockerfile,
		Extra: map[string]any{
			"story_type": storyType, // Include story type for template logic
		},
	}

	// Render enhanced planning template
	if c.renderer == nil {
		return proto.StateError, false, logx.Errorf("template renderer not available for planning")
	}
	prompt, err := c.renderer.RenderWithUserInstructions(planningTemplate, templateData, c.workDir, "CODER")
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to render planning template")
	}

	// Reset context for new template (only if template type changed)
	templateName := fmt.Sprintf("planning-%s", storyType)
	c.contextManager.ResetForNewTemplate(templateName, prompt)

	// Log the rendered prompt for debugging
	c.logger.Info("🧑‍💻 Starting planning phase for story_type '%s'", storyType)

	// Flush user buffer before LLM request
	if err := c.contextManager.FlushUserBuffer(); err != nil {
		return proto.StateError, false, fmt.Errorf("failed to flush user buffer: %w", err)
	}

	// Get LLM response with tool support.
	// Build messages starting with the planning prompt.
	messages := c.buildMessagesWithContext(prompt)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: 8192,                       // Increased for exploration
		Tools:     c.getPlanningToolsForLLM(), // Use story-type-specific planning tools
		// Temperature: uses default 0.3 for exploration during planning
	}

	// Use base agent retry mechanism - exponential backoff is already implemented.
	resp, llmErr := c.llmClient.Complete(ctx, req)
	if llmErr != nil {
		// Check if this is an empty response error that should trigger budget review
		if c.isEmptyResponseError(llmErr) {
			return c.handleEmptyResponseError(sm, prompt, req, StatePlanning)
		}

		// For other errors, continue with normal error handling
		return proto.StateError, false, logx.Wrap(llmErr, "failed to get LLM planning response")
	}

	// Handle LLM response with proper empty response logic
	if err := c.handleLLMResponse(resp); err != nil {
		// True empty response - this is an error condition
		return proto.StateError, false, err
	}

	// Process tool calls if any (when supported).
	if len(resp.ToolCalls) > 0 {
		return c.processPlanningToolCalls(ctx, sm, resp.ToolCalls)
	}

	// If no tool calls, continue in planning state
	c.logger.Info("🧑‍💻 Planning iteration completed, staying in PLANNING for potential tool usage")
	return StatePlanning, false, nil
}

// processPlanningToolCalls processes tool calls during planning phase.
func (c *Coder) processPlanningToolCalls(ctx context.Context, sm *agent.BaseStateMachine, toolCalls []agent.ToolCall) (proto.State, bool, error) {
	c.logger.Info("🧑‍💻 Processing %d planning tool calls", len(toolCalls))

	for i := range toolCalls {
		toolCall := &toolCalls[i]
		c.logger.Info("Executing planning tool: %s", toolCall.Name)

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

			// Store planning context before asking question.
			c.storePlanningContext(sm)

			// Create question effect
			eff := effect.NewQuestionEffect(question, context, urgency, string(StatePlanning))

			// Set story_id for dispatcher validation
			storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
			eff.StoryID = storyID

			c.logger.Info("🧑‍💻 Asking question during planning")

			// Execute the question effect (blocks until answer received)
			result, err := c.ExecuteEffect(ctx, eff)
			if err != nil {
				c.logger.Error("🧑‍💻 Failed to get answer: %v", err)
				// Add error to context for LLM to handle
				errorMsg := fmt.Sprintf("Question failed: %v", err)
				c.contextManager.AddMessage("question-error", errorMsg)
				continue
			}

			// Process the answer
			if questionResult, ok := result.(*effect.QuestionResult); ok {
				// Answer received from architect (logged to database only)

				// Add the Q&A to context so the LLM can see it
				qaContent := fmt.Sprintf("Question: %s\nAnswer: %s", question, questionResult.Answer)
				c.contextManager.AddMessage("architect-answer", qaContent)

				// Continue with planning using the answer
			} else {
				c.logger.Error("🧑‍💻 Invalid question result type: %T", result)
			}
			continue
		}

		// Get tool from ToolProvider and execute.
		tool, err := c.planningToolProvider.Get(toolCall.Name)
		if err != nil {
			c.logger.Error("Tool not found in ToolProvider: %s", toolCall.Name)
			continue
		}

		// Add agent_id to context for tools that need it (like chat tools)
		toolCtx := context.WithValue(ctx, tools.AgentIDContextKey, c.agentID)

		result, err := tool.Exec(toolCtx, toolCall.Parameters)
		if err != nil {
			if toolCall.Name == tools.ToolShell {
				// For shell tool, provide cleaner logging without Docker details
				c.logger.Info("Shell command failed: %v", err)
			} else {
				c.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
			}
			continue
		}

		// Handle tool results and state transitions.
		if resultMap, ok := result.(map[string]any); ok {
			if nextState, hasNextState := resultMap["next_state"]; hasNextState {
				if nextStateStr, ok := nextState.(string); ok {
					return c.handleToolStateTransition(ctx, sm, toolCall.Name, nextStateStr, resultMap)
				}
			}
		}

		// Add tool execution results to context using proper method.
		c.addToolResultToContext(*toolCall, result)
		c.logger.Info("Planning tool %s executed successfully", toolCall.Name)
	}

	// Continue planning after processing all tools
	return StatePlanning, false, nil
}

// storePlanningContext stores the current planning context.
func (c *Coder) storePlanningContext(sm *agent.BaseStateMachine) {
	context := map[string]any{
		"exploration_history": c.getExplorationHistory(),
		"files_examined":      c.getFilesExamined(),
		"current_findings":    c.getCurrentFindings(),
		"timestamp":           time.Now().UTC(),
	}
	sm.SetStateData(KeyPlanningContextSaved, context)
	c.logger.Debug("🧑‍💻 Stored planning context for QUESTION transition")
}

// handleToolStateTransition processes tool state transitions directly.
func (c *Coder) handleToolStateTransition(ctx context.Context, sm *agent.BaseStateMachine, toolName, nextState string, resultMap map[string]any) (proto.State, bool, error) {
	// Log the transition.
	if message, hasMessage := resultMap["message"].(string); hasMessage {
		c.logger.Info("Tool %s: %s", toolName, message)
	}

	// Handle tool-specific state transitions.
	switch toolName {
	case tools.ToolSubmitPlan:
		return c.handlePlanSubmissionDirect(ctx, sm, resultMap)

	case tools.ToolMarkStoryComplete:
		return c.handleCompletionSubmissionDirect(ctx, sm, resultMap)

	case tools.ToolAskQuestion:
		// Questions handled inline via Effects pattern
		c.logger.Info("🧑‍💻 Question handled inline via Effects pattern, continuing in PLANNING")
		return StatePlanning, false, nil

	default:
		c.logger.Info("🧑‍💻 Tool %s requested unknown state transition: %s, staying in PLANNING", toolName, nextState)
		return StatePlanning, false, nil
	}
}

// handlePlanSubmissionDirect processes submit_plan tool results directly.
func (c *Coder) handlePlanSubmissionDirect(_ context.Context, sm *agent.BaseStateMachine, resultMap map[string]any) (proto.State, bool, error) {
	plan := utils.GetMapFieldOr[string](resultMap, "plan", "")
	confidence := utils.GetMapFieldOr[string](resultMap, "confidence", "")
	explorationSummary := utils.GetMapFieldOr[string](resultMap, "exploration_summary", "")
	risks := utils.GetMapFieldOr[string](resultMap, "risks", "")
	todos := utils.GetMapFieldOr[[]any](resultMap, "todos", []any{})

	// Convert todos to structured format.
	planTodos := make([]PlanTodo, len(todos))
	for i, todoItem := range todos {
		if todoMap, ok := utils.SafeAssert[map[string]any](todoItem); ok {
			planTodos[i] = PlanTodo{
				ID:          utils.GetMapFieldOr[string](todoMap, "id", ""),
				Description: utils.GetMapFieldOr[string](todoMap, "description", ""),
				Completed:   utils.GetMapFieldOr[bool](todoMap, "completed", false),
			}
		}
	}

	// Store plan data using typed constants.
	sm.SetStateData(string(stateDataKeyPlan), plan)
	sm.SetStateData(string(stateDataKeyPlanConfidence), confidence)
	sm.SetStateData(string(stateDataKeyExplorationSummary), explorationSummary)
	sm.SetStateData(string(stateDataKeyPlanRisks), risks)
	sm.SetStateData(string(stateDataKeyPlanTodos), planTodos)
	sm.SetStateData(KeyPlanningCompletedAt, time.Now().UTC())

	// Store plan approval request for PLAN_REVIEW state to handle
	c.pendingApprovalRequest = &ApprovalRequest{
		ID:      proto.GenerateApprovalID(),
		Content: plan,
		Reason:  fmt.Sprintf("Enhanced plan requires approval (confidence: %s)", confidence),
		Type:    proto.ApprovalTypePlan,
	}

	c.logger.Info("🧑‍💻 Plan submitted, transitioning to PLAN_REVIEW for approval via Effects")

	return StatePlanReview, false, nil
}

// handleCompletionSubmissionDirect processes mark_story_complete tool results directly.
func (c *Coder) handleCompletionSubmissionDirect(_ context.Context, sm *agent.BaseStateMachine, resultMap map[string]any) (proto.State, bool, error) {
	reason := utils.GetMapFieldOr[string](resultMap, "reason", "")
	evidence := utils.GetMapFieldOr[string](resultMap, "evidence", "")
	confidence := utils.GetMapFieldOr[string](resultMap, "confidence", "")

	// Get story type to check for DevOps completion requirements
	storyType := utils.GetStateValueOr[string](sm, proto.KeyStoryType, string(proto.StoryTypeApp))

	// DevOps completion gate: must have valid target image
	if storyType == string(proto.StoryTypeDevOps) && !config.IsValidTargetImage() {
		return proto.StateError, false, fmt.Errorf("DevOps story cannot be completed without a valid target container. You must create a valid target container and run the container_update tool to proceed. Current reason: %s", reason)
	}

	// Store completion timestamp
	sm.SetStateData(KeyCompletionSubmittedAt, time.Now().UTC())

	// Store completion approval request for PLAN_REVIEW state to handle
	c.pendingApprovalRequest = &ApprovalRequest{
		ID:      proto.GenerateApprovalID(),
		Content: fmt.Sprintf("Story completion request:\n\nReason: %s\n\nEvidence: %s\n\nConfidence: %s", reason, evidence, confidence),
		Reason:  fmt.Sprintf("Story completion requires approval (confidence: %s)", confidence),
		Type:    proto.ApprovalTypeCompletion,
	}

	c.logger.Info("🧑‍💻 Completion submitted, transitioning to PLAN_REVIEW for approval via Effects")

	return StatePlanReview, false, nil
}

// Context management placeholder helper methods for planning.
func (c *Coder) getExplorationHistory() any { return []string{} }
func (c *Coder) getFilesExamined() any      { return []string{} }
func (c *Coder) getCurrentFindings() any    { return map[string]any{} }
