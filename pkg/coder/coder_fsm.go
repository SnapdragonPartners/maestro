package coder

import (
	"fmt"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/proto"
)

// State constants - single source of truth for state names
// We inherit three states, WAITING (the entry state), DONE and ERROR
// (the exit states) from the base agent.
const (
	StatePlanning   agent.State = "PLANNING"
	StateCoding     agent.State = "CODING"
	StateTesting    agent.State = "TESTING"
	StateFixing     agent.State = "FIXING"
	StatePlanReview agent.State = "PLAN_REVIEW"
	StateCodeReview agent.State = "CODE_REVIEW"
	StateQuestion   agent.State = "QUESTION"
)

// Import AUTO_CHECKIN types from proto package for inter-agent communication
type AutoAction = proto.AutoAction

const (
	AutoContinue              = proto.AutoContinue
	AutoPivot                 = proto.AutoPivot
	AutoEscalate              = proto.AutoEscalate
	AutoAbandon               = proto.AutoAbandon
	QuestionReasonAutoCheckin = proto.QuestionReasonAutoCheckin
)

// ValidateState checks if a state is valid for coder agents
func ValidateState(state agent.State) error {
	validStates := GetValidStates()
	for _, validState := range validStates {
		if state == validState {
			return nil
		}
	}
	return fmt.Errorf("invalid coder state: %s", state)
}

// GetValidStates returns all valid states for coder agents
func GetValidStates() []agent.State {
	return []agent.State{
		agent.StateWaiting, StatePlanning, StateCoding, StateTesting, StateFixing,
		StatePlanReview, StateCodeReview, StateQuestion, agent.StateDone, agent.StateError,
	}
}

// CoderTransitions defines the canonical state transition map for coder agents.
// This is the single source of truth, derived directly from STATES.md.
// Any code, tests, or diagrams must match this specification exactly.
var CoderTransitions = map[agent.State][]agent.State{
	// WAITING can only transition to PLANNING when receiving task
	agent.StateWaiting: {StatePlanning},

	// PLANNING can submit plan for review or ask questions
	StatePlanning: {StatePlanReview, StateQuestion},

	// PLAN_REVIEW can approve (→CODING), request changes (→PLANNING), or abandon (→ERROR)
	StatePlanReview: {StatePlanning, StateCoding, agent.StateError},

	// CODING can complete (→TESTING), ask questions, or hit unrecoverable error
	StateCoding: {StateTesting, StateQuestion, agent.StateError},

	// TESTING can pass (→CODE_REVIEW) or fail (→FIXING)
	StateTesting: {StateFixing, StateCodeReview},

	// FIXING can fix and retest, ask questions, or hit unrecoverable error
	StateFixing: {StateTesting, StateQuestion, agent.StateError},

	// CODE_REVIEW can approve (→DONE), request changes (→FIXING), or abandon (→ERROR)
	StateCodeReview: {agent.StateDone, StateFixing, agent.StateError},

	// QUESTION can return to any non-terminal state based on answer type
	StateQuestion: {
		StatePlanReview, StatePlanning, StateCoding,
		StateFixing, StateCodeReview, agent.StateError,
	},

	// Terminal states have no outgoing transitions
	agent.StateDone:  {},
	agent.StateError: {},
}

// IsValidCoderTransition checks if a transition between two states is allowed
// according to the canonical state machine specification.
func IsValidCoderTransition(from, to agent.State) bool {
	allowedStates, exists := CoderTransitions[from]
	if !exists {
		return false
	}

	for _, state := range allowedStates {
		if state == to {
			return true
		}
	}

	return false
}

// GetAllCoderStates returns all valid coder states derived from the transition map
// Returns states in deterministic alphabetical order
func GetAllCoderStates() []agent.State {
	stateSet := make(map[agent.State]bool)

	// Collect all states that appear as keys (source states)
	for fromState := range CoderTransitions {
		stateSet[fromState] = true
	}

	// Collect all states that appear as values (target states)
	for _, transitions := range CoderTransitions {
		for _, toState := range transitions {
			stateSet[toState] = true
		}
	}

	// Convert set to slice, filtering out base agent states to match legacy behavior
	states := make([]agent.State, 0, len(stateSet))
	for state := range stateSet {
		// Exclude base agent states to match legacy GetAllCoderStates behavior
		if state != agent.StateWaiting && state != agent.StateDone && state != agent.StateError {
			states = append(states, state)
		}
	}

	// Sort states alphabetically for consistency
	for i := 0; i < len(states)-1; i++ {
		for j := i + 1; j < len(states); j++ {
			if string(states[i]) > string(states[j]) {
				states[i], states[j] = states[j], states[i]
			}
		}
	}

	return states
}

// IsCoderState checks if a given state is a valid coder-specific state
// Excludes base agent states (WAITING, DONE, ERROR) to match legacy behavior
func IsCoderState(state agent.State) bool {
	// Base agent states are not considered "coder states" for backward compatibility
	if state == agent.StateWaiting || state == agent.StateDone || state == agent.StateError {
		return false
	}

	// Check if state exists in CoderTransitions (as key or value)
	if _, exists := CoderTransitions[state]; exists {
		return true
	}

	// Check if state appears as a target state
	for _, transitions := range CoderTransitions {
		for _, toState := range transitions {
			if toState == state {
				return true
			}
		}
	}

	return false
}

// ParseAutoAction delegates to proto package
var ParseAutoAction = proto.ParseAutoAction
