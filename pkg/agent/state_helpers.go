package agent

// GetStateData returns a copy of the current state data
func (d *BaseDriver) GetStateData() map[string]interface{} {
	return d.StateMachine.(*BaseStateMachine).GetStateData()
}