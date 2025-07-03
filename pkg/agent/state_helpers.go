package agent

// GetStateData returns a copy of the current state data
func (d *BaseDriver) GetStateData() map[string]any {
	return d.StateMachine.(*BaseStateMachine).GetStateData()
}
