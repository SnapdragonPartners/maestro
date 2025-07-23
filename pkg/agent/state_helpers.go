package agent

import "orchestrator/pkg/utils"

// GetStateData returns a copy of the current state data.
func (d *BaseDriver) GetStateData() map[string]any {
	if sm, ok := utils.SafeAssert[*BaseStateMachine](d.StateMachine); ok {
		return sm.GetStateData()
	}
	// Return empty map if assertion fails.
	return make(map[string]any)
}

// GetAgentType returns the type of the agent based on config.
func (d *BaseDriver) GetAgentType() Type {
	agentType, err := Parse(d.config.Type)
	if err != nil {
		// Fallback to coder if parsing fails.
		return TypeCoder
	}
	return agentType
}
