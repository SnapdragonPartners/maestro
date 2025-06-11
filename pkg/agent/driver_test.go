package agent

import (
	"context"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/state"
)

func TestNewDriver(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	
	if driver == nil {
		t.Error("Expected non-nil driver")
	}
	
	if driver.agentID != "test-agent" {
		t.Errorf("Expected agentID 'test-agent', got '%s'", driver.agentID)
	}
	
	if driver.currentState != StatePlanning {
		t.Errorf("Expected initial state PLANNING, got %s", driver.currentState)
	}
	
	if driver.stateStore != stateStore {
		t.Error("Expected state store to be set")
	}
	
	if driver.contextManager == nil {
		t.Error("Expected context manager to be initialized")
	}
	
	if driver.stateData == nil {
		t.Error("Expected state data to be initialized")
	}
}

func TestDriver_Initialize(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	ctx := context.Background()
	
	// Test initialization with no existing state
	if err := driver.Initialize(ctx); err != nil {
		t.Errorf("Expected no error initializing driver, got %v", err)
	}
	
	// State should remain at default
	if driver.currentState != StatePlanning {
		t.Errorf("Expected state PLANNING after init with no saved state, got %s", driver.currentState)
	}
	
	// Save some state first
	testData := map[string]interface{}{"test": "data"}
	if err := stateStore.SaveState("test-agent", "CODING", testData); err != nil {
		t.Fatalf("Failed to save test state: %v", err)
	}
	
	// Create new driver and initialize
	driver2 := NewDriver("test-agent", stateStore)
	if err := driver2.Initialize(ctx); err != nil {
		t.Errorf("Expected no error initializing driver with saved state, got %v", err)
	}
	
	// Should restore saved state
	if driver2.currentState != StateCoding {
		t.Errorf("Expected state CODING after init with saved state, got %s", driver2.currentState)
	}
	
	if driver2.stateData["test"] != "data" {
		t.Errorf("Expected restored state data, got %v", driver2.stateData["test"])
	}
}

func TestDriver_ProcessTask_SimpleFlow(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	ctx := context.Background()
	
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	
	// Process a simple task (should go through the full flow)
	taskContent := "Create a simple hello world program"
	
	if err := driver.ProcessTask(ctx, taskContent); err != nil {
		t.Errorf("Expected no error processing task, got %v", err)
	}
	
	// Should end in DONE state
	if driver.currentState != StateDone {
		t.Errorf("Expected final state DONE, got %s", driver.currentState)
	}
	
	// Check that task content was stored
	if driver.stateData["task_content"] != taskContent {
		t.Errorf("Expected task content to be stored, got %v", driver.stateData["task_content"])
	}
	
	// Check that we have some completion timestamps
	if _, exists := driver.stateData["coding_completed_at"]; !exists {
		t.Error("Expected coding_completed_at timestamp")
	}
	
	if _, exists := driver.stateData["testing_completed_at"]; !exists {
		t.Error("Expected testing_completed_at timestamp")
	}
}

func TestDriver_ProcessTask_WithTools(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	ctx := context.Background()
	
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	
	// Process a task that should trigger tool invocation
	taskContent := "Run shell command to list files"
	
	if err := driver.ProcessTask(ctx, taskContent); err != nil {
		t.Errorf("Expected no error processing task with tools, got %v", err)
	}
	
	// Should still end in DONE state
	if driver.currentState != StateDone {
		t.Errorf("Expected final state DONE, got %s", driver.currentState)
	}
	
	// Check that tool results were stored
	if _, exists := driver.stateData["tool_results"]; !exists {
		t.Error("Expected tool_results in state data")
	}
	
	if _, exists := driver.stateData["tool_invocation_completed_at"]; !exists {
		t.Error("Expected tool_invocation_completed_at timestamp")
	}
}

func TestDriver_StateTransitions(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	ctx := context.Background()
	
	// Test manual state transitions
	initialState := driver.GetCurrentState()
	if initialState != StatePlanning {
		t.Errorf("Expected initial state PLANNING, got %s", initialState)
	}
	
	// Transition to coding
	if err := driver.transitionTo(ctx, StateCoding, map[string]interface{}{"test": "data"}); err != nil {
		t.Errorf("Expected no error transitioning to CODING, got %v", err)
	}
	
	if driver.GetCurrentState() != StateCoding {
		t.Errorf("Expected current state CODING, got %s", driver.GetCurrentState())
	}
	
	// Check that additional data was stored
	stateData := driver.GetStateData()
	if stateData["test"] != "data" {
		t.Errorf("Expected additional data to be stored, got %v", stateData["test"])
	}
	
	// Check transition metadata
	if stateData["previous_state"] != "PLANNING" {
		t.Errorf("Expected previous_state PLANNING, got %v", stateData["previous_state"])
	}
	
	if stateData["current_state"] != "CODING" {
		t.Errorf("Expected current_state CODING, got %v", stateData["current_state"])
	}
	
	// Check that state was persisted
	savedState, savedData, err := stateStore.LoadState("test-agent")
	if err != nil {
		t.Errorf("Expected no error loading saved state, got %v", err)
	}
	
	if savedState != "CODING" {
		t.Errorf("Expected saved state CODING, got %s", savedState)
	}
	
	if savedData["test"] != "data" {
		t.Errorf("Expected saved data to include test value, got %v", savedData["test"])
	}
}

func TestDriver_GetMethods(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	
	// Test GetCurrentState
	if driver.GetCurrentState() != StatePlanning {
		t.Errorf("Expected current state PLANNING, got %s", driver.GetCurrentState())
	}
	
	// Add some state data
	driver.stateData["key1"] = "value1"
	driver.stateData["key2"] = 42
	
	// Test GetStateData returns a copy
	stateData := driver.GetStateData()
	if stateData["key1"] != "value1" {
		t.Errorf("Expected key1 value1, got %v", stateData["key1"])
	}
	
	// Modify returned data - should not affect original
	stateData["key1"] = "modified"
	if driver.stateData["key1"] == "modified" {
		t.Error("GetStateData should return a copy, not the original")
	}
	
	// Test GetContextSummary
	summary := driver.GetContextSummary()
	if summary == "" {
		t.Error("Expected non-empty context summary")
	}
}

func TestContainsKeyword(t *testing.T) {
	// Test basic keyword matching
	if !containsKeyword("hello world", "hello") {
		t.Error("Expected to find 'hello' in 'hello world'")
	}
	
	if !containsKeyword("hello world", "world") {
		t.Error("Expected to find 'world' in 'hello world'")
	}
	
	// Test case insensitive
	if !containsKeyword("Hello World", "hello") {
		t.Error("Expected case insensitive match for 'hello'")
	}
	
	if !containsKeyword("hello world", "WORLD") {
		t.Error("Expected case insensitive match for 'WORLD'")
	}
	
	// Test non-matches
	if containsKeyword("hello world", "foo") {
		t.Error("Expected not to find 'foo' in 'hello world'")
	}
	
	// Test word boundaries (this is a simple implementation)
	if !containsKeyword("run shell command", "shell") {
		t.Error("Expected to find 'shell' in 'run shell command'")
	}
}

func TestDriver_ContextTimeout(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	driver := NewDriver("test-agent", stateStore)
	
	// Create a context with immediate timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Nanosecond)
	defer cancel()
	
	// Wait a bit to ensure timeout
	time.Sleep(1 * time.Millisecond)
	
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	
	// Process task should return context error
	err = driver.ProcessTask(ctx, "test task")
	if err == nil {
		t.Error("Expected context timeout error")
	}
	
	if err != context.DeadlineExceeded {
		t.Errorf("Expected context.DeadlineExceeded, got %v", err)
	}
}

func TestNewDriverWithModel(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	modelConfig := &config.ModelCfg{
		MaxContextTokens: 50000,
		MaxReplyTokens:   4000,
		CompactionBuffer: 1000,
	}
	
	driver := NewDriverWithModel("test-agent", stateStore, modelConfig)
	
	if driver == nil {
		t.Error("Expected non-nil driver")
	}
	
	if driver.agentID != "test-agent" {
		t.Errorf("Expected agentID 'test-agent', got '%s'", driver.agentID)
	}
	
	// Test that context manager has model configuration
	if driver.contextManager.GetMaxContextTokens() != 50000 {
		t.Errorf("Expected max context tokens 50000, got %d", driver.contextManager.GetMaxContextTokens())
	}
	
	if driver.contextManager.GetMaxReplyTokens() != 4000 {
		t.Errorf("Expected max reply tokens 4000, got %d", driver.contextManager.GetMaxReplyTokens())
	}
}

func TestDriverWithModelCompaction(t *testing.T) {
	tempDir := t.TempDir()
	stateStore, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create state store: %v", err)
	}
	
	// Use small limits to test compaction
	modelConfig := &config.ModelCfg{
		MaxContextTokens: 200,
		MaxReplyTokens:   50,
		CompactionBuffer: 20,
	}
	
	driver := NewDriverWithModel("test-agent", stateStore, modelConfig)
	ctx := context.Background()
	
	if err := driver.Initialize(ctx); err != nil {
		t.Fatalf("Failed to initialize driver: %v", err)
	}
	
	// Add many messages to trigger compaction
	for i := 0; i < 10; i++ {
		driver.contextManager.AddMessage("user", "This is a test message that helps us reach the compaction threshold by adding many tokens to the context")
	}
	
	initialTokens := driver.contextManager.CountTokens()
	t.Logf("Initial token count: %d", initialTokens)
	
	// Force compaction check
	if err := driver.contextManager.CompactIfNeeded(); err != nil {
		t.Errorf("Expected no error during compaction, got %v", err)
	}
	
	finalTokens := driver.contextManager.CountTokens()
	t.Logf("Final token count: %d", finalTokens)
	
	// Get compaction info
	info := driver.contextManager.GetCompactionInfo()
	t.Logf("Compaction info: %+v", info)
	
	// Should have compacted if we exceeded threshold
	compactionThreshold := 200 - 50 - 20 // 130
	if initialTokens > compactionThreshold {
		if finalTokens >= initialTokens {
			t.Errorf("Expected compaction to reduce token count from %d, but got %d", initialTokens, finalTokens)
		}
	}
}