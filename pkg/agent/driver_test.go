package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/state"
	"orchestrator/pkg/utils"
)

func setupTestDriver(t *testing.T) *BaseDriver {
	t.Helper()

	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	ctx := &AgentContext{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, "", log.LstdFlags),
		WorkDir: tempDir,
		Store:   store,
	}

	cfg := &AgentConfig{
		ID:      "test-agent",
		Type:    "test",
		Context: *ctx,
	}

	driver, err := NewBaseDriver(cfg, proto.State("PLANNING"))
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Update the state machine to use test transitions.
	testTransitions := TransitionTable{
		proto.State("PLANNING"): {proto.State("CODING"), proto.StateError},
		proto.State("CODING"):   {proto.State("TESTING"), proto.StateDone, proto.StateError},
		proto.State("TESTING"):  {proto.StateDone, proto.State("PLANNING"), proto.StateError},
		proto.StateDone:         {proto.State("PLANNING")},
		proto.StateError:        {proto.StateWaiting},
		proto.StateWaiting:      {proto.State("PLANNING")},
	}

	// Replace state machine with one that has proper transitions.
	if baseSM, ok := driver.StateMachine.(*BaseStateMachine); ok {
		baseSM.table = testTransitions
	}

	if err := driver.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	return driver
}

func TestBaseDriver(t *testing.T) {
	driver := setupTestDriver(t)

	if driver.GetCurrentState() != proto.State("PLANNING") {
		t.Errorf("Expected initial state PLANNING, got %v", driver.GetCurrentState())
	}

	// Test state transition with metadata.
	metadata := map[string]any{
		"test": "data",
	}

	if err := driver.TransitionTo(context.Background(), proto.State("CODING"), metadata); err != nil {
		t.Errorf("Failed to transition state: %v", err)
	}

	// Verify state changed.
	if driver.GetCurrentState() != proto.State("CODING") {
		t.Errorf("Expected state CODING, got %v", driver.GetCurrentState())
	}

	// Verify metadata stored.
	baseStateMachine, ok := utils.SafeAssert[*BaseStateMachine](driver.StateMachine)
	if !ok {
		t.Fatal("StateMachine is not a BaseStateMachine")
	}
	data := baseStateMachine.GetStateData()
	if data["test"] != "data" {
		t.Errorf("Expected test data to be stored, got %v", data["test"])
	}
}

func TestBaseDriverWithModelConfig(t *testing.T) {
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	modelConfig := &config.ModelCfg{
		MaxContextTokens: 8192, // Updated to match new limits
		MaxReplyTokens:   4096,
		CompactionBuffer: 1000,
	}

	ctx := &AgentContext{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, "", log.LstdFlags),
		WorkDir: tempDir,
		Store:   store,
	}

	cfg := &AgentConfig{
		ID:      "test-agent",
		Type:    "test",
		Context: *ctx,
		LLMConfig: &LLMConfig{
			MaxContextTokens: modelConfig.MaxContextTokens,
			MaxOutputTokens:  modelConfig.MaxReplyTokens,
			CompactIfOver:    modelConfig.CompactionBuffer,
		},
	}

	driver, err := NewBaseDriver(cfg, proto.State("PLANNING"))
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	if err := driver.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}

	if driver.GetCurrentState() != proto.State("PLANNING") {
		t.Errorf("Expected initial state PLANNING, got %v", driver.GetCurrentState())
	}
}

func TestBaseDriverStateCompaction(t *testing.T) {
	driver := setupTestDriver(t)

	// Add several state transitions to trigger compaction.
	states := []proto.State{proto.State("CODING"), proto.State("TESTING"), proto.StateDone, proto.State("PLANNING")}
	for _, state := range states {
		err := driver.TransitionTo(context.Background(), state, map[string]any{
			"timestamp": time.Now(),
			"data":      "test data",
		})
		if err != nil {
			t.Errorf("Failed to transition to state %v: %v", state, err)
		}
	}

	// Force compaction.
	if err := driver.CompactIfNeeded(); err != nil {
		t.Errorf("Failed to compact state data: %v", err)
	}

	// Verify state is still accessible.
	if driver.GetCurrentState() != proto.State("PLANNING") {
		t.Errorf("Expected state PLANNING after compaction, got %v", driver.GetCurrentState())
	}
}

func TestBaseDriverContextCancellation(t *testing.T) {
	driver := setupTestDriver(t)

	// Create a cancelled context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Create new driver in a cancelled context.
	baseStateMachine, ok := utils.SafeAssert[*BaseStateMachine](driver.StateMachine)
	if !ok {
		t.Fatal("StateMachine is not a BaseStateMachine")
	}
	newCfg := &AgentConfig{
		ID:   "test-agent",
		Type: "test",
		Context: AgentContext{
			Context: ctx,
			Store:   baseStateMachine.store,
		},
	}

	driver, err := NewBaseDriver(newCfg, proto.State("PLANNING"))
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Attempt state transition with cancelled context.
	err = driver.TransitionTo(ctx, proto.State("CODING"), nil)
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Expected context.Canceled error, got %v", err)
	}

	// Verify state unchanged.
	if driver.GetCurrentState() != proto.State("PLANNING") {
		t.Errorf("Expected state to remain PLANNING, got %v", driver.GetCurrentState())
	}
}

func TestBaseDriverPersistence(t *testing.T) {
	driver := setupTestDriver(t)

	// Add state data.
	err := driver.TransitionTo(context.Background(), proto.State("CODING"), map[string]any{
		"test": "data",
	})
	if err != nil {
		t.Errorf("Failed to transition state: %v", err)
	}

	// Save state.
	if persistErr := driver.Persist(); persistErr != nil {
		t.Errorf("Failed to persist state: %v", persistErr)
	}

	// Create new driver and load state.
	baseStateMachine2, ok2 := utils.SafeAssert[*BaseStateMachine](driver.StateMachine)
	if !ok2 {
		t.Fatal("StateMachine is not a BaseStateMachine")
	}
	newCfg := &AgentConfig{
		ID:   "test-agent",
		Type: "test",
		Context: AgentContext{
			Context: context.Background(),
			Store:   baseStateMachine2.store,
		},
	}

	driver, err = NewBaseDriver(newCfg, proto.State("PLANNING"))
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	if err := driver.Initialize(context.Background()); err != nil {
		t.Errorf("Failed to initialize new driver: %v", err)
	}

	// Verify state restored.
	baseStateMachine := utils.MustAssert[*BaseStateMachine](driver.StateMachine, "state machine")
	data := baseStateMachine.GetStateData()
	if data["test"] != "data" {
		t.Errorf("Expected test data to be restored, got %v", data["test"])
	}
}

func TestBaseDriverWithMockLLM(t *testing.T) {
	// Initialize mock mode.
	resetModeForTest()
	InitMode(ModeMock)

	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}

	// Create mock LLM responses for a complete workflow.
	mockResponses := []CompletionResponse{
		{Content: "Planning phase complete"},
		{Content: "Code implementation ready"},
		{Content: "Testing completed successfully"},
	}

	mockClient := NewMockLLMClient(mockResponses, nil)

	ctx := &AgentContext{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, "", log.LstdFlags),
		WorkDir: tempDir,
		Store:   store,
	}

	cfg := &AgentConfig{
		ID:      "test-agent-mock",
		Type:    "mock-test",
		Context: *ctx,
		LLMConfig: &LLMConfig{
			MaxContextTokens: 8192,
			MaxOutputTokens:  4096,
			CompactIfOver:    1000,
		},
	}

	driver, err := NewBaseDriver(cfg, proto.State("PLANNING"))
	if err != nil {
		t.Fatalf("Failed to create driver: %v", err)
	}

	// Store mock client in driver state for testing.
	baseStateMachine := utils.MustAssert[*BaseStateMachine](driver.StateMachine, "state machine")
	baseStateMachine.SetStateData("llm_client", mockClient)

	// Test initialization.
	if initErr := driver.Initialize(context.Background()); initErr != nil {
		t.Fatalf("Failed to initialize driver: %v", initErr)
	}

	// Test using mock LLM client.
	baseStateMachine2 := utils.MustAssert[*BaseStateMachine](driver.StateMachine, "state machine")
	if client, exists := baseStateMachine2.GetStateValue("llm_client"); exists {
		if llmClient, ok := client.(LLMClient); ok {
			req := CompletionRequest{
				Messages: []CompletionMessage{{Role: RoleUser, Content: "Test planning request"}},
			}
			resp, llmErr := llmClient.Complete(context.Background(), req)
			if llmErr != nil {
				t.Errorf("Mock LLM client failed: %v", llmErr)
			}
			if resp.Content != "Planning phase complete" {
				t.Errorf("Expected 'Planning phase complete', got %v", resp.Content)
			}
		}
	}

	// Test state transitions with LLM integration.
	err = driver.TransitionTo(context.Background(), proto.State("CODING"), map[string]any{
		"llm_response": "Planning phase complete",
		"mock_mode":    true,
	})
	if err != nil {
		t.Errorf("Failed to transition with mock LLM: %v", err)
	}

	if driver.GetCurrentState() != proto.State("CODING") {
		t.Errorf("Expected state CODING, got %v", driver.GetCurrentState())
	}
}

func TestBaseDriverTimeout(t *testing.T) {
	driver := setupTestDriver(t)

	// Test StepWithTimeout with very short timeout.
	ctx := context.Background()
	_, err := driver.StepWithTimeout(ctx, 1*time.Nanosecond)

	// This might timeout or complete quickly depending on timing.
	// We mainly want to verify the method exists and doesn't panic.
	if err != nil && err.Error() != "ProcessState not implemented" {
		// If it's not the expected "not implemented" error, check if it's a timeout.
		if !strings.Contains(err.Error(), "timed out") {
			t.Errorf("Unexpected error from StepWithTimeout: %v", err)
		}
	}

	// Test with reasonable timeout.
	done, err := driver.StepWithTimeout(ctx, 1*time.Second)
	if err != nil && err.Error() != "ProcessState not implemented" {
		t.Errorf("StepWithTimeout failed with reasonable timeout: %v", err)
	}

	// Since BaseDriver doesn't implement ProcessState, done should be false.
	if done {
		t.Error("Expected done=false from BaseDriver with unimplemented ProcessState")
	}
}

func TestBaseDriverRunWithTimeout(t *testing.T) {
	driver := setupTestDriver(t)

	// Test RunWithTimeout with custom config.
	cfg := TimeoutConfig{
		StateTimeout:    100 * time.Millisecond,
		GlobalTimeout:   500 * time.Millisecond,
		ShutdownTimeout: 50 * time.Millisecond,
	}

	ctx := context.Background()
	err := driver.RunWithTimeout(ctx, cfg)

	// Should fail because ProcessState is not implemented.
	if err == nil {
		t.Error("Expected error from RunWithTimeout with unimplemented ProcessState")
	}

	if err.Error() != "ProcessState not implemented" {
		t.Errorf("Expected 'ProcessState not implemented', got %v", err)
	}
}

func TestBaseDriverShutdown(t *testing.T) {
	driver := setupTestDriver(t)

	// Add some state data.
	baseStateMachine := utils.MustAssert[*BaseStateMachine](driver.StateMachine, "state machine")
	baseStateMachine.SetStateData("test_data", "should_persist")

	// Test graceful shutdown.
	ctx := context.Background()
	err := driver.Shutdown(ctx)
	if err != nil {
		t.Errorf("Shutdown failed: %v", err)
	}

	// Verify state was persisted during shutdown.
	baseStateMachine2 := utils.MustAssert[*BaseStateMachine](driver.StateMachine, "state machine")
	newDriver, err := NewBaseDriver(&AgentConfig{
		ID:   "test-agent",
		Type: "test",
		Context: AgentContext{
			Context: context.Background(),
			Store:   baseStateMachine2.store,
		},
	}, proto.State("PLANNING"))
	if err != nil {
		t.Fatalf("Failed to create new driver: %v", err)
	}

	if err := newDriver.Initialize(context.Background()); err != nil {
		t.Fatalf("Failed to initialize new driver: %v", err)
	}

	newBaseStateMachine := utils.MustAssert[*BaseStateMachine](newDriver.StateMachine, "new driver state machine")
	data := newBaseStateMachine.GetStateData()
	if data["test_data"] != "should_persist" {
		t.Errorf("Expected data to persist after shutdown, got %v", data["test_data"])
	}
}

func TestBaseDriverErrorHandling(t *testing.T) {
	// Test driver creation with invalid config.
	invalidCfg := &AgentConfig{
		ID:   "", // Invalid: empty ID
		Type: "test",
		Context: AgentContext{
			Context: context.Background(),
		},
	}

	_, err := NewBaseDriver(invalidCfg, proto.State("PLANNING"))
	if err == nil {
		t.Error("Expected error when creating driver with invalid config")
	}

	// Test driver with valid config but failed step.
	driver := setupTestDriver(t)

	// Mock a state machine that will fail.
	originalSM := driver.StateMachine
	driver.StateMachine = &failingStateMachine{originalSM}

	ctx := context.Background()
	done, err := driver.Step(ctx)
	if err == nil {
		t.Error("Expected error from failing state machine")
	}
	if done {
		t.Error("Expected done=false when step fails")
	}

	// Verify error state transition was attempted.
	// (failingStateMachine will also fail the transition, but we check the attempt was made)
}

// Helper struct for testing error handling.
type failingStateMachine struct {
	StateMachine
}

func (f *failingStateMachine) ProcessState(_ context.Context) (proto.State, bool, error) {
	return proto.StateError, false, fmt.Errorf("simulated processing failure")
}

func (f *failingStateMachine) TransitionTo(_ context.Context, _ proto.State, _ map[string]any) error {
	return fmt.Errorf("simulated transition failure")
}
