package agent

// ValidTransitions defines allowed state transitions for each state
var ValidTransitions = map[State][]State{
	// Basic FSM transitions
	StatePlanning: {StateCoding, StateError, StatePlanReview, StateQuestion},
	StateCoding:   {StateTesting, StateError, StatePlanning, StateQuestion},
	StateTesting:  {StateDone, StateCoding, StateError, StateFixing, StateCodeReview},
	StateDone:     {StatePlanning, StateWaiting}, // Can start over from done
	StateError:    {StatePlanning, StateWaiting}, // Can retry from error
	
	// Extended v2 FSM transitions
	StateWaiting:     {StatePlanning, StateError},
	StatePlanReview:  {StateCoding, StatePlanning, StateError}, // Approve→CODING, Reject→PLANNING
	StateFixing:      {StateCoding, StateTesting, StateError},  // Fix→CODING or retry TESTING
	StateCodeReview:  {StateDone, StateFixing, StateError},    // Approve→DONE, Reject→FIXING
	StateQuestion:    {StatePlanning, StateCoding, StateFixing, StateError}, // Return to origin state
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