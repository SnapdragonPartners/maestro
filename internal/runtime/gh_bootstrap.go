// Package runtime provides container runtime operations for the orchestrator.
package runtime

import (
	"context"
	"fmt"
)

// Docker interface defines the Docker operations needed for GitHub bootstrap.
type Docker interface {
	Exec(ctx context.Context, cid string, args ...string) ([]byte, error)
	CpToContainer(ctx context.Context, cid string, dstPath string, data []byte, mode int) error
}

// InstallAndRunGHInit installs and executes the GitHub authentication script in a container.
// This function handles the complete GitHub auth setup process:
// 1. Installs the gh-init script into the container
// 2. Executes the script with the provided repository URL.
//
// The script will:
// - Configure ephemeral GitHub CLI authentication using GITHUB_TOKEN
// - Set up git credentials for HTTPS operations
// - Validate the setup by testing repository access (if repoURL provided).
func InstallAndRunGHInit(ctx context.Context, d Docker, cid, repoURL string, script []byte) error {
	fmt.Printf("🔧 InstallAndRunGHInit: Installing script to container %s (script size: %d bytes)\n", cid, len(script))

	// Install script to a user-writable location
	if err := d.CpToContainer(ctx, cid, "/tmp/gh-init", script, 0o755); err != nil {
		fmt.Printf("❌ InstallAndRunGHInit: Failed to copy script: %v\n", err)
		return fmt.Errorf("install gh-init: %w", err)
	}
	fmt.Printf("✅ InstallAndRunGHInit: Script copied to /tmp/gh-init\n")

	// Run the script with repository URL environment variable
	cmd := []string{"sh", "-lc", fmt.Sprintf(`REPO_URL=%q /tmp/gh-init`, repoURL)}
	fmt.Printf("🚀 InstallAndRunGHInit: Running command: %v\n", cmd)

	output, err := d.Exec(ctx, cid, cmd...)
	if err != nil {
		fmt.Printf("❌ InstallAndRunGHInit: Script execution failed: %v\n", err)
		fmt.Printf("📄 InstallAndRunGHInit: Script output: %s\n", string(output))
		return fmt.Errorf("run gh-init: %w", err)
	}

	fmt.Printf("✅ InstallAndRunGHInit: Script completed successfully\n")
	fmt.Printf("📄 InstallAndRunGHInit: Script output: %s\n", string(output))
	return nil
}
