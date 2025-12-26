// Package forge provides abstractions for git hosting providers (GitHub, Gitea).
// This package manages runtime state for forge providers, particularly for
// airplane mode where a local Gitea instance is used.
package forge

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"orchestrator/pkg/config"
)

// ForgeStateFile is the filename for forge state within .maestro directory.
const ForgeStateFile = "forge_state.json"

// ForgeStatePermissions are restrictive file permissions for the state file.
// The file contains a token, so we use 0600 (owner read/write only).
const ForgeStatePermissions = 0600

// State represents runtime state for the active forge provider.
// This is persisted separately from config because it contains runtime-generated
// values like tokens and dynamically assigned ports.
type State struct {
	// Provider identifies which forge provider is active ("github" or "gitea").
	Provider string `json:"provider"`
	// URL is the base URL for the forge API (e.g., "http://localhost:3000" for Gitea).
	URL string `json:"url"`
	// Token is the API token for authentication.
	// For Gitea in airplane mode, this is auto-generated.
	Token string `json:"token"`
	// Owner is the organization or user that owns repositories.
	Owner string `json:"owner"`
	// RepoName is the name of the primary repository.
	RepoName string `json:"repo_name"`
	// ContainerName is the Docker container name for the forge service.
	// Format: maestro-gitea-{project-name} for per-project isolation.
	ContainerName string `json:"container_name,omitempty"`
	// Port is the HTTP port for the forge service.
	// Used primarily for Gitea container management.
	Port int `json:"port,omitempty"`
}

// ErrStateNotFound is returned when no forge state file exists.
var ErrStateNotFound = errors.New("forge state not found")

// SaveState persists forge state to the project's .maestro directory.
// The file is written with restrictive permissions (0600) since it contains a token.
func SaveState(projectDir string, state *State) error {
	if state == nil {
		return errors.New("cannot save nil forge state")
	}

	// Ensure .maestro directory exists
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", err)
	}

	// Marshal state to JSON
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal forge state: %w", err)
	}

	// Write file with restrictive permissions
	statePath := filepath.Join(maestroDir, ForgeStateFile)
	if err := os.WriteFile(statePath, data, ForgeStatePermissions); err != nil {
		return fmt.Errorf("failed to write forge state: %w", err)
	}

	return nil
}

// LoadState reads forge state from the project's .maestro directory.
// Returns ErrStateNotFound if the state file doesn't exist.
func LoadState(projectDir string) (*State, error) {
	statePath := filepath.Join(projectDir, config.ProjectConfigDir, ForgeStateFile)

	data, err := os.ReadFile(statePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrStateNotFound
		}
		return nil, fmt.Errorf("failed to read forge state: %w", err)
	}

	var state State
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("failed to unmarshal forge state: %w", err)
	}

	return &state, nil
}

// StateExists checks if a forge state file exists in the project directory.
func StateExists(projectDir string) bool {
	statePath := filepath.Join(projectDir, config.ProjectConfigDir, ForgeStateFile)
	_, err := os.Stat(statePath)
	return err == nil
}

// DeleteState removes the forge state file if it exists.
// This is typically called when tearing down a Gitea instance.
func DeleteState(projectDir string) error {
	statePath := filepath.Join(projectDir, config.ProjectConfigDir, ForgeStateFile)

	err := os.Remove(statePath)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("failed to delete forge state: %w", err)
	}

	return nil
}

// NewGiteaState creates a State configured for a local Gitea instance.
// This is a convenience constructor for airplane mode setup.
func NewGiteaState(url, token, owner, repoName string, port int, containerName string) *State {
	return &State{
		Provider:      "gitea",
		URL:           url,
		Token:         token,
		Owner:         owner,
		RepoName:      repoName,
		Port:          port,
		ContainerName: containerName,
	}
}

// NewGitHubState creates a State configured for GitHub.
// This is a convenience constructor for standard mode setup.
func NewGitHubState(token, owner, repoName string) *State {
	return &State{
		Provider: "github",
		URL:      "https://api.github.com",
		Token:    token,
		Owner:    owner,
		RepoName: repoName,
	}
}
