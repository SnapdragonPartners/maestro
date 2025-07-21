package architect

import (
	"fmt"
	"time"

	"orchestrator/pkg/proto"
)

// IMPORTANT: This file is the canonical implementation of the architect FSM
// and must always be kept in sync with pkg/architect/STATES.md.
// Any changes to this file require explicit human permission and must
// be accompanied by corresponding updates to the state diagram.
// This is the single source of truth for all architect state transitions.

// Architect state constants - derived directly from STATES.md
const (
	// Entry state
	StateWaiting proto.State = "WAITING"

	// Spec intake states
	StateScoping proto.State = "SCOPING"

	// Story dispatch states
	StateDispatching proto.State = "DISPATCHING"

	// Main event loop states
	StateMonitoring proto.State = "MONITORING"
	StateRequest    proto.State = "REQUEST"

	// Human escalation states
	StateEscalated proto.State = "ESCALATED"

	// Merge & unblock states
	StateMerging proto.State = "MERGING"

	// Terminal states
	StateDone  proto.State = "DONE"
	StateError proto.State = "ERROR"
)

// ValidateState checks if a state is valid for architect agents
func ValidateState(state proto.State) error {
	validStates := GetValidStates()
	for _, validState := range validStates {
		if state == validState {
			return nil
		}
	}
	return fmt.Errorf("invalid architect state: %s", state)
}

// GetValidStates returns all valid states for architect agents
func GetValidStates() []proto.State {
	return []proto.State{
		StateWaiting, StateScoping, StateDispatching, StateMonitoring,
		StateRequest, StateEscalated, StateMerging, StateDone, StateError,
	}
}

// architectTransitions defines the canonical state transition map for architect agents.
// This is the single source of truth, derived directly from STATES.md.
// Any code, tests, or diagrams must match this specification exactly.
var architectTransitions = map[proto.State][]proto.State{
	// WAITING can transition to SCOPING when spec received, or REQUEST when question received
	StateWaiting: {StateScoping, StateRequest},

	// SCOPING can transition to DISPATCHING when stories queued, or ERROR on unrecoverable error
	StateScoping: {StateDispatching, StateError},

	// DISPATCHING can transition to MONITORING when stories placed on work-queue, or DONE when no stories left
	StateDispatching: {StateMonitoring, StateDone},

	// MONITORING can transition to REQUEST for any coder request, or MERGING when approved code-review arrives
	StateMonitoring: {StateRequest, StateMerging},

	// REQUEST can transition to MONITORING (approve non-code/request changes), MERGING (approve code-review),
	// ESCALATED (cannot answer), or ERROR (abandon/unrecoverable)
	StateRequest: {StateMonitoring, StateMerging, StateEscalated, StateError},

	// ESCALATED can transition to REQUEST when human answer supplied, or ERROR on timeout/no answer
	StateEscalated: {StateRequest, StateError},

	// MERGING can transition to DISPATCHING when merge succeeds (may unblock more stories), or ERROR on failure
	StateMerging: {StateDispatching, StateError},

	// DONE can transition to WAITING when new spec arrives
	StateDone: {StateWaiting},

	// ERROR can transition to WAITING on recovery/restart
	StateError: {StateWaiting},
}

// ValidNextStates returns the allowed next states for a given state
// This is the preferred way to access transition information
func ValidNextStates(from proto.State) []proto.State {
	return architectTransitions[from]
}

// IsValidArchitectTransition checks if a transition between two states is allowed
// according to the canonical state machine specification from STATES.md
func IsValidArchitectTransition(from, to proto.State) bool {
	allowedStates := ValidNextStates(from)
	for _, state := range allowedStates {
		if state == to {
			return true
		}
	}
	return false
}

// EscalationTimeout defines the maximum time an architect can remain in ESCALATED state
// before automatically transitioning to ERROR state
const EscalationTimeout = 2 * time.Hour

// HeartbeatInterval defines the interval for heartbeat debug logging in idle states
const HeartbeatInterval = 30 * time.Second

// DispatcherSendTimeout defines the timeout for dispatcher send operations
const DispatcherSendTimeout = 500 * time.Millisecond

// GetAllArchitectStates returns all valid architect states in deterministic order
func GetAllArchitectStates() []proto.State {
	return []proto.State{
		StateWaiting,
		StateScoping,
		StateDispatching,
		StateMonitoring,
		StateRequest,
		StateEscalated,
		StateMerging,
		StateDone,
		StateError,
	}
}

// IsTerminalState returns true if the state is a terminal state (DONE or ERROR)
func IsTerminalState(state proto.State) bool {
	return state == StateDone || state == StateError
}

// IsValidArchitectState checks if a given state is a valid architect state
func IsValidArchitectState(state proto.State) bool {
	allStates := GetAllArchitectStates()
	for _, validState := range allStates {
		if validState == state {
			return true
		}
	}
	return false
}
