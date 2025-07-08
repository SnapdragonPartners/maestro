package agent

import (
	"context"
	"testing"

	"orchestrator/pkg/state"
)

func TestBaseStateMachine(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create a test transition table with valid transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {StateDone, StateError},
		StateDone:         {},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	sm := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)

	// Test initial state
	if sm.GetCurrentState() != State("PLANNING") {
		t.Errorf("Expected initial state PLANNING, got %v", sm.GetCurrentState())
	}

	// Test state data operations
	sm.SetStateData("test_key", "test_value")
	value, exists := sm.GetStateValue("test_key")
	if !exists {
		t.Error("Expected test_key to exist in state data")
	}
	if value != "test_value" {
		t.Errorf("Expected 'test_value', got %v", value)
	}

	// Test valid state transition
	metadata := map[string]any{
		"transition_reason": "testing",
	}
	err = sm.TransitionTo(context.Background(), State("CODING"), metadata)
	if err != nil {
		t.Errorf("Failed to transition to CODING: %v", err)
	}

	if sm.GetCurrentState() != State("CODING") {
		t.Errorf("Expected state CODING, got %v", sm.GetCurrentState())
	}

	// Verify metadata was stored
	data := sm.GetStateData()
	if data["transition_reason"] != "testing" {
		t.Errorf("Expected transition metadata to be stored")
	}

	// Test transition history
	transitions := sm.GetTransitions()
	if len(transitions) != 1 {
		t.Errorf("Expected 1 transition, got %d", len(transitions))
	}
	if transitions[0].FromState != State("PLANNING") || transitions[0].ToState != State("CODING") {
		t.Errorf("Unexpected transition: %v -> %v", transitions[0].FromState, transitions[0].ToState)
	}
}

func TestBaseStateMachineValidation(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create a test transition table with valid transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {StateDone, StateError},
		StateDone:         {},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	sm := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)

	// Test invalid transition
	err = sm.TransitionTo(context.Background(), State("TESTING"), nil)
	if err == nil {
		t.Error("Expected error for invalid transition PLANNING -> TESTING")
	}

	// Test transition to error state (should always be allowed)
	err = sm.TransitionTo(context.Background(), StateError, map[string]any{
		"error": "test error",
	})
	if err != nil {
		t.Errorf("Failed to transition to ERROR state: %v", err)
	}

	if sm.GetCurrentState() != StateError {
		t.Errorf("Expected state ERROR, got %v", sm.GetCurrentState())
	}
}

func TestBaseStateMachinePersistence(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create a test transition table with valid transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {StateDone, StateError},
		StateDone:         {},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	// Create and configure first state machine
	sm1 := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)
	sm1.SetStateData("persistent_data", "should_survive")

	err = sm1.TransitionTo(context.Background(), State("CODING"), map[string]any{
		"test": "metadata",
	})
	if err != nil {
		t.Fatalf("Failed to transition: %v", err)
	}

	// Persist state
	err = sm1.Persist()
	if err != nil {
		t.Fatalf("Failed to persist state: %v", err)
	}

	// Create new state machine and load state
	sm2 := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)
	err = sm2.Initialize(context.Background())
	if err != nil {
		t.Fatalf("Failed to initialize second state machine: %v", err)
	}

	// Verify state was restored
	if sm2.GetCurrentState() != State("CODING") {
		t.Errorf("Expected restored state CODING, got %v", sm2.GetCurrentState())
	}

	data := sm2.GetStateData()
	if data["persistent_data"] != "should_survive" {
		t.Errorf("Expected persistent data to be restored, got %v", data["persistent_data"])
	}

	if data["test"] != "metadata" {
		t.Errorf("Expected transition metadata to be restored, got %v", data["test"])
	}

	// Verify transitions were restored
	transitions := sm2.GetTransitions()
	if len(transitions) != 1 {
		t.Errorf("Expected 1 restored transition, got %d", len(transitions))
	}
}

func TestBaseStateMachineRetries(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create a test transition table with valid transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {StateDone, StateError},
		StateDone:         {},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	sm := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)
	sm.SetMaxRetries(2)

	// Test retry counting
	err = sm.IncrementRetry()
	if err != nil {
		t.Errorf("First retry should not fail: %v", err)
	}

	err = sm.IncrementRetry()
	if err == nil {
		t.Error("Expected error after exceeding max retries")
	}

	// Test retry reset on state transition
	sm.SetMaxRetries(5) // Reset for clean test
	sm.IncrementRetry()

	err = sm.TransitionTo(context.Background(), State("CODING"), nil)
	if err != nil {
		t.Fatalf("Failed to transition: %v", err)
	}

	// After transition, retry count should be reset, so we can retry again
	err = sm.IncrementRetry()
	if err != nil {
		t.Errorf("Retry should work after state transition: %v", err)
	}
}

func TestBaseStateMachineCompaction(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create a test transition table with valid circular transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {State("TESTING"), StateDone, StateError},
		State("TESTING"):  {StateDone, State("PLANNING"), StateError},
		StateDone:         {State("PLANNING")},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	sm := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)

	// Add many transitions to trigger compaction
	states := []State{State("CODING"), State("TESTING"), StateDone, State("PLANNING")}
	for i := 0; i < 150; i++ { // More than the 100 transition limit
		state := states[i%len(states)]
		err = sm.TransitionTo(context.Background(), state, map[string]any{
			"iteration": i,
		})
		if err != nil {
			t.Fatalf("Failed to transition at iteration %d: %v", i, err)
		}
	}

	// Verify we have more than 100 transitions
	transitions := sm.GetTransitions()
	if len(transitions) <= 100 {
		t.Errorf("Expected more than 100 transitions before compaction, got %d", len(transitions))
	}

	// Force compaction
	err = sm.CompactIfNeeded()
	if err != nil {
		t.Errorf("Compaction failed: %v", err)
	}

	// Verify compaction worked
	transitions = sm.GetTransitions()
	if len(transitions) > 100 {
		t.Errorf("Expected at most 100 transitions after compaction, got %d", len(transitions))
	}

	// State should still be accessible
	// The final state after 150 transitions should be states[149%4] = states[1] = State("TESTING")
	expectedFinalState := states[(150-1)%len(states)]
	if sm.GetCurrentState() != expectedFinalState {
		t.Errorf("Expected current state %v to be preserved after compaction, got %v", expectedFinalState, sm.GetCurrentState())
	}
}

func TestBaseStateMachineContextCancellation(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create a test transition table with valid transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {StateDone, StateError},
		StateDone:         {},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	sm := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)

	// Create cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Attempt transition with cancelled context
	err = sm.TransitionTo(ctx, State("CODING"), nil)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}

	// State should remain unchanged
	if sm.GetCurrentState() != State("PLANNING") {
		t.Errorf("Expected state to remain PLANNING after cancelled transition")
	}
}

func TestBaseStateMachineWithMockLLM(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Initialize mock mode
	resetModeForTest()
	InitMode(ModeMock)

	// Create mock LLM client
	mockResponses := []CompletionResponse{
		{Content: "Planning complete"},
		{Content: "Code generated"},
	}
	mockClient := NewMockLLMClient(mockResponses, nil)

	// Create a test transition table with valid transitions
	testTransitions := TransitionTable{
		State("PLANNING"): {State("CODING"), StateError},
		State("CODING"):   {StateDone, StateError},
		StateDone:         {},
		StateError:        {StateWaiting},
		StateWaiting:      {State("PLANNING")},
	}

	// Test that mock client works with state machine
	sm := NewBaseStateMachine("test-agent", State("PLANNING"), store, testTransitions)
	sm.SetStateData("llm_client", mockClient)

	// Simulate using LLM client during state processing
	if client, exists := sm.GetStateValue("llm_client"); exists {
		if llmClient, ok := client.(LLMClient); ok {
			req := CompletionRequest{
				Messages: []CompletionMessage{{Role: RoleUser, Content: "Test request"}},
			}
			resp, err := llmClient.Complete(context.Background(), req)
			if err != nil {
				t.Errorf("Mock LLM client failed: %v", err)
			}
			if resp.Content != "Planning complete" {
				t.Errorf("Expected 'Planning complete', got %v", resp.Content)
			}
		}
	}

	// Test state transition with LLM data
	err = sm.TransitionTo(context.Background(), State("CODING"), map[string]any{
		"llm_response": "Planning complete",
	})
	if err != nil {
		t.Errorf("Failed to transition with LLM data: %v", err)
	}
}
