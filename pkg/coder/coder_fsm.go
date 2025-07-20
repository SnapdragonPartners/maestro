package coder

import (
	"fmt"

	"orchestrator/pkg/proto"
)

// State constants - single source of truth for state names
// We inherit three states, WAITING (the entry state), DONE and ERROR from the base agent.
// DONE is terminal (agent shutdown), ERROR transitions to DONE for orchestrator cleanup.
const (
	StateSetup        proto.State = "SETUP"
	StatePlanning     proto.State = "PLANNING"
	StateCoding       proto.State = "CODING"
	StateTesting      proto.State = "TESTING"
	StatePlanReview   proto.State = "PLAN_REVIEW"
	StateCodeReview   proto.State = "CODE_REVIEW"
	StateBudgetReview proto.State = "BUDGET_REVIEW"
	StateAwaitMerge   proto.State = "AWAIT_MERGE"
	StateQuestion     proto.State = "QUESTION"
)

// Import AUTO_CHECKIN types from proto package for inter-agent communication
type AutoAction = proto.AutoAction

const (
	AutoContinue               = proto.AutoContinue
	AutoPivot                  = proto.AutoPivot
	AutoEscalate               = proto.AutoEscalate
	AutoAbandon                = proto.AutoAbandon
	QuestionReasonBudgetReview = proto.QuestionReasonBudgetReview
)

// ValidateState checks if a state is valid for coder agents
func ValidateState(state proto.State) error {
	validStates := GetValidStates()
	for _, validState := range validStates {
		if state == validState {
			return nil
		}
	}
	return fmt.Errorf("invalid coder state: %s", state)
}

// GetValidStates returns all valid states for coder agents
func GetValidStates() []proto.State {
	return []proto.State{
		proto.StateWaiting, StateSetup, StatePlanning, StateCoding, StateTesting,
		StatePlanReview, StateCodeReview, StateBudgetReview, StateAwaitMerge, StateQuestion, proto.StateDone, proto.StateError,
	}
}

// CoderTransitions defines the canonical state transition map for coder agents.
// This is the single source of truth, derived directly from STATES.md and worktree MVP stories.
// Any code, tests, or diagrams must match this specification exactly.
var CoderTransitions = map[proto.State][]proto.State{
	// WAITING can transition to SETUP when receiving task assignment
	proto.StateWaiting: {StateSetup},

	// SETUP prepares workspace (mirror clone, worktree, branch) then goes to PLANNING
	StateSetup: {StatePlanning, proto.StateError},

	// PLANNING can submit plan for review, ask questions, or exceed budget (→BUDGET_REVIEW)
	StatePlanning: {StatePlanReview, StateQuestion, StateBudgetReview},

	// PLAN_REVIEW can approve plan (→CODING), approve completion (→DONE), request changes (→PLANNING), or abandon (→ERROR)
	StatePlanReview: {StatePlanning, StateCoding, proto.StateDone, proto.StateError},

	// CODING can complete (→TESTING), ask questions, exceed budget (→BUDGET_REVIEW), or hit unrecoverable error
	StateCoding: {StateTesting, StateQuestion, StateBudgetReview, proto.StateError},

	// TESTING can pass (→CODE_REVIEW) or fail (→CODING)
	StateTesting: {StateCoding, StateCodeReview},

	// CODE_REVIEW can approve (→AWAIT_MERGE), request changes (→CODING), or abandon (→ERROR)
	StateCodeReview: {StateAwaitMerge, StateCoding, proto.StateError},

	// BUDGET_REVIEW can continue (→CODING), pivot (→PLANNING), or abandon (→ERROR)
	StateBudgetReview: {StatePlanning, StateCoding, proto.StateError},

	// AWAIT_MERGE can complete successfully (→DONE) or encounter merge conflicts (→CODING)
	StateAwaitMerge: {proto.StateDone, StateCoding},

	// QUESTION can return to origin state or escalate to error based on answer type
	StateQuestion: {StatePlanning, StateCoding, proto.StateError},

	// ERROR transitions to DONE for orchestrator cleanup and restart
	// DONE is terminal (no transitions)
	proto.StateError: {proto.StateDone},
}

// IsValidCoderTransition checks if a transition between two states is allowed
// according to the canonical state machine specification.
func IsValidCoderTransition(from, to proto.State) bool {
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
func GetAllCoderStates() []proto.State {
	stateSet := make(map[proto.State]bool)

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
	states := make([]proto.State, 0, len(stateSet))
	for state := range stateSet {
		// Exclude base agent states to match legacy GetAllCoderStates behavior
		if state != proto.StateWaiting && state != proto.StateDone && state != proto.StateError {
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
func IsCoderState(state proto.State) bool {
	// Base agent states are not considered "coder states" for backward compatibility
	if state == proto.StateWaiting || state == proto.StateDone || state == proto.StateError {
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
