// Package architect provides the implementation of the architect agent.
package architect

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/proto"
)

// SerializeState persists the architect's current state to the database for resume.
func (d *Driver) SerializeState(_ context.Context, db *sql.DB, sessionID string) error {
	d.logger.Info("Serializing state for resume (session=%s)", sessionID)

	// Get current state from state machine.
	currentState := d.GetCurrentState()

	// Serialize escalation counts from the handler.
	// Note: The escalation handler persists its own state via file, but we need
	// the iteration counts for resume. For now, we'll skip this since
	// escalation state is file-based. In a future enhancement, we could
	// serialize active escalations.
	var escalationCountsJSON *string
	_ = escalationCountsJSON // Will be populated in future enhancement

	// Save architect state.
	state := &persistence.ArchitectState{
		SessionID:            sessionID,
		State:                string(currentState),
		EscalationCountsJSON: escalationCountsJSON,
	}

	if err := persistence.SaveArchitectState(db, state); err != nil {
		return fmt.Errorf("failed to save architect state: %w", err)
	}

	// Save per-agent contexts.
	d.contextMutex.RLock()
	defer d.contextMutex.RUnlock()

	for agentID, cm := range d.agentContexts {
		contextData, err := cm.Serialize()
		if err != nil {
			d.logger.Warn("Failed to serialize context for agent %s: %v", agentID, err)
			continue
		}

		agentCtx := &persistence.AgentContext{
			SessionID:    sessionID,
			AgentID:      "architect",
			ContextType:  agentID, // Context type is the target agent ID.
			MessagesJSON: string(contextData),
		}

		if err := persistence.SaveAgentContext(db, agentCtx); err != nil {
			d.logger.Warn("Failed to save context for agent %s: %v", agentID, err)
			continue
		}
	}

	d.logger.Info("State serialized successfully (state=%s, contexts=%d)", currentState, len(d.agentContexts))
	return nil
}

// RestoreState loads the architect's state from the database.
func (d *Driver) RestoreState(_ context.Context, db *sql.DB, sessionID string) error {
	d.logger.Info("Restoring state from session %s", sessionID)

	// Load architect state.
	state, err := persistence.GetArchitectState(db, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get architect state: %w", err)
	}

	// Restore state machine state using ForceState (bypasses transition validation).
	d.ForceState(proto.State(state.State))

	// Restore escalation counts if present.
	if state.EscalationCountsJSON != nil {
		// Parse and restore escalation counts.
		var counts map[string]int
		if unmarshalErr := json.Unmarshal([]byte(*state.EscalationCountsJSON), &counts); unmarshalErr != nil {
			d.logger.Warn("Failed to unmarshal escalation counts: %v", unmarshalErr)
		}
		// The escalation handler manages its own state via files.
		// We would need to add a method to restore counts if needed.
	}

	// Restore per-agent contexts.
	contexts, err := persistence.GetAgentContexts(db, sessionID, "architect")
	if err != nil {
		return fmt.Errorf("failed to get agent contexts: %w", err)
	}

	d.contextMutex.Lock()
	for i := range contexts {
		agentID := contexts[i].ContextType
		if agentID == "main" {
			// Skip main context for architect (uses per-agent contexts).
			continue
		}

		cm := contextmgr.NewContextManager()
		if deserializeErr := cm.Deserialize([]byte(contexts[i].MessagesJSON)); deserializeErr != nil {
			d.logger.Warn("Failed to deserialize context for agent %s: %v", agentID, deserializeErr)
			continue
		}

		d.agentContexts[agentID] = cm
	}
	d.contextMutex.Unlock()

	// Restore stories from the database into the queue.
	// Only incomplete stories (not 'done' or 'failed') are loaded.
	stories, err := persistence.GetIncompleteStoriesForSession(db, sessionID)
	if err != nil {
		d.logger.Warn("Failed to get incomplete stories for session: %v", err)
		// Continue without stories - the architect can function without pre-existing stories.
	} else if len(stories) > 0 {
		loadedCount := d.queue.LoadStoriesFromDB(stories)
		d.logger.Info("Loaded %d incomplete stories from database", loadedCount)
	}

	d.logger.Info("State restored successfully (state=%s, contexts=%d, stories=%d)",
		state.State, len(d.agentContexts), len(stories))
	return nil
}

// Checkpoint sends the architect's current state to the persistence channel for saving.
// This is a fire-and-forget operation used for crash recovery checkpoints.
// It should be called when a story is marked done (completion boundary).
func (d *Driver) Checkpoint(sessionID string) {
	if d.persistenceChannel == nil {
		d.logger.Debug("Skipping checkpoint: persistence channel not available")
		return
	}

	// Build architect state
	currentState := d.GetCurrentState()
	state := &persistence.ArchitectState{
		SessionID: sessionID,
		State:     string(currentState),
	}

	// Build per-agent contexts
	d.contextMutex.RLock()
	contexts := make([]*persistence.AgentContext, 0, len(d.agentContexts))
	for agentID, cm := range d.agentContexts {
		contextData, err := cm.Serialize()
		if err != nil {
			d.logger.Warn("Failed to serialize context for agent %s during checkpoint: %v", agentID, err)
			continue
		}
		contexts = append(contexts, &persistence.AgentContext{
			SessionID:    sessionID,
			AgentID:      "architect",
			ContextType:  agentID,
			MessagesJSON: string(contextData),
		})
	}
	d.contextMutex.RUnlock()

	// Send checkpoint request (fire-and-forget)
	select {
	case d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpCheckpointArchitectState,
		Data: &persistence.CheckpointArchitectStateRequest{
			State:    state,
			Contexts: contexts,
		},
	}:
		d.logger.Debug("Checkpoint request sent (state=%s, contexts=%d)", currentState, len(contexts))
	default:
		d.logger.Warn("Persistence channel full, checkpoint skipped")
	}
}
