package coder

import (
	"testing"

	"orchestrator/pkg/proto"
)

func TestValidateState_ValidStates(t *testing.T) {
	validStates := GetValidStates()
	for _, state := range validStates {
		if err := ValidateState(state); err != nil {
			t.Errorf("ValidateState(%s) returned error: %v", state, err)
		}
	}
}

func TestValidateState_InvalidState(t *testing.T) {
	err := ValidateState(proto.State("INVALID_STATE"))
	if err == nil {
		t.Error("Expected error for invalid state")
	}
}

func TestGetValidStates(t *testing.T) {
	states := GetValidStates()

	// Check minimum expected states
	expectedStates := []proto.State{
		proto.StateWaiting,
		StateSetup,
		StatePlanning,
		StateCoding,
		StateTesting,
		StatePlanReview,
		StateCodeReview,
		StatePrepareMerge,
		StateBudgetReview,
		StateAwaitMerge,
		StateQuestion,
		proto.StateSuspend,
		proto.StateDone,
		proto.StateError,
	}

	if len(states) != len(expectedStates) {
		t.Errorf("Expected %d states, got %d", len(expectedStates), len(states))
	}

	// Verify all expected states are present
	stateSet := make(map[proto.State]bool)
	for _, s := range states {
		stateSet[s] = true
	}

	for _, expected := range expectedStates {
		if !stateSet[expected] {
			t.Errorf("Missing expected state: %s", expected)
		}
	}
}

func TestIsValidCoderTransition_AllowedTransitions(t *testing.T) {
	testCases := []struct {
		from proto.State
		to   proto.State
	}{
		// WAITING transitions
		{proto.StateWaiting, StateSetup},
		{proto.StateWaiting, proto.StateError},
		{proto.StateWaiting, proto.StateDone},

		// SETUP transitions
		{StateSetup, StatePlanning},
		{StateSetup, StateCoding}, // Express path
		{StateSetup, proto.StateError},

		// PLANNING transitions
		{StatePlanning, StatePlanReview},
		{StatePlanning, StateBudgetReview},
		{StatePlanning, StateQuestion},

		// PLAN_REVIEW transitions
		{StatePlanReview, StatePlanning},
		{StatePlanReview, StateCoding},
		{StatePlanReview, proto.StateDone},
		{StatePlanReview, proto.StateError},

		// CODING transitions
		{StateCoding, StateTesting},
		{StateCoding, StateBudgetReview},
		{StateCoding, StateQuestion},
		{StateCoding, proto.StateError},

		// TESTING transitions
		{StateTesting, StateCoding},
		{StateTesting, StateCodeReview},

		// CODE_REVIEW transitions
		{StateCodeReview, StatePrepareMerge},
		{StateCodeReview, proto.StateDone},
		{StateCodeReview, StateCoding},
		{StateCodeReview, proto.StateError},

		// BUDGET_REVIEW transitions
		{StateBudgetReview, StatePlanning},
		{StateBudgetReview, StateCoding},
		{StateBudgetReview, proto.StateError},

		// PREPARE_MERGE transitions
		{StatePrepareMerge, StateAwaitMerge},
		{StatePrepareMerge, StateCoding},
		{StatePrepareMerge, proto.StateError},

		// AWAIT_MERGE transitions
		{StateAwaitMerge, proto.StateDone},
		{StateAwaitMerge, StateCoding},
		{StateAwaitMerge, proto.StateError},

		// QUESTION transitions
		{StateQuestion, StatePlanning},
		{StateQuestion, StateCoding},
		{StateQuestion, proto.StateError},
	}

	for _, tc := range testCases {
		t.Run(string(tc.from)+"->"+string(tc.to), func(t *testing.T) {
			if !IsValidCoderTransition(tc.from, tc.to) {
				t.Errorf("Expected transition %s -> %s to be valid", tc.from, tc.to)
			}
		})
	}
}

func TestIsValidCoderTransition_DisallowedTransitions(t *testing.T) {
	testCases := []struct {
		from proto.State
		to   proto.State
	}{
		// WAITING cannot go directly to CODING
		{proto.StateWaiting, StateCoding},
		{proto.StateWaiting, StatePlanning},

		// SETUP cannot skip to CODE_REVIEW
		{StateSetup, StateCodeReview},
		{StateSetup, StateAwaitMerge},

		// PLANNING cannot skip to TESTING
		{StatePlanning, StateTesting},
		{StatePlanning, StateCodeReview},

		// TESTING cannot skip to AWAIT_MERGE
		{StateTesting, StateAwaitMerge},
		{StateTesting, proto.StateDone},

		// Terminal states have no transitions
		{proto.StateDone, StateSetup},
		{proto.StateError, StatePlanning},

		// Invalid source state
		{proto.State("INVALID"), StateCoding},
	}

	for _, tc := range testCases {
		t.Run(string(tc.from)+"->"+string(tc.to), func(t *testing.T) {
			if IsValidCoderTransition(tc.from, tc.to) {
				t.Errorf("Expected transition %s -> %s to be invalid", tc.from, tc.to)
			}
		})
	}
}

func TestGetAllCoderStates_ExcludesBaseStates(t *testing.T) {
	states := GetAllCoderStates()

	// Should not include base agent states
	for _, state := range states {
		if state == proto.StateWaiting || state == proto.StateDone || state == proto.StateError {
			t.Errorf("GetAllCoderStates should not include base state: %s", state)
		}
	}

	// Should include coder-specific states
	expectedStates := []proto.State{
		StateSetup,
		StatePlanning,
		StateCoding,
		StateTesting,
		StatePlanReview,
		StateCodeReview,
		StatePrepareMerge,
		StateBudgetReview,
		StateAwaitMerge,
		StateQuestion,
	}

	stateSet := make(map[proto.State]bool)
	for _, s := range states {
		stateSet[s] = true
	}

	for _, expected := range expectedStates {
		if !stateSet[expected] {
			t.Errorf("Missing coder state: %s", expected)
		}
	}
}

func TestGetAllCoderStates_Sorted(t *testing.T) {
	states := GetAllCoderStates()

	// Verify alphabetical order
	for i := 0; i < len(states)-1; i++ {
		if string(states[i]) > string(states[i+1]) {
			t.Errorf("States not sorted: %s should come before %s", states[i+1], states[i])
		}
	}
}

func TestIsCoderState_AllStates(t *testing.T) {
	// Coder-specific states should return true
	coderStates := []proto.State{
		StateSetup,
		StatePlanning,
		StateCoding,
		StateTesting,
		StatePlanReview,
		StateCodeReview,
		StatePrepareMerge,
		StateBudgetReview,
		StateAwaitMerge,
		StateQuestion,
	}

	for _, state := range coderStates {
		if !IsCoderState(state) {
			t.Errorf("IsCoderState(%s) should return true", state)
		}
	}

	// Base agent states should return false
	baseStates := []proto.State{
		proto.StateWaiting,
		proto.StateDone,
		proto.StateError,
	}

	for _, state := range baseStates {
		if IsCoderState(state) {
			t.Errorf("IsCoderState(%s) should return false for base state", state)
		}
	}

	// Invalid states should return false
	if IsCoderState(proto.State("INVALID")) {
		t.Error("IsCoderState should return false for invalid state")
	}
}

func TestCoderTransitions_NoDeadEndStates(t *testing.T) {
	// Every state in CoderTransitions should have at least one transition
	for state, transitions := range CoderTransitions {
		if len(transitions) == 0 {
			t.Errorf("State %s has no transitions (dead end)", state)
		}
	}
}

func TestCoderTransitions_ReachableFromWaiting(t *testing.T) {
	// All coder states should be reachable from WAITING
	// via some path through the state machine
	reachable := make(map[proto.State]bool)
	var visit func(state proto.State)
	visit = func(state proto.State) {
		if reachable[state] {
			return
		}
		reachable[state] = true
		for _, next := range CoderTransitions[state] {
			visit(next)
		}
	}

	visit(proto.StateWaiting)

	// All states mentioned in CoderTransitions should be reachable
	for state := range CoderTransitions {
		if !reachable[state] {
			t.Errorf("State %s is not reachable from WAITING", state)
		}
	}

	// All target states should be reachable
	for _, transitions := range CoderTransitions {
		for _, target := range transitions {
			if !reachable[target] {
				t.Errorf("Target state %s is not reachable from WAITING", target)
			}
		}
	}
}

func TestCoderTransitions_TerminalStatesReachable(t *testing.T) {
	// DONE and ERROR should be reachable from WAITING
	reachable := make(map[proto.State]bool)
	var visit func(state proto.State)
	visit = func(state proto.State) {
		if reachable[state] {
			return
		}
		reachable[state] = true
		for _, next := range CoderTransitions[state] {
			visit(next)
		}
	}

	visit(proto.StateWaiting)

	if !reachable[proto.StateDone] {
		t.Error("DONE state is not reachable from WAITING")
	}
	if !reachable[proto.StateError] {
		t.Error("ERROR state is not reachable from WAITING")
	}
}

func TestStateConstants_NonEmpty(t *testing.T) {
	states := []proto.State{
		StateSetup,
		StatePlanning,
		StateCoding,
		StateTesting,
		StatePlanReview,
		StateCodeReview,
		StatePrepareMerge,
		StateBudgetReview,
		StateAwaitMerge,
		StateQuestion,
	}

	for _, state := range states {
		if len(state) == 0 {
			t.Error("State constant should not be empty")
		}
	}
}

func TestStateConstants_Unique(t *testing.T) {
	states := []proto.State{
		StateSetup,
		StatePlanning,
		StateCoding,
		StateTesting,
		StatePlanReview,
		StateCodeReview,
		StatePrepareMerge,
		StateBudgetReview,
		StateAwaitMerge,
		StateQuestion,
	}

	seen := make(map[proto.State]bool)
	for _, state := range states {
		if seen[state] {
			t.Errorf("Duplicate state constant: %s", state)
		}
		seen[state] = true
	}
}

func TestAutoActionConstants_MatchProto(t *testing.T) {
	// Verify auto action constants match proto package
	if AutoContinue != proto.AutoContinue {
		t.Error("AutoContinue should match proto.AutoContinue")
	}
	if AutoPivot != proto.AutoPivot {
		t.Error("AutoPivot should match proto.AutoPivot")
	}
	if AutoEscalate != proto.AutoEscalate {
		t.Error("AutoEscalate should match proto.AutoEscalate")
	}
	if AutoAbandon != proto.AutoAbandon {
		t.Error("AutoAbandon should match proto.AutoAbandon")
	}
}

func TestStateDataKeyConstants_NonEmpty(t *testing.T) {
	keys := []string{
		KeyOrigin,
		KeyErrorMessage,
		KeyStoryMessageID,
		KeyStoryID,
		KeyExpress,
		KeyIsHotfix,
		KeyQuestionSubmitted,
		KeyPlanSubmitted,
		KeyStoryCompletedAt,
		KeyCompletionStatus,
		KeyPlanReviewCompletedAt,
		KeyPlan,
		KeyNoToolCallsCount,
		KeyCodeGenerated,
		KeyFilesCreated,
		KeyCodingCompletedAt,
		KeyWorkspacePath,
		KeyBuildBackend,
		KeyTestError,
		KeyTestsPassed,
		KeyTestOutput,
		KeyTestingCompletedAt,
		KeyCodeReviewCompletedAt,
		KeyMergeResult,
		KeyMergeCompletedAt,
		KeyBudgetReviewCompletedAt,
		KeyLocalBranchName,
		KeyRemoteBranchName,
		KeyPlanningCompletedAt,
		KeyCompletionSubmittedAt,
		KeyTreeOutputCached,
		KeyPendingQuestion,
		KeyPlanningContextSaved,
		KeyCodingContextSaved,
		KeyDoneLogged,
		KeyPrepareMergeCompletedAt,
		KeyPRURL,
		KeyPRCreated,
		KeyPRSkipped,
		KeyTaskContent,
		KeyPlanApprovalResult,
		KeyCodeApprovalResult,
		KeyQuestionAnswered,
		KeyLastQA,
		KeyCodingSessionID,
		KeyResumeInput,
		KeyPlanConfidence,
		KeyExplorationSummary,
		KeyPlanRisks,
		KeyCompletionSignaled,
		KeyCompletionDetails,
		KeyEmptyResponse,
		KeyTodoList,
		KeyBudgetReviewEffect,
	}

	seen := make(map[string]bool)
	for _, key := range keys {
		if key == "" {
			t.Error("State data key should not be empty")
		}
		if seen[key] {
			t.Errorf("Duplicate state data key: %s", key)
		}
		seen[key] = true
	}
}

func TestParseAutoAction_Valid(t *testing.T) {
	// Verify ParseAutoAction is properly aliased
	if ParseAutoAction == nil {
		t.Error("ParseAutoAction should not be nil")
	}

	// Test parsing (delegates to proto package)
	action, err := ParseAutoAction("CONTINUE")
	if err != nil {
		t.Errorf("Expected CONTINUE to be valid auto action, got error: %v", err)
	}
	if action != AutoContinue {
		t.Errorf("Expected AutoContinue, got %v", action)
	}

	// Test invalid action
	_, err = ParseAutoAction("INVALID")
	if err == nil {
		t.Error("Expected error for invalid auto action")
	}
}
