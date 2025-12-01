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

// SerializeState persists the PM's current state to the database for resume.
func (d *Driver) SerializeState(_ context.Context, db *sql.DB, sessionID string) error {
	d.logger.Info("Serializing state for resume (session=%s)", sessionID)

	// Get current state from state machine.
	currentState := d.GetCurrentState()

	// Serialize spec content from state data.
	// Priority: draft_spec_markdown (in-progress) > draft_spec (submitted) > spec_markdown (approved)
	var specContent *string
	if spec := utils.GetStateValueOr[string](d.BaseStateMachine, "draft_spec_markdown", ""); spec != "" {
		specContent = &spec
	} else if spec := utils.GetStateValueOr[string](d.BaseStateMachine, "draft_spec", ""); spec != "" {
		specContent = &spec
	} else if spec := utils.GetStateValueOr[string](d.BaseStateMachine, "spec_markdown", ""); spec != "" {
		specContent = &spec
	}

	// Serialize bootstrap params from state data.
	var bootstrapParamsJSON *string
	stateData := d.GetStateData()

	// Collect bootstrap-related state data.
	bootstrapParams := make(map[string]any)
	if hasRepo, ok := stateData[StateKeyHasRepository].(bool); ok {
		bootstrapParams[StateKeyHasRepository] = hasRepo
	}
	if expertise, ok := stateData[StateKeyUserExpertise].(string); ok {
		bootstrapParams[StateKeyUserExpertise] = expertise
	}
	if requirements, ok := stateData[StateKeyBootstrapRequirements]; ok {
		bootstrapParams[StateKeyBootstrapRequirements] = requirements
	}
	if platform, ok := stateData[StateKeyDetectedPlatform].(string); ok {
		bootstrapParams[StateKeyDetectedPlatform] = platform
	}
	if devInProgress, ok := stateData[StateKeyDevelopmentInProgress].(bool); ok {
		bootstrapParams[StateKeyDevelopmentInProgress] = devInProgress
	}

	if len(bootstrapParams) > 0 {
		data, err := json.Marshal(bootstrapParams)
		if err != nil {
			return fmt.Errorf("failed to marshal bootstrap params: %w", err)
		}
		s := string(data)
		bootstrapParamsJSON = &s
	}

	// Save PM state.
	state := &persistence.PMState{
		SessionID:           sessionID,
		State:               string(currentState),
		SpecContent:         specContent,
		BootstrapParamsJSON: bootstrapParamsJSON,
	}

	if err := persistence.SavePMState(db, state); err != nil {
		return fmt.Errorf("failed to save PM state: %w", err)
	}

	// Save context manager state.
	if d.contextManager != nil {
		contextData, err := d.contextManager.Serialize()
		if err != nil {
			return fmt.Errorf("failed to serialize context manager: %w", err)
		}

		agentCtx := &persistence.AgentContext{
			SessionID:    sessionID,
			AgentID:      d.GetAgentID(),
			ContextType:  "main",
			MessagesJSON: string(contextData),
		}

		if err := persistence.SaveAgentContext(db, agentCtx); err != nil {
			return fmt.Errorf("failed to save agent context: %w", err)
		}
	}

	d.logger.Info("State serialized successfully (state=%s)", currentState)
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

	// Restore spec content to draft_spec_markdown (used by WebUI for preview).
	// The PM will handle the appropriate state transitions based on current state.
	if state.SpecContent != nil {
		d.SetStateData("draft_spec_markdown", *state.SpecContent)
		d.SetStateData("draft_spec", *state.SpecContent)
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
