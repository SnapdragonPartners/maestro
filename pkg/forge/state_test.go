package forge

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/config"
)

// TestStateSaveLoad tests the roundtrip save/load of State.
func TestStateSaveLoad(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	original := &State{
		Provider:      "gitea",
		URL:           "http://localhost:3000",
		Token:         "test-token-12345",
		Owner:         "maestro-dev",
		RepoName:      "my-project",
		Port:          3000,
		ContainerName: "maestro-gitea-my-project",
	}

	if saveErr := SaveState(tempDir, original); saveErr != nil {
		t.Fatalf("SaveState failed: %v", saveErr)
	}

	loaded, loadErr := LoadState(tempDir)
	if loadErr != nil {
		t.Fatalf("LoadState failed: %v", loadErr)
	}

	if loaded.Provider != original.Provider {
		t.Errorf("Provider mismatch: got %q, want %q", loaded.Provider, original.Provider)
	}
	if loaded.URL != original.URL {
		t.Errorf("URL mismatch: got %q, want %q", loaded.URL, original.URL)
	}
	if loaded.Token != original.Token {
		t.Errorf("Token mismatch: got %q, want %q", loaded.Token, original.Token)
	}
	if loaded.Owner != original.Owner {
		t.Errorf("Owner mismatch: got %q, want %q", loaded.Owner, original.Owner)
	}
	if loaded.RepoName != original.RepoName {
		t.Errorf("RepoName mismatch: got %q, want %q", loaded.RepoName, original.RepoName)
	}
	if loaded.Port != original.Port {
		t.Errorf("Port mismatch: got %d, want %d", loaded.Port, original.Port)
	}
	if loaded.ContainerName != original.ContainerName {
		t.Errorf("ContainerName mismatch: got %q, want %q", loaded.ContainerName, original.ContainerName)
	}
}

// TestStateFilePermissions tests that saved state has correct permissions.
func TestStateFilePermissions(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-perm-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	state := &State{
		Provider: "gitea",
		URL:      "http://localhost:3000",
		Token:    "secret-token",
	}
	if saveErr := SaveState(tempDir, state); saveErr != nil {
		t.Fatalf("SaveState failed: %v", saveErr)
	}

	statePath := filepath.Join(tempDir, config.ProjectConfigDir, ForgeStateFile)
	info, statErr := os.Stat(statePath)
	if statErr != nil {
		t.Fatalf("Failed to stat state file: %v", statErr)
	}

	perm := info.Mode().Perm()
	if perm != ForgeStatePermissions {
		t.Errorf("File permissions incorrect: got %o, want %o", perm, ForgeStatePermissions)
	}
}

// TestStateExists tests the existence check function.
func TestStateExists(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-exists-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	if StateExists(tempDir) {
		t.Error("StateExists should return false before save")
	}

	state := &State{Provider: "gitea", URL: "http://localhost:3000"}
	if saveErr := SaveState(tempDir, state); saveErr != nil {
		t.Fatalf("SaveState failed: %v", saveErr)
	}

	if !StateExists(tempDir) {
		t.Error("StateExists should return true after save")
	}
}

// TestLoadStateNotFound tests loading when file doesn't exist.
func TestLoadStateNotFound(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-notfound-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	_, loadErr := LoadState(tempDir)
	if !errors.Is(loadErr, ErrStateNotFound) {
		t.Errorf("Expected ErrStateNotFound, got: %v", loadErr)
	}
}

// TestDeleteState tests state deletion.
func TestDeleteState(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-delete-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	state := &State{Provider: "gitea", URL: "http://localhost:3000"}
	if saveErr := SaveState(tempDir, state); saveErr != nil {
		t.Fatalf("SaveState failed: %v", saveErr)
	}

	if !StateExists(tempDir) {
		t.Fatal("State should exist after save")
	}

	if delErr := DeleteState(tempDir); delErr != nil {
		t.Fatalf("DeleteState failed: %v", delErr)
	}

	if StateExists(tempDir) {
		t.Error("State should not exist after delete")
	}

	// Deleting again should not error (idempotent)
	if delErr := DeleteState(tempDir); delErr != nil {
		t.Errorf("Second delete should not error: %v", delErr)
	}
}

// TestSaveStateNil tests saving nil state.
func TestSaveStateNil(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-nil-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	saveErr := SaveState(tempDir, nil)
	if saveErr == nil {
		t.Error("SaveState should return error for nil state")
	}
}

// TestNewGiteaState tests the Gitea state constructor.
func TestNewGiteaState(t *testing.T) {
	state := NewGiteaState(
		"http://localhost:3000",
		"test-token",
		"maestro-dev",
		"my-project",
		3000,
		"maestro-gitea-my-project",
	)

	if state.Provider != "gitea" {
		t.Errorf("Provider should be 'gitea', got %q", state.Provider)
	}
	if state.URL != "http://localhost:3000" {
		t.Errorf("URL mismatch: got %q", state.URL)
	}
	if state.Token != "test-token" {
		t.Errorf("Token mismatch: got %q", state.Token)
	}
	if state.Owner != "maestro-dev" {
		t.Errorf("Owner mismatch: got %q", state.Owner)
	}
	if state.RepoName != "my-project" {
		t.Errorf("RepoName mismatch: got %q", state.RepoName)
	}
	if state.Port != 3000 {
		t.Errorf("Port mismatch: got %d", state.Port)
	}
	if state.ContainerName != "maestro-gitea-my-project" {
		t.Errorf("ContainerName mismatch: got %q", state.ContainerName)
	}
}

// TestNewGitHubState tests the GitHub state constructor.
func TestNewGitHubState(t *testing.T) {
	state := NewGitHubState("gh-token-12345", "my-org", "my-repo")

	if state.Provider != "github" {
		t.Errorf("Provider should be 'github', got %q", state.Provider)
	}
	if state.URL != "https://api.github.com" {
		t.Errorf("URL should be GitHub API URL, got %q", state.URL)
	}
	if state.Token != "gh-token-12345" {
		t.Errorf("Token mismatch: got %q", state.Token)
	}
	if state.Owner != "my-org" {
		t.Errorf("Owner mismatch: got %q", state.Owner)
	}
	if state.RepoName != "my-repo" {
		t.Errorf("RepoName mismatch: got %q", state.RepoName)
	}
	if state.Port != 0 {
		t.Errorf("Port should be 0 for GitHub, got %d", state.Port)
	}
	if state.ContainerName != "" {
		t.Errorf("ContainerName should be empty for GitHub, got %q", state.ContainerName)
	}
}

// TestStateOverwrite tests that saving overwrites existing state.
func TestStateOverwrite(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-overwrite-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	state1 := &State{
		Provider: "github",
		URL:      "https://api.github.com",
		Token:    "old-token",
	}
	if saveErr := SaveState(tempDir, state1); saveErr != nil {
		t.Fatalf("First save failed: %v", saveErr)
	}

	state2 := &State{
		Provider: "gitea",
		URL:      "http://localhost:3000",
		Token:    "new-token",
	}
	if saveErr := SaveState(tempDir, state2); saveErr != nil {
		t.Fatalf("Second save failed: %v", saveErr)
	}

	loaded, loadErr := LoadState(tempDir)
	if loadErr != nil {
		t.Fatalf("Load failed: %v", loadErr)
	}

	if loaded.Provider != "gitea" {
		t.Errorf("Provider should be 'gitea' after overwrite, got %q", loaded.Provider)
	}
	if loaded.Token != "new-token" {
		t.Errorf("Token should be 'new-token' after overwrite, got %q", loaded.Token)
	}
}

// TestStateGitHubOmitsContainerFields tests that GitHub state omits container fields in JSON.
func TestStateGitHubOmitsContainerFields(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-omit-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	state := NewGitHubState("token", "owner", "repo")
	if saveErr := SaveState(tempDir, state); saveErr != nil {
		t.Fatalf("Save failed: %v", saveErr)
	}

	statePath := filepath.Join(tempDir, config.ProjectConfigDir, ForgeStateFile)
	data, readErr := os.ReadFile(statePath)
	if readErr != nil {
		t.Fatalf("Failed to read state file: %v", readErr)
	}

	jsonStr := string(data)
	if contains(jsonStr, `"port"`) {
		t.Error("Port should be omitted from JSON when zero")
	}
	if contains(jsonStr, `"container_name"`) {
		t.Error("ContainerName should be omitted from JSON when empty")
	}
}

// contains is a helper to check if a string contains a substring.
func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestStateCreatesDirectory tests that SaveState creates .maestro if needed.
func TestStateCreatesDirectory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "forge-state-mkdir-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	maestroDir := filepath.Join(tempDir, config.ProjectConfigDir)
	if _, statErr := os.Stat(maestroDir); !os.IsNotExist(statErr) {
		t.Fatal(".maestro should not exist initially")
	}

	state := &State{Provider: "gitea"}
	if saveErr := SaveState(tempDir, state); saveErr != nil {
		t.Fatalf("Save failed: %v", saveErr)
	}

	if _, statErr := os.Stat(maestroDir); statErr != nil {
		t.Errorf(".maestro directory should exist after save: %v", statErr)
	}
}
