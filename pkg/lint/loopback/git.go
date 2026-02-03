package loopback

import (
	"fmt"
	"os/exec"
	"strings"
)

// GetBranchChangedFiles returns files changed in the current branch vs origin/main.
// Returns relative paths from the workspace root.
func GetBranchChangedFiles(workspacePath string) ([]string, error) {
	// Use git diff to get files changed in the branch
	// --name-only: only show file names
	// origin/main...HEAD: changes between origin/main and current HEAD (branch changes)
	cmd := exec.Command("git", "diff", "--name-only", "origin/main...HEAD")
	cmd.Dir = workspacePath

	output, err := cmd.Output()
	if err != nil {
		// If origin/main doesn't exist, try main
		cmd = exec.Command("git", "diff", "--name-only", "main...HEAD")
		cmd.Dir = workspacePath
		output, err = cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("git diff failed: %w", err)
		}
	}

	// Parse output into file list
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var files []string
	for _, line := range lines {
		if line != "" {
			files = append(files, line)
		}
	}

	return files, nil
}

// HasEnvOrComposeChanges checks if any .env or compose files changed in the branch.
// This is a quick check to determine if full scanning is needed.
func HasEnvOrComposeChanges(workspacePath string) (bool, error) {
	files, err := GetBranchChangedFiles(workspacePath)
	if err != nil {
		return false, err
	}

	linter := NewLinter(workspacePath)
	for _, f := range files {
		if linter.isEnvFile(f) || linter.isComposeFile(f) {
			return true, nil
		}
	}

	return false, nil
}
