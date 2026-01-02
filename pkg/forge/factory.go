package forge

import (
	"fmt"
	"os"

	"orchestrator/pkg/config"
)

// NewClient creates the appropriate forge client based on operating mode.
// In airplane mode, it creates a Gitea client using runtime state.
// In standard mode, it creates a GitHub client.
func NewClient(projectDir string) (Client, error) {
	if config.IsAirplaneMode() {
		return newGiteaClient(projectDir)
	}
	return newGitHubClient()
}

// newGiteaClient creates a Gitea client from runtime state.
// This is defined here as a function that will be implemented by the gitea package.
// We use a function variable to allow the gitea package to register itself.
//
//nolint:gochecknoglobals // Factory pattern requires global registration
var newGiteaClient = func(projectDir string) (Client, error) {
	// Load forge state
	state, err := LoadState(projectDir)
	if err != nil {
		return nil, fmt.Errorf("failed to load forge state: %w (run 'maestro --airplane' to set up)", err)
	}

	// Import cycle workaround: this function will be replaced by gitea.init()
	// For now, return an error indicating the gitea package needs to be imported
	return nil, fmt.Errorf("gitea client not registered - state: %+v", state)
}

// newGitHubClient creates a GitHub client.
// This is a stub that will need to be integrated with the existing github package.
//
//nolint:gochecknoglobals // Factory pattern requires global registration
var newGitHubClient = func() (Client, error) {
	// Check for required environment variable
	token := os.Getenv("GITHUB_TOKEN")
	if token == "" {
		return nil, fmt.Errorf("GITHUB_TOKEN environment variable is not set")
	}

	// The existing github.Client doesn't implement forge.Client yet.
	// This will need to be integrated in a future work package.
	return nil, fmt.Errorf("github client not yet integrated with forge.Client interface")
}

// RegisterGiteaClientFactory allows the gitea package to register its client factory.
// This avoids import cycles between forge and gitea packages.
func RegisterGiteaClientFactory(factory func(projectDir string) (Client, error)) {
	newGiteaClient = factory
}

// RegisterGitHubClientFactory allows integration with the existing github package.
func RegisterGitHubClientFactory(factory func() (Client, error)) {
	newGitHubClient = factory
}
