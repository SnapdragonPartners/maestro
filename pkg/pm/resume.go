// Package pm provides the implementation of the PM (Product Manager) agent.
package pm

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

// SerializeState sends the PM's current state to the persistence queue for saving.
// This uses the same persistence channel as Checkpoint to maintain FIFO ordering.
// The caller should drain the persistence queue after calling this to ensure the state is written.
func (d *Driver) SerializeState(_ context.Context, _ *sql.DB, sessionID string) error {
	if d.persistenceChannel == nil {
		return fmt.Errorf("persistence channel not available")
	}

	d.logger.Info("Serializing state for resume (session=%s)", sessionID)

	// Get current state from state machine.
	currentState := d.GetCurrentState()

	// Serialize spec content from state data.
	var specContent *string
	if spec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, ""); spec != "" {
		specContent = &spec
	}

	// Serialize bootstrap params from state data.
	bootstrapParamsJSON := d.collectBootstrapParamsJSON()

	// Build PM state.
	state := &persistence.PMState{
		SessionID:           sessionID,
		State:               string(currentState),
		SpecContent:         specContent,
		BootstrapParamsJSON: bootstrapParamsJSON,
	}

	// Build context if available.
	var context *persistence.AgentContext
	if d.contextManager != nil {
		contextData, err := d.contextManager.Serialize()
		if err != nil {
			d.logger.Warn("Failed to serialize context manager: %v", err)
		} else {
			context = &persistence.AgentContext{
				SessionID:    sessionID,
				AgentID:      d.GetAgentID(),
				ContextType:  "main",
				MessagesJSON: string(contextData),
			}
		}
	}

	// Send to persistence queue (blocking send to ensure it's queued for shutdown).
	// FIFO ordering ensures this is processed after any pending checkpoints.
	d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpCheckpointPMState,
		Data: &persistence.CheckpointPMStateRequest{
			State:   state,
			Context: context,
		},
	}

	d.logger.Info("State serialization queued (state=%s)", currentState)
	return nil
}

// RestoreState loads the PM's state from the database.
func (d *Driver) RestoreState(_ context.Context, db *sql.DB, sessionID string) error {
	d.logger.Info("Restoring state from session %s", sessionID)

	// Load PM state.
	state, err := persistence.GetPMState(db, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get PM state: %w", err)
	}

	// Restore state machine state using ForceState (bypasses transition validation).
	d.ForceState(proto.State(state.State))

	// Restore spec content (used by WebUI for preview via GetDraftSpec).
	// The PM will handle the appropriate state transitions based on current state.
	if state.SpecContent != nil {
		d.SetStateData(StateKeyUserSpecMd, *state.SpecContent)
	}

	// Restore bootstrap params.
	if state.BootstrapParamsJSON != nil {
		var bootstrapParams map[string]any
		if unmarshalErr := json.Unmarshal([]byte(*state.BootstrapParamsJSON), &bootstrapParams); unmarshalErr != nil {
			d.logger.Warn("Failed to unmarshal bootstrap params: %v", unmarshalErr)
		} else {
			for key, value := range bootstrapParams {
				d.SetStateData(key, value)
			}
		}
	}

	// Restore context manager.
	contexts, err := persistence.GetAgentContexts(db, sessionID, d.GetAgentID())
	if err != nil {
		return fmt.Errorf("failed to get agent contexts: %w", err)
	}

	for i := range contexts {
		if contexts[i].ContextType == "main" {
			if d.contextManager == nil {
				d.contextManager = contextmgr.NewContextManager()
			}
			if err := d.contextManager.Deserialize([]byte(contexts[i].MessagesJSON)); err != nil {
				return fmt.Errorf("failed to deserialize context manager: %w", err)
			}
			break
		}
	}

	d.logger.Info("State restored successfully (state=%s)", state.State)
	return nil
}

// Checkpoint sends the PM's current state to the persistence channel for saving.
// This is a fire-and-forget operation used for crash recovery checkpoints.
// It should be called when a spec is submitted (completion boundary).
func (d *Driver) Checkpoint(sessionID string) {
	if d.persistenceChannel == nil {
		d.logger.Debug("Skipping checkpoint: persistence channel not available")
		return
	}

	// Get current state from state machine.
	currentState := d.GetCurrentState()

	// Serialize spec content from state data.
	var specContent *string
	if spec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, ""); spec != "" {
		specContent = &spec
	}

	// Serialize bootstrap params from state data.
	bootstrapParamsJSON := d.collectBootstrapParamsJSON()

	// Build PM state
	state := &persistence.PMState{
		SessionID:           sessionID,
		State:               string(currentState),
		SpecContent:         specContent,
		BootstrapParamsJSON: bootstrapParamsJSON,
	}

	// Build context if available
	var context *persistence.AgentContext
	if d.contextManager != nil {
		contextData, err := d.contextManager.Serialize()
		if err != nil {
			d.logger.Warn("Failed to serialize context manager during checkpoint: %v", err)
		} else {
			context = &persistence.AgentContext{
				SessionID:    sessionID,
				AgentID:      d.GetAgentID(),
				ContextType:  "main",
				MessagesJSON: string(contextData),
			}
		}
	}

	// Send checkpoint request (fire-and-forget)
	select {
	case d.persistenceChannel <- &persistence.Request{
		Operation: persistence.OpCheckpointPMState,
		Data: &persistence.CheckpointPMStateRequest{
			State:   state,
			Context: context,
		},
	}:
		d.logger.Debug("Checkpoint request sent (state=%s)", currentState)
	default:
		d.logger.Warn("Persistence channel full, checkpoint skipped")
	}
}

// collectBootstrapParamsJSON collects bootstrap-related state data and returns it as JSON.
// Returns nil if no bootstrap params are present.
func (d *Driver) collectBootstrapParamsJSON() *string {
	bootstrapParams := make(map[string]any)

	// Collect all bootstrap-related state keys using type-safe utilities
	if hasRepo, ok := utils.GetStateValue[bool](d.BaseStateMachine, StateKeyHasRepository); ok {
		bootstrapParams[StateKeyHasRepository] = hasRepo
	}
	if expertise, ok := utils.GetStateValue[string](d.BaseStateMachine, StateKeyUserExpertise); ok {
		bootstrapParams[StateKeyUserExpertise] = expertise
	}
	if requirements, ok := d.GetStateValue(StateKeyBootstrapRequirements); ok {
		bootstrapParams[StateKeyBootstrapRequirements] = requirements
	}
	// Note: Platform is NOT persisted here - it's stored in config when user confirms
	if inFlight, ok := utils.GetStateValue[bool](d.BaseStateMachine, StateKeyInFlight); ok {
		bootstrapParams[StateKeyInFlight] = inFlight
	}
	// Note: StateKeyBootstrapSpecMd is no longer used - architect renders bootstrap spec from requirement IDs
	if isHotfix, ok := utils.GetStateValue[bool](d.BaseStateMachine, StateKeyIsHotfix); ok {
		bootstrapParams[StateKeyIsHotfix] = isHotfix
	}

	if len(bootstrapParams) == 0 {
		return nil
	}

	data, err := json.Marshal(bootstrapParams)
	if err != nil {
		d.logger.Warn("Failed to marshal bootstrap params: %v", err)
		return nil
	}
	s := string(data)
	return &s
}
