package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	// Create temporary directory.
	tempDir := t.TempDir()

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Expected no error creating store, got %v", err)
	}

	if store == nil {
		t.Fatal("Expected non-nil store")
	}

	if store.baseDir != tempDir {
		t.Errorf("Expected baseDir %s, got %s", tempDir, store.baseDir)
	}

	// Check that directory was created.
	if _, err := os.Stat(tempDir); os.IsNotExist(err) {
		t.Error("Expected base directory to be created")
	}
}

func TestStore_SaveState(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Test invalid parameters.
	if saveErr := store.SaveState("", "state", nil); saveErr == nil {
		t.Error("Expected error for empty agentID")
	}

	if saveErr := store.SaveState("agent1", "", nil); saveErr == nil {
		t.Error("Expected error for empty state")
	}

	// Test valid save.
	data := map[string]any{
		"key1": "value1",
		"key2": 42,
	}

	if saveErr := store.SaveState("agent1", "PLANNING", data); saveErr != nil {
		t.Errorf("Expected no error saving state, got %v", saveErr)
	}

	// Check that file was created.
	filename := filepath.Join(tempDir, "STATUS_agent1.json")
	if _, statErr := os.Stat(filename); os.IsNotExist(statErr) {
		t.Error("Expected state file to be created")
	}

	// Check file content is valid JSON.
	fileData, err := os.ReadFile(filename)
	if err != nil {
		t.Fatalf("Failed to read state file: %v", err)
	}

	if len(fileData) == 0 {
		t.Error("Expected non-empty state file")
	}
}

func TestStore_LoadState(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Test invalid parameters.
	if _, _, loadErr := store.LoadState(""); loadErr == nil {
		t.Error("Expected error for empty agentID")
	}

	// Test loading non-existent state.
	state, data, err := store.LoadState("nonexistent")
	if err != nil {
		t.Errorf("Expected no error for non-existent state, got %v", err)
	}
	if state != "" {
		t.Errorf("Expected empty state for non-existent agent, got %s", state)
	}
	if data == nil {
		t.Error("Expected non-nil data map")
	}
	if len(data) != 0 {
		t.Errorf("Expected empty data map, got %v", data)
	}

	// Save state first.
	originalData := map[string]any{
		"key1": "value1",
		"key2": 42,
		"key3": []string{"a", "b", "c"},
	}

	if saveErr := store.SaveState("agent1", "CODING", originalData); saveErr != nil {
		t.Fatalf("Failed to save state: %v", saveErr)
	}

	// Load state.
	loadedState, loadedData, err := store.LoadState("agent1")
	if err != nil {
		t.Errorf("Expected no error loading state, got %v", err)
	}

	if loadedState != "CODING" {
		t.Errorf("Expected state 'CODING', got '%s'", loadedState)
	}

	if loadedData == nil {
		t.Error("Expected non-nil loaded data")
	}

	// Check data content.
	if loadedData["key1"] != "value1" {
		t.Errorf("Expected key1 value 'value1', got %v", loadedData["key1"])
	}

	// Note: JSON unmarshaling converts numbers to float64.
	if loadedData["key2"] != float64(42) {
		t.Errorf("Expected key2 value 42.0, got %v", loadedData["key2"])
	}
}

func TestStore_GetStateInfo(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Test non-existent agent.
	if _, infoErr := store.GetStateInfo("nonexistent"); infoErr == nil {
		t.Error("Expected error for non-existent agent")
	}

	// Save state first.
	data := map[string]any{"test": "data"}
	if saveErr := store.SaveState("agent1", "TESTING", data); saveErr != nil {
		t.Fatalf("Failed to save state: %v", saveErr)
	}

	// Get state info.
	info, err := store.GetStateInfo("agent1")
	if err != nil {
		t.Errorf("Expected no error getting state info, got %v", err)
	}

	if info == nil {
		t.Fatal("Expected non-nil state info")
	}

	if info.State != "TESTING" {
		t.Errorf("Expected state 'TESTING', got '%s'", info.State)
	}

	if info.LastTimestamp.IsZero() {
		t.Error("Expected non-zero timestamp")
	}

	if time.Since(info.LastTimestamp) > time.Minute {
		t.Error("Expected recent timestamp")
	}

	if info.ContextSnapshot == nil {
		t.Error("Expected non-nil context snapshot")
	}

	if info.ContextSnapshot["agent_id"] != "agent1" {
		t.Errorf("Expected agent_id 'agent1' in context, got %v", info.ContextSnapshot["agent_id"])
	}
}

func TestStore_DeleteState(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Test deleting non-existent state (should not error)
	if err := store.DeleteState("nonexistent"); err != nil {
		t.Errorf("Expected no error deleting non-existent state, got %v", err)
	}

	// Save state first.
	if err := store.SaveState("agent1", "DONE", nil); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Verify file exists.
	filename := filepath.Join(tempDir, "STATUS_agent1.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Expected state file to exist before deletion")
	}

	// Delete state.
	if err := store.DeleteState("agent1"); err != nil {
		t.Errorf("Expected no error deleting state, got %v", err)
	}

	// Verify file is gone.
	if _, err := os.Stat(filename); !os.IsNotExist(err) {
		t.Error("Expected state file to be deleted")
	}
}

func TestStore_ListAgents(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Test empty directory.
	agents, err := store.ListAgents()
	if err != nil {
		t.Errorf("Expected no error listing agents, got %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("Expected 0 agents in empty directory, got %d", len(agents))
	}

	// Save states for multiple agents.
	if saveErr := store.SaveState("agent1", "PLANNING", nil); saveErr != nil {
		t.Fatalf("Failed to save state for agent1: %v", saveErr)
	}

	if saveErr := store.SaveState("agent2", "CODING", nil); saveErr != nil {
		t.Fatalf("Failed to save state for agent2: %v", saveErr)
	}

	if saveErr := store.SaveState("claude-test", "DONE", nil); saveErr != nil {
		t.Fatalf("Failed to save state for claude-test: %v", saveErr)
	}

	// Create a non-state file (should be ignored)
	if writeErr := os.WriteFile(filepath.Join(tempDir, "other-file.txt"), []byte("test"), 0644); writeErr != nil {
		t.Fatalf("Failed to create test file: %v", writeErr)
	}

	// List agents.
	agents, err = store.ListAgents()
	if err != nil {
		t.Errorf("Expected no error listing agents, got %v", err)
	}

	if len(agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(agents))
	}

	// Check that all expected agents are present.
	expectedAgents := map[string]bool{
		"agent1":      false,
		"agent2":      false,
		"claude-test": false,
	}

	for _, agentID := range agents {
		if _, exists := expectedAgents[agentID]; exists {
			expectedAgents[agentID] = true
		} else {
			t.Errorf("Unexpected agent ID: %s", agentID)
		}
	}

	for agentID, found := range expectedAgents {
		if !found {
			t.Errorf("Expected agent ID %s not found", agentID)
		}
	}
}

func TestGlobalStoreFunctions(t *testing.T) {
	tempDir := t.TempDir()

	// Test uninitialized global store.
	if err := SaveState("agent1", "PLANNING", nil); err == nil {
		t.Error("Expected error when global store not initialized")
	}

	if _, _, err := LoadState("agent1"); err == nil {
		t.Error("Expected error when global store not initialized")
	}

	// Initialize global store.
	if err := InitGlobalStore(tempDir); err != nil {
		t.Fatalf("Failed to initialize global store: %v", err)
	}

	// Test global store functions.
	data := map[string]any{"global": "test"}

	if err := SaveState("global-agent", "TESTING", data); err != nil {
		t.Errorf("Expected no error with global SaveState, got %v", err)
	}

	state, loadedData, err := LoadState("global-agent")
	if err != nil {
		t.Errorf("Expected no error with global LoadState, got %v", err)
	}

	if state != "TESTING" {
		t.Errorf("Expected state 'TESTING', got '%s'", state)
	}

	if loadedData["global"] != "test" {
		t.Errorf("Expected global data 'test', got %v", loadedData["global"])
	}

	// Test GetGlobalStore.
	store := GetGlobalStore()
	if store == nil {
		t.Error("Expected non-nil global store")
	}
}

func TestAgentState_NewFields(t *testing.T) {
	tempDir := t.TempDir()
	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create AgentState with new fields.
	plan := "Test implementation plan"
	taskContent := "Implement feature X with Y"

	// Test saving state with new fields.
	if saveErr := store.SaveState("test-agent", "PLANNING", nil); saveErr != nil {
		t.Fatalf("Failed to save initial state: %v", saveErr)
	}

	// Load and modify the state.
	info, err := store.GetStateInfo("test-agent")
	if err != nil {
		t.Fatalf("Failed to get state info: %v", err)
	}

	// Test version field.
	if info.Version != "v1" {
		t.Errorf("Expected version 'v1', got '%s'", info.Version)
	}

	// Add plan and task content.
	info.Plan = &plan
	info.TaskContent = &taskContent

	// Test AppendTransition method.
	info.AppendTransition("WAITING", "PLANNING")
	info.AppendTransition("PLANNING", "CODING")

	// Save updated state.
	if saveErr := store.Save("test-agent", info); saveErr != nil {
		t.Fatalf("Failed to save updated state: %v", saveErr)
	}

	// Load again and verify.
	reloaded, err := store.GetStateInfo("test-agent")
	if err != nil {
		t.Fatalf("Failed to reload state: %v", err)
	}

	// Verify plan field.
	if reloaded.Plan == nil {
		t.Error("Expected non-nil plan")
	} else if *reloaded.Plan != plan {
		t.Errorf("Expected plan '%s', got '%s'", plan, *reloaded.Plan)
	}

	// Verify task content field.
	if reloaded.TaskContent == nil {
		t.Error("Expected non-nil task content")
	} else if *reloaded.TaskContent != taskContent {
		t.Errorf("Expected task content '%s', got '%s'", taskContent, *reloaded.TaskContent)
	}

	// Verify transitions.
	if len(reloaded.Transitions) != 2 {
		t.Errorf("Expected 2 transitions, got %d", len(reloaded.Transitions))
	}

	if len(reloaded.Transitions) >= 1 {
		tr1 := reloaded.Transitions[0]
		if tr1.From != "WAITING" || tr1.To != "PLANNING" {
			t.Errorf("Expected transition WAITING->PLANNING, got %s->%s", tr1.From, tr1.To)
		}
		if tr1.TS.IsZero() {
			t.Error("Expected non-zero timestamp for first transition")
		}
	}

	if len(reloaded.Transitions) >= 2 {
		tr2 := reloaded.Transitions[1]
		if tr2.From != "PLANNING" || tr2.To != "CODING" {
			t.Errorf("Expected transition PLANNING->CODING, got %s->%s", tr2.From, tr2.To)
		}
		if tr2.TS.IsZero() {
			t.Error("Expected non-zero timestamp for second transition")
		}
	}
}

func TestAgentState_AppendTransition(t *testing.T) {
	// Test multiple calls to AppendTransition.
	state := &AgentState{}

	// Initially no transitions.
	if len(state.Transitions) != 0 {
		t.Errorf("Expected 0 initial transitions, got %d", len(state.Transitions))
	}

	// Add first transition.
	state.AppendTransition("IDLE", "WORKING")
	if len(state.Transitions) != 1 {
		t.Errorf("Expected 1 transition after first append, got %d", len(state.Transitions))
	}

	// Add second transition.
	state.AppendTransition("WORKING", "DONE")
	if len(state.Transitions) != 2 {
		t.Errorf("Expected 2 transitions after second append, got %d", len(state.Transitions))
	}

	// Verify order and content.
	if state.Transitions[0].From != "IDLE" || state.Transitions[0].To != "WORKING" {
		t.Errorf("Expected first transition IDLE->WORKING, got %s->%s",
			state.Transitions[0].From, state.Transitions[0].To)
	}

	if state.Transitions[1].From != "WORKING" || state.Transitions[1].To != "DONE" {
		t.Errorf("Expected second transition WORKING->DONE, got %s->%s",
			state.Transitions[1].From, state.Transitions[1].To)
	}

	// Verify timestamps are set and reasonable.
	for i, tr := range state.Transitions {
		if tr.TS.IsZero() {
			t.Errorf("Expected non-zero timestamp for transition %d", i)
		}
		if time.Since(tr.TS) > time.Minute {
			t.Errorf("Expected recent timestamp for transition %d", i)
		}
	}
}
