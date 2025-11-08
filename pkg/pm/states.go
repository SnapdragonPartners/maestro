// Package pm implements the PM (Product Manager) agent for interactive specification development.
package pm

import (
	"orchestrator/pkg/proto"
)

// PM-specific states.
const (
	// StateWaiting - PM is idle, waiting for interview request from WebUI.
	StateWaiting proto.State = "WAITING"
	// StateInterviewing - PM is conducting interview, gathering requirements.
	StateInterviewing proto.State = "INTERVIEWING"
	// StateDrafting - PM is generating markdown spec from conversation.
	StateDrafting proto.State = "DRAFTING"
	// StateSubmitting - PM is validating and submitting spec to architect.
	StateSubmitting proto.State = "SUBMITTING"
)

// IsValidPMTransition checks if a state transition is valid for PM agent.
func IsValidPMTransition(from, to proto.State) bool {
	validTransitions := map[proto.State][]proto.State{
		StateWaiting: {
			StateInterviewing,
			proto.StateDone,
		},
		StateInterviewing: {
			StateDrafting,
			proto.StateError,
			proto.StateDone,
		},
		StateDrafting: {
			StateSubmitting,
			StateInterviewing, // Back to interview if draft needs refinement
			proto.StateError,
			proto.StateDone,
		},
		StateSubmitting: {
			StateWaiting, // Return to waiting for next interview
			proto.StateError,
			proto.StateDone,
		},
		proto.StateError: {
			StateWaiting, // Reset to waiting after error
			proto.StateDone,
		},
	}

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
