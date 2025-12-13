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
	// Priority: user_spec_md (new) > draft_spec_markdown (legacy)
	var specContent *string
	if spec := utils.GetStateValueOr[string](d.BaseStateMachine, StateKeyUserSpecMd, ""); spec != "" {
		specContent = &spec
	} else if spec := utils.GetStateValueOr[string](d.BaseStateMachine, "draft_spec_markdown", ""); spec != "" {
		specContent = &spec
	}

	// Serialize bootstrap params from state data.
	bootstrapParamsJSON := d.collectBootstrapParamsJSON()

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

	// Restore spec content to both new and legacy keys (used by WebUI for preview).
	// The PM will handle the appropriate state transitions based on current state.
	if state.SpecContent != nil {
		d.SetStateData(StateKeyUserSpecMd, *state.SpecContent)
		d.SetStateData("draft_spec_markdown", *state.SpecContent) // Legacy compatibility
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

// collectBootstrapParamsJSON collects bootstrap-related state data and returns it as JSON.
// Returns nil if no bootstrap params are present.
func (d *Driver) collectBootstrapParamsJSON() *string {
	stateData := d.GetStateData()
	bootstrapParams := make(map[string]any)

	// Collect all bootstrap-related state keys
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
	if inFlight, ok := stateData[StateKeyInFlight].(bool); ok {
		bootstrapParams[StateKeyInFlight] = inFlight
	}
	if bootstrapSpec, ok := stateData[StateKeyBootstrapSpecMd].(string); ok {
		bootstrapParams[StateKeyBootstrapSpecMd] = bootstrapSpec
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
