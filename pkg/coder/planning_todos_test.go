package coder

import (
	"context"
	"testing"

	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// TestProcessPlanDataFromEffect_StoresTodosInState verifies that todos provided
// via submit_plan's ProcessEffect.Data are extracted and stored as a TodoList
// in both the coder's todoList field and the state machine.
func TestProcessPlanDataFromEffect_StoresTodosInState(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	effectData := map[string]any{
		"plan":       "Implementation plan text",
		"confidence": "HIGH",
		"todos":      []string{"Create module", "Add tests", "Update docs"},
	}

	err := coder.processPlanDataFromEffect(sm, effectData)
	if err != nil {
		t.Fatalf("processPlanDataFromEffect returned error: %v", err)
	}

	// Verify plan stored in state
	plan := utils.GetStateValueOr[string](sm, KeyPlan, "")
	if plan != "Implementation plan text" {
		t.Errorf("expected plan stored in state, got: %q", plan)
	}

	// Verify todos stored in coder field
	if coder.todoList == nil {
		t.Fatal("expected coder.todoList to be set, got nil")
	}
	if len(coder.todoList.Items) != 3 {
		t.Fatalf("expected 3 todo items, got %d", len(coder.todoList.Items))
	}
	if coder.todoList.Items[0].Description != "Create module" {
		t.Errorf("expected first todo 'Create module', got %q", coder.todoList.Items[0].Description)
	}
	if coder.todoList.Items[1].Description != "Add tests" {
		t.Errorf("expected second todo 'Add tests', got %q", coder.todoList.Items[1].Description)
	}
	if coder.todoList.Items[2].Description != "Update docs" {
		t.Errorf("expected third todo 'Update docs', got %q", coder.todoList.Items[2].Description)
	}
	if coder.todoList.Current != 0 {
		t.Errorf("expected Current=0, got %d", coder.todoList.Current)
	}

	// Verify all items start as not completed
	for i, item := range coder.todoList.Items {
		if item.Completed {
			t.Errorf("todo item %d should not be completed", i)
		}
	}

	// Verify todos stored in state machine
	stateTodoList := utils.GetStateValueOr[*TodoList](sm, KeyTodoList, nil)
	if stateTodoList == nil {
		t.Fatal("expected TodoList stored in state machine, got nil")
	}
	if len(stateTodoList.Items) != 3 {
		t.Errorf("expected 3 items in state TodoList, got %d", len(stateTodoList.Items))
	}
}

// TestProcessPlanDataFromEffect_NoTodos verifies that when submit_plan does not
// include todos (legacy path), the coder's todoList remains nil.
func TestProcessPlanDataFromEffect_NoTodos(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	effectData := map[string]any{
		"plan":       "Implementation plan text",
		"confidence": "HIGH",
	}

	err := coder.processPlanDataFromEffect(sm, effectData)
	if err != nil {
		t.Fatalf("processPlanDataFromEffect returned error: %v", err)
	}

	if coder.todoList != nil {
		t.Errorf("expected coder.todoList to be nil when no todos provided, got %+v", coder.todoList)
	}
}

// TestProcessPlanDataFromEffect_EmptyTodos verifies that an empty todos array
// does not create a TodoList.
func TestProcessPlanDataFromEffect_EmptyTodos(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	effectData := map[string]any{
		"plan":       "Implementation plan text",
		"confidence": "HIGH",
		"todos":      []string{},
	}

	err := coder.processPlanDataFromEffect(sm, effectData)
	if err != nil {
		t.Fatalf("processPlanDataFromEffect returned error: %v", err)
	}

	if coder.todoList != nil {
		t.Errorf("expected coder.todoList to be nil for empty todos, got %+v", coder.todoList)
	}
}

// TestHandlePlanReviewApproval_SkipsTodoCollectionWhenTodosExist verifies that
// when todos are already populated from submit_plan, handlePlanReviewApproval
// skips the separate requestTodoList() LLM call and transitions directly to CODING.
func TestHandlePlanReviewApproval_SkipsTodoCollectionWhenTodosExist(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	// Pre-populate todos as submit_plan would
	coder.todoList = &TodoList{
		Items: []TodoItem{
			{Description: "Create module", Completed: false},
			{Description: "Add tests", Completed: false},
		},
		Current: 0,
	}

	// Set required state data for the approval handler
	sm.SetStateData(KeyPlan, "Implementation plan")
	sm.SetStateData(string(stateDataKeyPlanConfidence), "HIGH")

	ctx := context.Background()
	nextState, completed, err := coder.handlePlanReviewApproval(ctx, sm, proto.ApprovalTypePlan)
	if err != nil {
		t.Fatalf("handlePlanReviewApproval returned error: %v", err)
	}

	if completed {
		t.Error("expected completed=false for plan approval")
	}

	// The key assertion: should transition to CODING, not ERROR.
	// If it tried to call requestTodoList() without an LLM client, it would error.
	// Since we pre-populated todos, it should skip that and go to CODING.
	if nextState != StateCoding {
		t.Errorf("expected next state %s, got %s", StateCoding, nextState)
	}

	// Verify todos are still intact
	if coder.todoList == nil || len(coder.todoList.Items) != 2 {
		t.Errorf("expected 2 todo items still present, got %v", coder.todoList)
	}
}

// TestHandlePlanReviewApproval_CompletionApproval verifies that completion
// approval still works correctly (not affected by todo changes).
func TestHandlePlanReviewApproval_CompletionApproval(t *testing.T) {
	coder := createTestCoder(t, nil)
	sm := coder.BaseStateMachine

	ctx := context.Background()
	nextState, completed, err := coder.handlePlanReviewApproval(ctx, sm, proto.ApprovalTypeCompletion)
	if err != nil {
		t.Fatalf("handlePlanReviewApproval returned error: %v", err)
	}

	if !completed {
		t.Error("expected completed=true for completion approval")
	}
	if nextState != proto.StateDone {
		t.Errorf("expected next state %s, got %s", proto.StateDone, nextState)
	}
}
