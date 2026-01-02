package coder

import (
	"context"
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/agent/toolloop"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/tools"
	"orchestrator/pkg/utils"
)

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
		ContextManager:     c.contextManager,
		InitialPrompt:      "",             // Prompt already in context via ResetForNewTemplate
		GeneralTools:       []tools.Tool{}, // No general tools
		TerminalTool:       terminalTool,
		MaxIterations:      2,    // One call + one retry if needed
		MaxTokens:          4096, // Sufficient for todo list
		AgentID:            c.GetAgentID(),
		DebugLogging:       config.GetDebugLLMMessages(),
		PersistenceChannel: c.persistenceChannel,
		StoryID:            utils.GetStateValueOr[string](sm, KeyStoryID, ""),
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
