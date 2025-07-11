package agent

// GetStateData returns a copy of the current state data
func (d *BaseDriver) GetStateData() map[string]any {
	return d.StateMachine.(*BaseStateMachine).GetStateData()
}

// GetAgentType returns the type of the agent based on config
func (d *BaseDriver) GetAgentType() AgentType {
	agentType, err := ParseAgentType(d.config.Type)
	if err != nil {
		// Fallback to coder if parsing fails
		return AgentTypeCoder
	}
	return agentType
}
