package coder

import (
	"context"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// handleDone processes the DONE state - terminal state indicating successful completion.
//
//nolint:unparam // error return required by state machine interface, always nil for terminal states
func (c *Coder) handleDone(_ /* ctx */ context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// DONE is terminal - orchestrator will handle all cleanup and restart.
	// Only log once when entering DONE state to avoid spam.
	if val, exists := sm.GetStateValue(KeyDoneLogged); !exists || val != true {
		c.logger.Info("🧑‍💻 Agent in DONE state - orchestrator will handle cleanup and restart")
		sm.SetStateData(KeyDoneLogged, true)
	}

	// Return done=true to stop the run loop.
	return proto.StateDone, true, nil
}

// handleError processes the ERROR state - terminal state indicating failure.
//
//nolint:unparam // error return required by state machine interface, always nil for terminal states
func (c *Coder) handleError(_ context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// ERROR is truly terminal - orchestrator handles all cleanup and story requeue.
	// Only log once when entering ERROR state to avoid spam.
	if val, exists := sm.GetStateValue(KeyDoneLogged); !exists || val != true {
		errorMsg, _ := sm.GetStateValue(KeyErrorMessage)
		c.logger.Error("🧑‍💻 Agent in ERROR state: %v - orchestrator will handle cleanup and story requeue", errorMsg)
		sm.SetStateData(KeyDoneLogged, true)
	}

	// Return done=true to stop the run loop - orchestrator handles everything else.
	return proto.StateError, true, nil
}
