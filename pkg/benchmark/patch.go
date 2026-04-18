package benchmark

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// CollectPatch clones/fetches from the Gitea repo and produces a unified diff
// between the benchmark-base tag and the tip of origin/main.
// Returns the diff string (may be empty if no changes). Always attempts even on failure.
func CollectPatch(ctx context.Context, giteaCloneURL, workDir string) (string, error) {
	cloneDir := filepath.Join(workDir, "patch-collect")

	// Clone or fetch
	if _, err := os.Stat(filepath.Join(cloneDir, ".git")); os.IsNotExist(err) {
		cmd := exec.CommandContext(ctx, "git", "clone", "--quiet", giteaCloneURL, cloneDir)
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git clone for patch: %w\n%s", err, string(out))
		}
	} else {
		cmd := exec.CommandContext(ctx, "git", "-C", cloneDir, "fetch", "--quiet", "origin")
		if out, err := cmd.CombinedOutput(); err != nil {
			return "", fmt.Errorf("git fetch for patch: %w\n%s", err, string(out))
		}
	}

	// Produce diff: benchmark-base..origin/main
	cmd := exec.CommandContext(ctx, "git", "-C", cloneDir, "diff", "benchmark-base..origin/main")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git diff for patch: %w\n%s", err, string(out))
	}

	return strings.TrimSpace(string(out)), nil
}
