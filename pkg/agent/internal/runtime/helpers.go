package runtime

import (
	"orchestrator/pkg/agent/internal/core"
	"orchestrator/pkg/utils"
)

// GetStateData returns a copy of the current state data.
func (d *BaseDriver) GetStateData() map[string]any {
	if sm, ok := utils.SafeAssert[*core.BaseStateMachine](d.StateMachine); ok {
		return sm.GetStateData()
	}
	// Return empty map if assertion fails.
	return make(map[string]any)
}

// GetAgentType returns the type of the agent based on config.
func (d *BaseDriver) GetAgentType() AgentType {
	return ParseAgentType(d.config.Type)
}
