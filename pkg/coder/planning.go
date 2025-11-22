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
	loop := toolloop.New(c.LLMClient, c.logger)

	//nolint:dupl // Similar config in coding.go - intentional per-state configuration
	cfg := &toolloop.Config[PlanningResult]{
		ContextManager: c.contextManager,
		InitialPrompt:  "", // Prompt already in context via ResetForNewTemplate
		ToolProvider:   c.planningToolProvider,
		MaxIterations:  maxPlanningIterations,
		MaxTokens:      8192, // Increased for exploration
		AgentID:        c.GetAgentID(),
		DebugLogging:   false,
		CheckTerminal: func(calls []agent.ToolCall, results []any) string {
			return c.checkPlanningTerminal(ctx, sm, calls, results)
		},
		ExtractResult: ExtractPlanningResult,
		Escalation: &toolloop.EscalationConfig{
			Key:       fmt.Sprintf("planning_%s", utils.GetStateValueOr[string](sm, KeyStoryID, "unknown")),
			SoftLimit: maxPlanningIterations - 2, // Warn 2 iterations before limit
			HardLimit: maxPlanningIterations,
			OnHardLimit: func(_ context.Context, key string, count int) error {
				c.logger.Info("⚠️  Planning reached max iterations (%d, key: %s), triggering budget review", count, key)

				// Render full budget review content with template (same as checkLoopBudget)
				content := c.getBudgetReviewContent(sm, StatePlanning, count, maxPlanningIterations)
				if content == "" {
					return logx.Errorf("failed to generate budget review content - cannot proceed without proper context for architect")
				}

				budgetEff := effect.NewBudgetReviewEffect(
					content,
					"Planning workflow needs additional iterations to complete exploration",
					string(StatePlanning),
				)
				// Set story ID for dispatcher validation
				budgetEff.StoryID = utils.GetStateValueOr[string](sm, KeyStoryID, "")
				sm.SetStateData("budget_review_effect", budgetEff)
				// Store origin state so budget review knows where to return
				sm.SetStateData(KeyOrigin, string(StatePlanning))
				c.logger.Info("🔍 Toolloop iteration limit: stored origin=%q in state data", string(StatePlanning))
				// Return nil so toolloop returns IterationLimitError (not this error)
				return nil
			},
		},
	}

	out := toolloop.Run(loop, ctx, cfg)

	// Switch on outcome kind first
	switch out.Kind {
	case toolloop.OutcomeSuccess:
		// Process extracted result
		if err := c.processPlanningResult(sm, &out.Value); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to process planning result")
		}

		// Handle terminal signals from successful completion
		switch out.Signal {
		case string(StateBudgetReview):
			return StateBudgetReview, false, nil
		case string(StateQuestion):
			return StateQuestion, false, nil
		case string(StatePlanReview):
			return StatePlanReview, false, nil
		case "":
			// No signal, continue planning
			c.logger.Info("🧑‍💻 Planning iteration completed, staying in PLANNING")
			return StatePlanning, false, nil
		default:
			c.logger.Warn("Unknown signal from planning toolloop: %s", out.Signal)
			return StatePlanning, false, nil
		}

	case toolloop.OutcomeIterationLimit:
		// OnHardLimit already stored BudgetReviewEffect in state
		c.logger.Info("📊 Iteration limit reached (%d iterations), transitioning to BUDGET_REVIEW", out.Iteration)
		return StateBudgetReview, false, nil

	case toolloop.OutcomeLLMError, toolloop.OutcomeMaxIterations, toolloop.OutcomeExtractionError:
		// Check if this is an empty response error
		if c.isEmptyResponseError(out.Err) {
			req := agent.CompletionRequest{MaxTokens: 8192}
			return c.handleEmptyResponseError(sm, prompt, req, StatePlanning)
		}
		return proto.StateError, false, logx.Wrap(out.Err, "toolloop execution failed")

	case toolloop.OutcomeNoToolTwice:
		// LLM failed to use tools - treat as error
		return proto.StateError, false, logx.Wrap(out.Err, "LLM did not use tools in planning")

	default:
		return proto.StateError, false, logx.Errorf("unknown toolloop outcome kind: %v", out.Kind)
	}
}

// checkPlanningTerminal examines tool calls and results for terminal signals during planning.
// ONLY checks for signals - does not extract or process data (that's done by ExtractPlanningResult).
func (c *Coder) checkPlanningTerminal(_ context.Context, sm *agent.BaseStateMachine, calls []agent.ToolCall, results []any) string {
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
				c.logger.Info("🧑‍💻 Tool %s signaled next_state: %s", toolCall.Name, nextStateStr)
				return nextStateStr // Return signal directly
			}
		}
	}

	return "" // No terminal signal, continue loop
}

// processPlanningResult processes the extracted result from planning toolloop.
// Stores data in stateData and performs any necessary side effects.
//
//nolint:unparam // error return reserved for future validation logic
func (c *Coder) processPlanningResult(sm *agent.BaseStateMachine, result *PlanningResult) error {
	// Only process if we have plan data (i.e., submit_plan was called)
	if result.Signal == SignalPlanReview && result.Plan != "" {
		c.logger.Info("✅ Planning result extracted: plan (%d chars), confidence: %s",
			len(result.Plan), result.Confidence)

		// Get knowledge pack from result or fall back to state data
		knowledgePack := result.KnowledgePack
		if knowledgePack == "" {
			knowledgePack = utils.GetStateValueOr[string](sm, string(stateDataKeyKnowledgePack), "")
		}

		// Store plan data using typed constants
		sm.SetStateData(string(stateDataKeyPlan), result.Plan)
		sm.SetStateData(string(stateDataKeyPlanConfidence), result.Confidence)
		sm.SetStateData(string(stateDataKeyExplorationSummary), result.ExplorationSummary)
		sm.SetStateData(string(stateDataKeyPlanRisks), result.Risks)
		sm.SetStateData(string(stateDataKeyPlanTodos), result.Todos)
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
			Content: result.Plan,
			Reason:  fmt.Sprintf("Enhanced plan requires approval (confidence: %s)", result.Confidence),
			Type:    proto.ApprovalTypePlan,
		}

		c.logger.Info("🧑‍💻 Plan data stored, ready for PLAN_REVIEW transition")
	}

	return nil
}

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
