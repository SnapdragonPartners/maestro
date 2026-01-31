package coder

import (
	"context"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/demo"
	"orchestrator/pkg/proto"
)

// handleDone processes the DONE state - terminal state indicating successful completion.
//
//nolint:unparam // error return required by state machine interface, always nil for terminal states
func (c *Coder) handleDone(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// DONE is terminal - orchestrator will handle all cleanup and restart.
	// Only log once when entering DONE state to avoid spam.
	if val, exists := sm.GetStateValue(KeyDoneLogged); !exists || val != true {
		c.logger.Info("🧑‍💻 Agent in DONE state - orchestrator will handle cleanup and restart")
		sm.SetStateData(KeyDoneLogged, true)

		// Cleanup compose stack if it exists
		c.cleanupComposeStack(ctx, sm)
	}

	// Return done=true to stop the run loop.
	return proto.StateDone, true, nil
}

// cleanupComposeStack tears down compose services and network for the coder.
func (c *Coder) cleanupComposeStack(ctx context.Context, sm *agent.BaseStateMachine) {
	workspacePathVal, exists := sm.GetStateValue(KeyWorkspacePath)
	if !exists {
		return
	}
	workspacePath, ok := workspacePathVal.(string)
	if !ok || workspacePath == "" {
		return
	}

	// Check if compose file exists
	if !demo.ComposeFileExists(workspacePath) {
		return
	}

	// Project name must match the naming convention in compose_up tool
	projectName := "maestro-" + c.GetAgentID()
	composePath := demo.ComposeFilePath(workspacePath)

	c.logger.Info("🐳 Cleaning up compose stack for agent %s", projectName)

	// Teardown compose stack with volumes
	stack := demo.NewStack(projectName, composePath, "")
	if err := stack.Down(ctx); err != nil {
		c.logger.Warn("⚠️ Failed to teardown compose stack: %v", err)
	}

	// Remove network
	networkName := projectName + "-network"
	networkMgr := demo.NewNetworkManager()
	if err := networkMgr.RemoveNetwork(ctx, networkName); err != nil {
		c.logger.Debug("Network %s removal: %v (may not exist)", networkName, err)
	}

	c.logger.Info("✅ Compose stack cleanup complete for agent %s", projectName)
}

// handleError processes the ERROR state - terminal state indicating failure.
//
//nolint:unparam // error return required by state machine interface, always nil for terminal states
func (c *Coder) handleError(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// ERROR is truly terminal - orchestrator handles all cleanup and story requeue.
	// Only log once when entering ERROR state to avoid spam.
	if val, exists := sm.GetStateValue(KeyDoneLogged); !exists || val != true {
		errorMsg, _ := sm.GetStateValue(KeyErrorMessage)
		c.logger.Error("🧑‍💻 Agent in ERROR state: %v - orchestrator will handle cleanup and story requeue", errorMsg)
		sm.SetStateData(KeyDoneLogged, true)

		// Cleanup compose stack if it exists
		c.cleanupComposeStack(ctx, sm)
	}

	// Return done=true to stop the run loop - orchestrator handles everything else.
	return proto.StateError, true, nil
}
