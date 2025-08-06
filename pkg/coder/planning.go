package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/utils"
)

// handlePlanning processes the PLANNING state with enhanced codebase exploration.
func (c *Coder) handlePlanning(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	logx.DebugState(ctx, "coder", "enter", string(StatePlanning))
	// Note: Don't add assistant message here - the LLM response will be the assistant message

	// Check planning budget using unified budget review mechanism.
	const maxPlanningIterations = 10
	if c.checkLoopBudget(sm, string(stateDataKeyPlanningIterations), maxPlanningIterations, StatePlanning) {
		c.logger.Info("Planning budget exceeded, triggering BUDGET_REVIEW")
		return StateBudgetReview, false, nil
	}

	// Check for question tool result (ask_question was called).
	if questionData, exists := sm.GetStateValue(KeyQuestionSubmitted); exists {
		return c.handleQuestionTransition(ctx, sm, questionData)
	}

	// Check for plan submission (submit_plan was called).
	if planData, exists := sm.GetStateValue(KeyPlanSubmitted); exists {
		return c.handlePlanSubmission(ctx, sm, planData)
	}

	// Check for completion submission (mark_story_complete was called).
	if completionData, exists := sm.GetStateValue(string(stateDataKeyCompletionSubmitted)); exists && completionData != nil {
		return c.handleCompletionSubmission(ctx, sm, completionData)
	}

	// Continue with iterative planning using LLM + tools.
	taskContent := utils.GetStateValueOr[string](sm, string(stateDataKeyTaskContent), "")

	// Restore planning context if returning from QUESTION.
	if questionAnswered := utils.GetStateValueOr[bool](sm, string(stateDataKeyQuestionAnswered), false); questionAnswered {
		c.restorePlanningContext(sm)
		sm.SetStateData(string(stateDataKeyQuestionAnswered), false) // Clear flag
		c.logger.Info("🧑‍💻 Restored planning context after question answered")
	}

	// Generate tree output for template (cached for efficiency).
	_, exists := sm.GetStateValue(KeyTreeOutputCached)
	if !exists {
		tree := "Project structure not available"
		if c.longRunningExecutor != nil && c.containerName != "" {
			// Try tree command first, fall back to find if not available.
			c.logger.Debug("Attempting to get workspace structure")
			if treeResult, err := c.executeShellCommand(ctx, "tree", "/workspace", "-L", "3", "-I", "node_modules|.git|*.log"); err == nil {
				c.logger.Debug("tree command succeeded")
				tree = treeResult
			} else {
				// Fallback: use find to show directory structure.
				c.logger.Info("tree command failed, using find fallback: %v", err)
				if findResult, findErr := c.executeShellCommand(ctx, "find", "/workspace", "-maxdepth", "3", "-type", "d"); findErr == nil {
					c.logger.Info("find fallback succeeded")
					tree = "Directory structure (find fallback):\n" + findResult
				} else {
					c.logger.Warn("find fallback failed, trying ls: %v", findErr)
					// Ultimate fallback: basic ls.
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

	// Create enhanced template data with state-specific tool documentation.
	templateData := &templates.TemplateData{
		TaskContent:       taskContent,
		TreeOutput:        utils.GetStateValueOr[string](sm, KeyTreeOutputCached, "Project structure not available"),
		ToolDocumentation: c.planningToolProvider.GenerateToolDocumentation(),
		Extra: map[string]any{
			"story_type": storyType, // Include story type for template logic
		},
	}

	// Render enhanced planning template.
	if c.renderer == nil {
		return proto.StateError, false, logx.Errorf("template renderer not available for planning")
	}
	prompt, err := c.renderer.RenderWithUserInstructions(planningTemplate, templateData, c.workDir, "CODER")
	if err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to render planning template")
	}

	// Log the rendered prompt for debugging
	c.logger.Info("🧑‍💻 Starting planning phase for story_type '%s'", storyType)

	// Get LLM response with tool support.
	// Build messages starting with the planning prompt.
	messages := c.buildMessagesWithContext(prompt)

	req := agent.CompletionRequest{
		Messages:  messages,
		MaxTokens: 8192,                       // Increased for exploration
		Tools:     c.getPlanningToolsForLLM(), // Use story-type-specific planning tools
	}

	// Use base agent retry mechanism - exponential backoff is already implemented.
	resp, llmErr := c.llmClient.Complete(ctx, req)
	if llmErr != nil {
		return proto.StateError, false, logx.Wrap(llmErr, "failed to get LLM planning response")
	}

	if resp.Content == "" && len(resp.ToolCalls) == 0 {
		return proto.StateError, false, logx.Errorf("empty response from Claude")
	}

	// Process tool calls if any (when supported).
	if len(resp.ToolCalls) > 0 {
		return c.processPlanningToolCalls(ctx, sm, resp.ToolCalls)
	}

	// If no tool calls, continue in planning state with response.
	c.contextManager.AddMessage("assistant", resp.Content)
	c.logger.Info("🧑‍💻 Planning iteration completed, staying in PLANNING for potential tool usage")
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

// restorePlanningContext restores the planning context after returning from QUESTION.
func (c *Coder) restorePlanningContext(sm *agent.BaseStateMachine) {
	if contextData, exists := sm.GetStateValue(KeyPlanningContextSaved); exists {
		if context, ok := contextData.(map[string]any); ok {
			c.restoreExplorationHistory(context["exploration_history"])
			c.restoreFilesExamined(context["files_examined"])
			c.restoreCurrentFindings(context["current_findings"])
			c.logger.Debug("🧑‍💻 Restored planning context from QUESTION transition")
		}
	}
}

// processPlanningToolCalls handles tool execution during planning.
func (c *Coder) processPlanningToolCalls(ctx context.Context, sm *agent.BaseStateMachine, toolCalls []agent.ToolCall) (proto.State, bool, error) {
	c.logger.Info("🧑‍💻 Processing %d tool calls in planning state", len(toolCalls))

	for i := range toolCalls {
		toolCall := &toolCalls[i]
		c.logger.Info("Executing planning tool: %s", toolCall.Name)

		// Get tool from ToolProvider and execute.
		tool, err := c.planningToolProvider.Get(toolCall.Name)
		if err != nil {
			c.logger.Error("Tool not found in ToolProvider: %s", toolCall.Name)
			return proto.StateError, false, logx.Wrap(err, fmt.Sprintf("tool %s not found", toolCall.Name))
		}

		result, err := tool.Exec(ctx, toolCall.Parameters)
		if err != nil {
			// Tool execution failures are recoverable - add comprehensive error to context for LLM to react.
			c.logger.Info("Tool execution failed for %s: %v", toolCall.Name, err)
			c.addComprehensiveToolFailureToContext(*toolCall, err)
			continue // Continue processing other tool calls
		}

		// Handle tool result generically - check if tool requests state transition.
		if resultMap, ok := result.(map[string]any); ok {
			if nextState, hasNextState := resultMap["next_state"].(string); hasNextState {
				return c.handleToolStateTransition(ctx, sm, toolCall.Name, nextState, resultMap)
			}
		}

		// No state transition requested - continue in current state.
		// Add tool execution results to context so Claude can see them.
		c.addToolResultToContext(*toolCall, result)
		c.logger.Info("Tool %s executed successfully, continuing in planning", toolCall.Name)
	}

	return StatePlanning, false, nil
}

// handleToolStateTransition processes generic tool state transitions.
func (c *Coder) handleToolStateTransition(_ /* ctx */ context.Context, sm *agent.BaseStateMachine, toolName, nextState string, resultMap map[string]any) (proto.State, bool, error) {
	// Store all result data in state machine (let the tool decide what to store).
	for key, value := range resultMap {
		if key != "next_state" && key != "success" && key != "message" {
			sm.SetStateData(key, value)
		}
	}

	// Log the transition.
	if message, hasMessage := resultMap["message"].(string); hasMessage {
		c.logger.Info("Tool %s: %s", toolName, message)
	}

	if nextState == string(StatePlanReview) {
		// Set plan submitted flag so handlePlanSubmission can process it on next iteration.
		sm.SetStateData(KeyPlanSubmitted, resultMap)
		return StatePlanning, false, nil
	}

	if nextState == "COMPLETION_REVIEW" {
		// Set completion submitted flag so handleCompletionSubmission can process it on next iteration.
		sm.SetStateData(string(stateDataKeyCompletionSubmitted), resultMap)
		return StatePlanning, false, nil
	}

	if nextState == "QUESTION" {
		// Set question submitted flag so handleQuestionTransition can process it on next iteration.
		sm.SetStateData(KeyQuestionSubmitted, resultMap)
		return StatePlanning, false, nil
	}

	c.logger.Info("🧑‍💻 Tool %s requested unknown state transition: %s, staying in PLANNING", toolName, nextState)
	return StatePlanning, false, nil
}

// Context management placeholder helper methods for planning.
func (c *Coder) getExplorationHistory() any    { return []string{} }
func (c *Coder) getFilesExamined() any         { return []string{} }
func (c *Coder) getCurrentFindings() any       { return map[string]any{} }
func (c *Coder) restoreExplorationHistory(any) {}
func (c *Coder) restoreFilesExamined(any)      {}
func (c *Coder) restoreCurrentFindings(any)    {}
