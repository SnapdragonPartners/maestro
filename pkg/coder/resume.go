// Package coder provides the implementation of the coding agent.
package coder

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// Resumable is implemented by agents that support graceful shutdown and resume.
type Resumable interface {
	// SerializeState persists the agent's current state to the database.
	SerializeState(ctx context.Context, db *sql.DB, sessionID string) error
	// RestoreState loads the agent's state from the database.
	RestoreState(ctx context.Context, db *sql.DB, sessionID string) error
}

// SerializeState persists the coder's current state to the database for resume.
func (c *Coder) SerializeState(_ context.Context, db *sql.DB, sessionID string) error {
	agentID := c.GetAgentID()
	c.logger.Info("Serializing state for resume (session=%s)", sessionID)

	// Get current state from state machine.
	currentState := c.GetCurrentState()
	storyID := utils.GetStateValueOr[string](c.BaseStateMachine, KeyStoryID, "")

	// Serialize plan from state data.
	var planJSON *string
	if plan := utils.GetStateValueOr[string](c.BaseStateMachine, KeyPlan, ""); plan != "" {
		planJSON = &plan
	}

	// Serialize todo list.
	var todoListJSON *string
	if c.todoList != nil {
		data, err := json.Marshal(c.todoList)
		if err != nil {
			return fmt.Errorf("failed to marshal todo list: %w", err)
		}
		s := string(data)
		todoListJSON = &s
	}

	// Get current todo index.
	currentTodoIndex := 0
	if c.todoList != nil {
		currentTodoIndex = c.todoList.Current
	}

	// Serialize knowledge pack.
	var knowledgePackJSON *string
	if kp := utils.GetStateValueOr[string](c.BaseStateMachine, string(stateDataKeyKnowledgePack), ""); kp != "" {
		knowledgePackJSON = &kp
	}

	// Serialize pending request (QUESTION or REQUEST).
	var pendingRequestType, pendingRequestJSON *string
	if c.pendingQuestion != nil {
		t := "QUESTION"
		pendingRequestType = &t
		data, err := json.Marshal(c.pendingQuestion)
		if err != nil {
			return fmt.Errorf("failed to marshal pending question: %w", err)
		}
		s := string(data)
		pendingRequestJSON = &s
	} else if c.pendingApprovalRequest != nil {
		t := "REQUEST"
		pendingRequestType = &t
		data, err := json.Marshal(c.pendingApprovalRequest)
		if err != nil {
			return fmt.Errorf("failed to marshal pending approval request: %w", err)
		}
		s := string(data)
		pendingRequestJSON = &s
	}

	// Container image (if any).
	var containerImage *string
	if c.containerName != "" {
		containerImage = &c.containerName
	}

	// Story ID pointer.
	var storyIDPtr *string
	if storyID != "" {
		storyIDPtr = &storyID
	}

	// Save coder state.
	state := &persistence.CoderState{
		SessionID:          sessionID,
		AgentID:            agentID,
		StoryID:            storyIDPtr,
		State:              string(currentState),
		PlanJSON:           planJSON,
		TodoListJSON:       todoListJSON,
		CurrentTodoIndex:   currentTodoIndex,
		KnowledgePackJSON:  knowledgePackJSON,
		PendingRequestType: pendingRequestType,
		PendingRequestJSON: pendingRequestJSON,
		ContainerImage:     containerImage,
	}

	if err := persistence.SaveCoderState(db, state); err != nil {
		return fmt.Errorf("failed to save coder state: %w", err)
	}

	// Save context manager state.
	if c.contextManager != nil {
		contextData, err := c.contextManager.Serialize()
		if err != nil {
			return fmt.Errorf("failed to serialize context manager: %w", err)
		}

		agentCtx := &persistence.AgentContext{
			SessionID:    sessionID,
			AgentID:      agentID,
			ContextType:  "main",
			MessagesJSON: string(contextData),
		}

		if err := persistence.SaveAgentContext(db, agentCtx); err != nil {
			return fmt.Errorf("failed to save agent context: %w", err)
		}
	}

	c.logger.Info("State serialized successfully (state=%s, story=%s)", currentState, storyID)
	return nil
}

// RestoreState loads the coder's state from the database.
func (c *Coder) RestoreState(_ context.Context, db *sql.DB, sessionID string) error {
	agentID := c.GetAgentID()
	c.logger.Info("Restoring state from session %s", sessionID)

	// Load coder state.
	state, err := persistence.GetCoderState(db, sessionID, agentID)
	if err != nil {
		return fmt.Errorf("failed to get coder state: %w", err)
	}

	// Restore state machine state using ForceState (bypasses transition validation).
	c.ForceState(proto.State(state.State))

	// Restore story ID.
	if state.StoryID != nil {
		c.BaseStateMachine.SetStateData(KeyStoryID, *state.StoryID)
	}

	// Restore plan.
	if state.PlanJSON != nil {
		c.BaseStateMachine.SetStateData(KeyPlan, *state.PlanJSON)
	}

	// Restore todo list.
	if state.TodoListJSON != nil {
		var todoList TodoList
		if unmarshalErr := json.Unmarshal([]byte(*state.TodoListJSON), &todoList); unmarshalErr != nil {
			return fmt.Errorf("failed to unmarshal todo list: %w", unmarshalErr)
		}
		c.todoList = &todoList
	}

	// Restore knowledge pack.
	if state.KnowledgePackJSON != nil {
		c.BaseStateMachine.SetStateData(string(stateDataKeyKnowledgePack), *state.KnowledgePackJSON)
	}

	// Restore pending request.
	if state.PendingRequestType != nil && state.PendingRequestJSON != nil {
		switch *state.PendingRequestType {
		case "QUESTION":
			var question Question
			if unmarshalErr := json.Unmarshal([]byte(*state.PendingRequestJSON), &question); unmarshalErr != nil {
				return fmt.Errorf("failed to unmarshal pending question: %w", unmarshalErr)
			}
			c.pendingQuestion = &question
		case "REQUEST":
			var request ApprovalRequest
			if unmarshalErr := json.Unmarshal([]byte(*state.PendingRequestJSON), &request); unmarshalErr != nil {
				return fmt.Errorf("failed to unmarshal pending approval request: %w", unmarshalErr)
			}
			c.pendingApprovalRequest = &request
		}
	}

	// Restore container name.
	if state.ContainerImage != nil {
		c.containerName = *state.ContainerImage
	}

	// Restore context manager.
	contexts, err := persistence.GetAgentContexts(db, sessionID, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent contexts: %w", err)
	}

	for i := range contexts {
		if contexts[i].ContextType == "main" {
			if c.contextManager == nil {
				c.contextManager = contextmgr.NewContextManager()
			}
			if err := c.contextManager.Deserialize([]byte(contexts[i].MessagesJSON)); err != nil {
				return fmt.Errorf("failed to deserialize context manager: %w", err)
			}
			break
		}
	}

	c.logger.Info("State restored successfully (state=%s)", state.State)
	return nil
}
