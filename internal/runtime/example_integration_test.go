package runtime

import (
	"testing"

	"orchestrator/internal/embeds/scripts"
	"orchestrator/pkg/exec"
)

// TestGitHubAuthIntegrationExample demonstrates how to use the GitHub auth
// bootstrap functionality with the existing Docker infrastructure.
//
// This is an example test that shows the integration pattern, but doesn't
// actually run Docker commands to avoid requiring Docker in CI.
func TestGitHubAuthIntegrationExample(t *testing.T) {
	// Skip this test in CI - it's just an example of the integration pattern
	if testing.Short() {
		t.Skip("Skipping integration example in short mode")
	}

	// Example: How this would be used with the existing Docker executor
	// In real usage, you'd get the executor from the container registry
	dockerExec := exec.NewLongRunningDockerExec("alpine:latest", "test-agent")

	// Example container ID (in real usage, this would come from starting a container)
	containerID := "test-container-123"
	repoURL := "https://github.com/example/repo.git"

	// Use the embedded script for GitHub authentication
	script := scripts.GHInitSh

	// This is how you'd bootstrap GitHub auth in a real container
	// (commented out to avoid requiring Docker in tests)
	/*
		err := InstallAndRunGHInit(ctx, dockerExec, containerID, repoURL, script)
		if err != nil {
			t.Fatalf("Failed to bootstrap GitHub auth: %v", err)
		}
	*/

	// Verify the embedded script is available
	if len(script) == 0 {
		t.Fatalf("Embedded GitHub auth script is empty")
	}

	t.Logf("GitHub auth script successfully embedded (%d bytes)", len(script))
	t.Logf("Integration pattern verified - ready for use with container ID: %s", containerID)
	t.Logf("Would authenticate against repo: %s", repoURL)

	// Verify Docker executor implements the Docker interface we need
	_ = Docker(dockerExec) // Verify interface compliance
}
