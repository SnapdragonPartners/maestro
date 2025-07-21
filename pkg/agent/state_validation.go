package agent

import (
	"sync"

	"orchestrator/pkg/proto"
)

// ValidTransitions defines allowed state transitions for generic agent states
// Note: Specialized agents (like coder) can override transition validation
// Protected by validTransitionsMux for thread safety
var ValidTransitions = map[proto.State][]proto.State{
	proto.StateDone:    {proto.StateWaiting}, // Can start over from done
	proto.StateError:   {proto.StateWaiting}, // Can retry from error
	proto.StateWaiting: {},                   // Empty - derived agents should define valid transitions from WAITING
}

// ValidTransitionsMux protects ValidTransitions map from concurrent access
var ValidTransitionsMux sync.RWMutex

// allowSelfLoop permits staying in the same state (for long-running states)
func allowSelfLoop(from, to proto.State) bool {
	return from == to
}

// IsValidTransition checks if a state transition is allowed using the instance table
func (sm *BaseStateMachine) IsValidTransition(from, to proto.State) bool {
	// Allow self-loops for long-running states (shared by coder and architect)
	if allowSelfLoop(from, to) {
		return true
	}

	// Allow any transition to error state
	if to == proto.StateError {
		return true
	}

	// Use instance-local transition table (no global mutex needed)
	allowed, ok := sm.table[from]
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

// CloneTransitionTable creates a deep copy of a transition table
func CloneTransitionTable(src TransitionTable) TransitionTable {
	dst := make(TransitionTable, len(src))
	for k, v := range src {
		cp := make([]proto.State, len(v))
		copy(cp, v)
		dst[k] = cp
	}
	return dst
}
