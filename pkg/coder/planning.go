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
	"orchestrator/pkg/knowledge"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
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

	// Retrieve knowledge pack on first planning iteration
	if _, exists := sm.GetStateValue(string(stateDataKeyKnowledgePack)); !exists {
		if knowledgePack, err := c.retrieveKnowledgePack(ctx, taskContent); err == nil {
			sm.SetStateData(string(stateDataKeyKnowledgePack), knowledgePack)
			c.logger.Info("📚 Knowledge pack retrieved (%d nodes)", len(strings.Split(knowledgePack, "\n")))
		} else {
			c.logger.Warn("Failed to retrieve knowledge pack: %v", err)
			// Not a fatal error - continue without knowledge pack
			sm.SetStateData(string(stateDataKeyKnowledgePack), "")
		}
	}

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

	// Use toolloop for LLM iteration with planning tools
	loop := toolloop.New(c.llmClient, c.logger)

	//nolint:dupl // Similar config in coding.go - intentional per-state configuration
	cfg := &toolloop.Config{
		ContextManager: c.contextManager,
		InitialPrompt:  "", // Prompt already in context via ResetForNewTemplate
		ToolProvider:   c.planningToolProvider,
		MaxIterations:  maxPlanningIterations,
		MaxTokens:      8192, // Increased for exploration
		AgentID:        c.agentID,
		DebugLogging:   false,
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			return c.checkPlanningTerminal(ctx, sm, calls, results)
		},
		OnIterationLimit: func(_ context.Context) (string, error) {
			c.logger.Info("⚠️  Planning reached max iterations, triggering budget review")
			budgetEff := effect.NewBudgetReviewEffect(
				fmt.Sprintf("Maximum planning iterations (%d) reached", maxPlanningIterations),
				"Planning workflow needs additional iterations to complete exploration",
				string(StatePlanning),
			)
			// Set story ID for dispatcher validation
			budgetEff.StoryID = utils.GetStateValueOr[string](sm, KeyStoryID, "")
			sm.SetStateData("budget_review_effect", budgetEff)
			return string(StateBudgetReview), nil
		},
	}

	signal, err := loop.Run(ctx, cfg)
	if err != nil {
		// Check if this is an empty response error
		if c.isEmptyResponseError(err) {
			req := agent.CompletionRequest{MaxTokens: 8192}
			return c.handleEmptyResponseError(sm, prompt, req, StatePlanning)
		}
		return proto.StateError, false, logx.Wrap(err, "toolloop execution failed")
	}

	// Handle terminal signals
	switch signal {
	case string(StateBudgetReview):
		return StateBudgetReview, false, nil
	case string(StateQuestion):
		return StateQuestion, false, nil
	case "PLAN_REVIEW":
		return StatePlanReview, false, nil
	case "":
		// No signal, continue planning
		c.logger.Info("🧑‍💻 Planning iteration completed, staying in PLANNING")
		return StatePlanning, false, nil
	default:
		c.logger.Warn("Unknown signal from planning toolloop: %s", signal)
		return StatePlanning, false, nil
	}
}

// checkPlanningTerminal examines tool calls and results for terminal signals during planning.
func (c *Coder) checkPlanningTerminal(ctx context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
	for i := range calls {
		toolCall := &calls[i]

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
				"origin":   string(StatePlanning),
			})

			c.logger.Info("🧑‍💻 Planning detected ask_question, transitioning to QUESTION state")
			return string(StateQuestion) // Signal state transition
		}

		// Check if result contains next_state signal (submit_plan, mark_story_complete)
		resultMap, ok := results[i].(map[string]any)
		if !ok {
			continue
		}

		// Check for next_state signal in tool result
		if nextState, hasNextState := resultMap["next_state"]; hasNextState {
			if nextStateStr, ok := nextState.(string); ok {
				// Process via existing handler to maintain current behavior
				newState, _, err := c.handleToolStateTransition(ctx, sm, toolCall.Name, nextStateStr, resultMap)
				if err != nil {
					c.logger.Error("Error handling tool state transition: %v", err)
					continue
				}

				// Map state to signal for toolloop
				switch newState {
				case StatePlanReview:
					return "PLAN_REVIEW"
				default:
					c.logger.Warn("Unmapped terminal state from tool %s: %s", toolCall.Name, newState)
					return string(newState)
				}
			}
		}
	}

	return "" // No terminal signal, continue loop
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

	// Get knowledge pack from result or fall back to state data
	knowledgePack := utils.GetMapFieldOr[string](resultMap, "knowledge_pack", "")
	if knowledgePack == "" {
		knowledgePack = utils.GetStateValueOr[string](sm, string(stateDataKeyKnowledgePack), "")
	}

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

	// Store knowledge pack via persistence if available
	if knowledgePack != "" {
		storyID := utils.GetStateValueOr[string](sm, KeyStoryID, "")
		if storyID != "" {
			c.storeKnowledgePack(storyID, knowledgePack)
		}
	}

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

// retrieveKnowledgePack extracts key terms from story content and retrieves relevant knowledge.
func (c *Coder) retrieveKnowledgePack(_ context.Context, taskContent string) (string, error) {
	// Parse story content to extract description and acceptance criteria
	description, acceptanceCriteria := parseStoryContent(taskContent)

	// Extract key terms from story content
	searchTerms := knowledge.ExtractKeyTerms(description, acceptanceCriteria)

	if searchTerms == "" {
		return "", fmt.Errorf("no search terms extracted from story content")
	}

	c.logger.Debug("📚 Extracted search terms: %s", searchTerms)

	// Get session ID from config
	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	// Create response channel for query
	responseChan := make(chan interface{}, 1)

	// Send retrieval request via persistence queue
	c.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpRetrieveKnowledgePack,
		Data: &persistence.RetrieveKnowledgePackRequest{
			SessionID:   cfg.SessionID,
			SearchTerms: searchTerms,
			Level:       "all", // Include both architecture and implementation
			MaxResults:  20,
			Depth:       1, // Include immediate neighbors
		},
		Response: responseChan,
	}

	// Wait for response with timeout
	select {
	case resp := <-responseChan:
		if err, ok := resp.(error); ok {
			return "", fmt.Errorf("knowledge retrieval failed: %w", err)
		}
		if result, ok := resp.(*persistence.RetrieveKnowledgePackResponse); ok {
			return result.Subgraph, nil
		}
		return "", fmt.Errorf("unexpected response type: %T", resp)
	case <-time.After(5 * time.Second):
		return "", fmt.Errorf("knowledge retrieval timed out")
	}
}

// parseStoryContent parses markdown story content to extract description and acceptance criteria.
// Story content format:
//
//	**Task**
//	<description>
//
//	**Acceptance Criteria**
//	* <criteria 1>
//	* <criteria 2>
func parseStoryContent(content string) (string, []string) {
	lines := strings.Split(content, "\n")
	var description strings.Builder
	var acceptanceCriteria []string
	inTask := false
	inCriteria := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if strings.HasPrefix(trimmed, "**Task**") {
			inTask = true
			inCriteria = false
			continue
		}

		if strings.HasPrefix(trimmed, "**Acceptance Criteria**") {
			inTask = false
			inCriteria = true
			continue
		}

		if inTask && trimmed != "" {
			description.WriteString(trimmed)
			description.WriteString(" ")
		}

		if inCriteria && strings.HasPrefix(trimmed, "*") {
			// Remove leading "* " from criteria
			criteria := strings.TrimPrefix(trimmed, "* ")
			criteria = strings.TrimPrefix(criteria, "- ")
			if criteria != "" {
				acceptanceCriteria = append(acceptanceCriteria, criteria)
			}
		}
	}

	return strings.TrimSpace(description.String()), acceptanceCriteria
}

// storeKnowledgePack stores the knowledge pack for a story via persistence queue.
func (c *Coder) storeKnowledgePack(storyID, knowledgePack string) {
	// Get config for session ID
	cfg, err := config.GetConfig()
	if err != nil {
		c.logger.Error("Failed to get config for knowledge pack storage: %v", err)
		return
	}

	// Extract search terms from the pack for metadata (store first line of nodes)
	searchTerms := ""
	if lines := strings.Split(knowledgePack, "\n"); len(lines) > 1 {
		// Use first node ID as representative search terms
		searchTerms = "knowledge-pack-" + storyID[:8]
	}

	// Count nodes in the pack
	nodeCount := strings.Count(knowledgePack, "[") // Rough approximation

	// Send to persistence queue (fire-and-forget)
	c.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpStoreKnowledgePack,
		Data: &persistence.StoreKnowledgePackRequest{
			StoryID:     storyID,
			SessionID:   cfg.SessionID,
			Subgraph:    knowledgePack,
			SearchTerms: searchTerms,
			NodeCount:   nodeCount,
		},
		Response: nil, // Fire-and-forget
	}

	c.logger.Debug("📚 Stored knowledge pack for story %s (%d nodes)", storyID, nodeCount)
}
