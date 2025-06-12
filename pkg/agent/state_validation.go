package agent

// ValidTransitions defines allowed state transitions for each state
var ValidTransitions = map[State][]State{
	StatePlanning: {StateCoding, StateError},
	StateCoding:   {StateTesting, StateError, StatePlanning},
	StateTesting:  {StateDone, StateCoding, StateError},
	StateDone:     {StatePlanning}, // Can start over from done
	StateError:    {StatePlanning}, // Can retry from error
}

// IsValidTransition checks if a state transition is allowed
func (sm *BaseStateMachine) IsValidTransition(from, to State) bool {
	// Allow any transition to error state
	if to == StateError {
		return true
	}

	// Get allowed transitions for current state
	allowed, ok := ValidTransitions[from]
	if !ok {
		return false
	}

	// Check if requested state is in allowed list
	for _, s := range allowed {
		if s == to {
			return true
		}
	}

	return false
}