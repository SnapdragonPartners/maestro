package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNewStore(t *testing.T) {
	// Create temporary directory
	tempDir := t.TempDir()

	store, err := NewStore(tempDir)
	if err != nil {
		t.Fatalf("Expected no error creating store, got %v", err)
	}

	if store == nil {
		t.Error("Expected non-nil store")
	}

	if store.baseDir != tempDir {
		t.Errorf("Expected baseDir %s, got %s", tempDir, store.baseDir)
	}

	// Check that directory was created
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

	// Test invalid parameters
	if err := store.SaveState("", "state", nil); err == nil {
		t.Error("Expected error for empty agentID")
	}

	if err := store.SaveState("agent1", "", nil); err == nil {
		t.Error("Expected error for empty state")
	}

	// Test valid save
	data := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
	}

	if err := store.SaveState("agent1", "PLANNING", data); err != nil {
		t.Errorf("Expected no error saving state, got %v", err)
	}

	// Check that file was created
	filename := filepath.Join(tempDir, "STATUS_agent1.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Expected state file to be created")
	}

	// Check file content is valid JSON
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

	// Test invalid parameters
	if _, _, err := store.LoadState(""); err == nil {
		t.Error("Expected error for empty agentID")
	}

	// Test loading non-existent state
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

	// Save state first
	originalData := map[string]interface{}{
		"key1": "value1",
		"key2": 42,
		"key3": []string{"a", "b", "c"},
	}

	if err := store.SaveState("agent1", "CODING", originalData); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Load state
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

	// Check data content
	if loadedData["key1"] != "value1" {
		t.Errorf("Expected key1 value 'value1', got %v", loadedData["key1"])
	}

	// Note: JSON unmarshaling converts numbers to float64
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

	// Test non-existent agent
	if _, err := store.GetStateInfo("nonexistent"); err == nil {
		t.Error("Expected error for non-existent agent")
	}

	// Save state first
	data := map[string]interface{}{"test": "data"}
	if err := store.SaveState("agent1", "TESTING", data); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Get state info
	info, err := store.GetStateInfo("agent1")
	if err != nil {
		t.Errorf("Expected no error getting state info, got %v", err)
	}

	if info == nil {
		t.Error("Expected non-nil state info")
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

	// Save state first
	if err := store.SaveState("agent1", "DONE", nil); err != nil {
		t.Fatalf("Failed to save state: %v", err)
	}

	// Verify file exists
	filename := filepath.Join(tempDir, "STATUS_agent1.json")
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		t.Error("Expected state file to exist before deletion")
	}

	// Delete state
	if err := store.DeleteState("agent1"); err != nil {
		t.Errorf("Expected no error deleting state, got %v", err)
	}

	// Verify file is gone
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

	// Test empty directory
	agents, err := store.ListAgents()
	if err != nil {
		t.Errorf("Expected no error listing agents, got %v", err)
	}
	if len(agents) != 0 {
		t.Errorf("Expected 0 agents in empty directory, got %d", len(agents))
	}

	// Save states for multiple agents
	if err := store.SaveState("agent1", "PLANNING", nil); err != nil {
		t.Fatalf("Failed to save state for agent1: %v", err)
	}

	if err := store.SaveState("agent2", "CODING", nil); err != nil {
		t.Fatalf("Failed to save state for agent2: %v", err)
	}

	if err := store.SaveState("claude-test", "DONE", nil); err != nil {
		t.Fatalf("Failed to save state for claude-test: %v", err)
	}

	// Create a non-state file (should be ignored)
	if err := os.WriteFile(filepath.Join(tempDir, "other-file.txt"), []byte("test"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// List agents
	agents, err = store.ListAgents()
	if err != nil {
		t.Errorf("Expected no error listing agents, got %v", err)
	}

	if len(agents) != 3 {
		t.Errorf("Expected 3 agents, got %d", len(agents))
	}

	// Check that all expected agents are present
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

	// Test uninitialized global store
	if err := SaveState("agent1", "PLANNING", nil); err == nil {
		t.Error("Expected error when global store not initialized")
	}

	if _, _, err := LoadState("agent1"); err == nil {
		t.Error("Expected error when global store not initialized")
	}

	// Initialize global store
	if err := InitGlobalStore(tempDir); err != nil {
		t.Fatalf("Failed to initialize global store: %v", err)
	}

	// Test global store functions
	data := map[string]interface{}{"global": "test"}

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

	// Test GetGlobalStore
	store := GetGlobalStore()
	if store == nil {
		t.Error("Expected non-nil global store")
	}
}
