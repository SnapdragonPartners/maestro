// Package pm implements the PM (Product Manager) agent for interactive specification development.
package pm

import (
	"orchestrator/pkg/proto"
)

// PM-specific states.
const (
	// StateWaiting - PM is idle, waiting for interview request from WebUI.
	StateWaiting proto.State = "WAITING"
	// StateWorking - PM is actively working (conducting interview, making LLM calls with tools).
	// PM has access to all tools: chat_post, read_file, list_files, await_user, spec_submit.
	StateWorking proto.State = "WORKING"
	// StateAwaitUser - PM is blocked waiting for user's response in chat.
	// Transitions back to WORKING when new chat messages arrive.
	StateAwaitUser proto.State = "AWAIT_USER"
	// StatePreview - PM has generated spec, user is reviewing in WebUI before submission to architect.
	// User can choose to continue interview or submit to architect.
	StatePreview proto.State = "PREVIEW"
	// StateAwaitArchitect - PM is blocked waiting for architect's RESULT message (feedback or approval).
	// Blocks on response channel (no polling).
	StateAwaitArchitect proto.State = "AWAIT_ARCHITECT"
)

// validTransitions defines the PM state machine transition rules.
//
//nolint:gochecknoglobals // Intentional package-level constant for state machine definition
var validTransitions = map[proto.State][]proto.State{
	StateWaiting: {
		StateWaiting,   // Stay in waiting while polling for state changes
		StateWorking,   // User starts interview - PM begins working
		StateAwaitUser, // User starts interview - wait for first message
		StatePreview,   // User uploads spec file - go directly to preview
		proto.StateDone,
	},
	StateWorking: {
		StateWorking,   // Stay in working while iterating through tool calls
		StateAwaitUser, // await_user tool called - wait for user response
		StatePreview,   // spec_submit tool called - transition to user review
		proto.StateError,
		proto.StateDone,
	},
	StateAwaitUser: {
		StateAwaitUser, // Stay in await state while polling for messages
		StateWorking,   // New messages arrived - resume work
		proto.StateError,
		proto.StateDone,
	},
	StatePreview: {
		StatePreview,        // Stay in preview while waiting for valid action
		StateAwaitUser,      // User clicks "Continue Interview"
		StateAwaitArchitect, // User clicks "Submit for Development"
		proto.StateError,
		proto.StateDone,
	},
	StateAwaitArchitect: {
		StateAwaitArchitect, // Stay in await state while blocking on response channel
		StateWorking,        // Architect provides feedback OR approval (stay engaged for tweaks)
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
		StateAwaitUser,
		StatePreview,
		StateAwaitArchitect,
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
