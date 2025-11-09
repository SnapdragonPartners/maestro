// Package pm implements the PM (Product Manager) agent for interactive specification development.
package pm

import (
	"orchestrator/pkg/proto"
)

// PM-specific states.
const (
	// StateWaiting - PM is idle, waiting for interview request from WebUI.
	StateWaiting proto.State = "WAITING"
	// StateWorking - PM is actively working with user (interviewing, drafting, submitting).
	// PM has access to all tools: chat_post, read_file, list_files, submit_spec.
	// When submit_spec succeeds, PM transitions back to WAITING.
	StateWorking proto.State = "WORKING"
)

// validTransitions defines the PM state machine transition rules.
//
//nolint:gochecknoglobals // Intentional package-level constant for state machine definition
var validTransitions = map[proto.State][]proto.State{
	StateWaiting: {
		StateWorking, // User starts interview or uploads spec
		proto.StateDone,
	},
	StateWorking: {
		StateWaiting, // submit_spec succeeds or architect approves
		proto.StateError,
		proto.StateDone,
	},
	proto.StateError: {
		StateWaiting, // Reset to waiting after error
		proto.StateDone,
	},
	proto.StateDone: {
		// Terminal state - no outgoing transitions
		// Supervisor handles restarting agents via restart policy
	},
}

// IsValidPMTransition checks if a state transition is valid for PM agent.
func IsValidPMTransition(from, to proto.State) bool {
	allowedStates, exists := validTransitions[from]
	if !exists {
		return false
	}

	for _, allowed := range allowedStates {
		if allowed == to {
			return true
		}
	}

	return false
}

// GetAllPMStates returns all PM-specific states.
func GetAllPMStates() []proto.State {
	return []proto.State{
		StateWaiting,
		StateWorking,
		proto.StateDone,
		proto.StateError,
	}
}

// IsValidPMState checks if a state is a valid PM state.
func IsValidPMState(state proto.State) bool {
	for _, s := range GetAllPMStates() {
		if s == state {
			return true
		}
	}
	return false
}

// ValidNextStates returns the valid next states for a given PM state.
func ValidNextStates(from proto.State) []proto.State {
	return validTransitions[from]
}

// IsTerminalState checks if a state is terminal (DONE or ERROR).
func IsTerminalState(state proto.State) bool {
	return state == proto.StateDone || state == proto.StateError
}
