package coder

import (
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

// State constants - single source of truth for state names.
// We inherit three states, WAITING (the entry state), DONE and ERROR from the base agent.
// DONE is terminal (agent shutdown), ERROR transitions to DONE for orchestrator cleanup.
const (
	StateSetup        proto.State = "SETUP"
	StatePlanning     proto.State = "PLANNING"
	StateCoding       proto.State = "CODING"
	StateTesting      proto.State = "TESTING"
	StatePlanReview   proto.State = "PLAN_REVIEW"
	StateCodeReview   proto.State = "CODE_REVIEW"
	StatePrepareMerge proto.State = "PREPARE_MERGE"
	StateBudgetReview proto.State = "BUDGET_REVIEW"
	StateAwaitMerge   proto.State = "AWAIT_MERGE"
)

// AutoAction imports AUTO_CHECKIN types from proto package for inter-agent communication.
type AutoAction = proto.AutoAction

const (
	// AutoContinue indicates to continue with the current approach.
	AutoContinue = proto.AutoContinue
	// AutoPivot indicates to change approach or strategy.
	AutoPivot = proto.AutoPivot
	// AutoEscalate indicates to escalate to higher authority.
	AutoEscalate = proto.AutoEscalate
	// AutoAbandon indicates to abandon the current task.
	AutoAbandon = proto.AutoAbandon
	// QuestionReasonBudgetReview indicates a budget review question.
	QuestionReasonBudgetReview = proto.QuestionReasonBudgetReview
)

// State data keys - single source of truth for SetStateData/GetStateValue calls.
const (
	KeyOrigin                      = "origin"
	KeyErrorMessage                = "error_message"
	KeyStoryMessageID              = "story_message_id"
	KeyStoryID                     = "story_id"
	KeyQuestionSubmitted           = "question_submitted"
	KeyPlanSubmitted               = "plan_submitted"
	KeyStoryCompletedAt            = "story_completed_at"
	KeyCompletionStatus            = "completion_status"
	KeyPlanReviewCompletedAt       = "plan_review_completed_at"
	KeyMergeConflictDetails        = "merge_conflict_details"
	KeyCodeReviewRejectionFeedback = "code_review_rejection_feedback"
	KeyTestFailureOutput           = "test_failure_output"
	KeyPlan                        = "plan"
	KeyCodingMode                  = "coding_mode"
	KeyNoToolCallsCount            = "no_tool_calls_count"
	KeyCodeGenerated               = "code_generated"
	KeyFilesCreated                = "files_created"
	KeyCodingCompletedAt           = "coding_completed_at"
	KeyWorkspacePath               = "workspace_path"
	KeyBuildBackend                = "build_backend"
	KeyTestError                   = "test_error"
	KeyTestsPassed                 = "tests_passed"
	KeyTestOutput                  = "test_output"
	KeyTestingCompletedAt          = "testing_completed_at"
	KeyCodeReviewCompletedAt       = "code_review_completed_at"
	KeyMergeResult                 = "merge_result"
	KeyMergeCompletedAt            = "merge_completed_at"
	KeyBudgetReviewCompletedAt     = "budget_review_completed_at"
	KeyArchitectResponse           = "architect_response"
	KeyLocalBranchName             = "local_branch_name"
	KeyRemoteBranchName            = "remote_branch_name"
	KeyQuestionContext             = "question_context"
	KeyPlanningCompletedAt         = "planning_completed_at"
	KeyCompletionReason            = "completion_reason"
	KeyCompletionEvidence          = "completion_evidence"
	KeyCompletionConfidence        = "completion_confidence"
	KeyCompletionSubmittedAt       = "completion_submitted_at"
	KeyTreeOutputCached            = "tree_output_cached"
	KeyPlanningContextSaved        = "planning_context_saved"
	KeyCodingContextSaved          = "coding_context_saved"
	KeyDoneLogged                  = "done_logged"
	KeyFixesApplied                = "fixes_applied"
	KeyPrepareMergeCompletedAt     = "prepare_merge_completed_at"
	KeyPRCreationError             = "pr_creation_error"
	KeyPRURL                       = "pr_url"
	KeyPRCreated                   = "pr_created"
	KeyPRSkipped                   = "pr_skipped"
	KeyTaskContent                 = "task_content"
	KeyPlanApprovalResult          = "plan_approval_result"
	KeyCodeApprovalResult          = "code_approval_result"
	KeyExplorationFindings         = "exploration_findings"
	KeyQuestionAnswered            = "question_answered"
	KeyPlanConfidence              = "plan_confidence"
	KeyExplorationSummary          = "exploration_summary"
	KeyPlanRisks                   = "plan_risks"
	KeyCompletionSignaled          = "completion_signaled"
	KeyConsecutiveEmptyResponses   = "consecutive_empty_responses"
	KeyCompletionDetails           = "completion_details"
)

// ValidateState checks if a state is valid for coder agents.
func ValidateState(state proto.State) error {
	validStates := GetValidStates()
	for _, validState := range validStates {
		if state == validState {
			return nil
		}
	}
	return logx.Errorf("invalid coder state: %s", state)
}

// GetValidStates returns all valid states for coder agents.
func GetValidStates() []proto.State {
	return []proto.State{
		proto.StateWaiting, StateSetup, StatePlanning, StateCoding, StateTesting,
		StatePlanReview, StateCodeReview, StatePrepareMerge, StateBudgetReview, StateAwaitMerge, proto.StateDone, proto.StateError,
	}
}

// CoderTransitions defines the canonical state transition map for coder agents.
// This is the single source of truth, derived directly from STATES.md and clone-based workspace stories.
// Any code, tests, or diagrams must match this specification exactly.
var CoderTransitions = map[proto.State][]proto.State{ //nolint:gochecknoglobals
	// WAITING can transition to SETUP when receiving task assignment, ERROR during shutdown, or DONE for clean shutdown.
	proto.StateWaiting: {StateSetup, proto.StateError, proto.StateDone},

	// SETUP prepares workspace (mirror, clone, branch) then goes to PLANNING.
	StateSetup: {StatePlanning, proto.StateError},

	// PLANNING can submit plan for review or exceed budget (→BUDGET_REVIEW). Questions are handled inline via Effects.
	StatePlanning: {StatePlanReview, StateBudgetReview},

	// PLAN_REVIEW can approve plan (→CODING), approve completion (→DONE), request changes (→PLANNING), or abandon (→ERROR).
	StatePlanReview: {StatePlanning, StateCoding, proto.StateDone, proto.StateError},

	// CODING can complete (→TESTING), exceed budget (→BUDGET_REVIEW), or hit unrecoverable error. Questions are handled inline via Effects.
	StateCoding: {StateTesting, StateBudgetReview, proto.StateError},

	// TESTING can pass (→CODE_REVIEW) or fail (→CODING).
	StateTesting: {StateCoding, StateCodeReview},

	// CODE_REVIEW can approve (→PREPARE_MERGE), request changes (→CODING), or abandon (→ERROR).
	StateCodeReview: {StatePrepareMerge, StateCoding, proto.StateError},

	// BUDGET_REVIEW can continue (→CODING), pivot (→PLANNING), or abandon (→ERROR).
	StateBudgetReview: {StatePlanning, StateCoding, proto.StateError},

	// PREPARE_MERGE can commit and create PR (→AWAIT_MERGE), encounter recoverable git errors (→CODING), or hit unrecoverable errors (→ERROR).
	StatePrepareMerge: {StateAwaitMerge, StateCoding, proto.StateError},

	// AWAIT_MERGE can complete successfully (→DONE), encounter merge conflicts (→CODING), or have channel closure (→ERROR).
	StateAwaitMerge: {proto.StateDone, StateCoding, proto.StateError},

	// ERROR is terminal (no transitions) - agent requeues story before terminating.
	// DONE is terminal (no transitions).
}

// IsValidCoderTransition checks if a transition between two states is allowed.
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

// GetAllCoderStates returns all valid coder states derived from the transition map.
// Returns states in deterministic alphabetical order.
func GetAllCoderStates() []proto.State {
	stateSet := make(map[proto.State]bool)

	// Collect all states that appear as keys (source states).
	for fromState := range CoderTransitions {
		stateSet[fromState] = true
	}

	// Collect all states that appear as values (target states).
	for _, transitions := range CoderTransitions {
		for _, toState := range transitions {
			stateSet[toState] = true
		}
	}

	// Convert set to slice, filtering out base agent states.
	states := make([]proto.State, 0, len(stateSet))
	for state := range stateSet {
		// Exclude base agent states (WAITING, DONE, ERROR).
		if state != proto.StateWaiting && state != proto.StateDone && state != proto.StateError {
			states = append(states, state)
		}
	}

	// Sort states alphabetically for consistency.
	for i := 0; i < len(states)-1; i++ {
		for j := i + 1; j < len(states); j++ {
			if string(states[i]) > string(states[j]) {
				states[i], states[j] = states[j], states[i]
			}
		}
	}

	return states
}

// IsCoderState checks if a given state is a valid coder-specific state.
// Excludes base agent states (WAITING, DONE, ERROR) to match legacy behavior.
func IsCoderState(state proto.State) bool {
	// Base agent states are not considered "coder states" for backward compatibility.
	if state == proto.StateWaiting || state == proto.StateDone || state == proto.StateError {
		return false
	}

	// Check if state exists in CoderTransitions (as key or value).
	if _, exists := CoderTransitions[state]; exists {
		return true
	}

	// Check if state appears as a target state.
	for _, transitions := range CoderTransitions {
		for _, toState := range transitions {
			if toState == state {
				return true
			}
		}
	}

	return false
}

// ParseAutoAction delegates to proto package.
var ParseAutoAction = proto.ParseAutoAction //nolint:gochecknoglobals
