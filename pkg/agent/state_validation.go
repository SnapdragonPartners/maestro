package agent

// ValidTransitions defines allowed state transitions for generic agent states
// Note: Specialized agents (like coder) can override transition validation
var ValidTransitions = map[State][]State{
	StateDone:    {StateWaiting}, // Can start over from done
	StateError:   {StateWaiting}, // Can retry from error
	StateWaiting: {},             // Empty - derived agents should define valid transitions from WAITING
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
