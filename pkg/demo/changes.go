package demo

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// ChangeType represents the type of changes detected between commits.
type ChangeType int

const (
	// NoChange indicates no changes between commits.
	NoChange ChangeType = iota
	// CodeOnly indicates only code files changed (restart sufficient).
	CodeOnly
	// DockerfileChanged indicates Dockerfile was modified (rebuild required).
	DockerfileChanged
	// ComposeChanged indicates compose file was modified (rebuild + services restart required).
	ComposeChanged
)

// String returns a human-readable representation of the change type.
func (c ChangeType) String() string {
	switch c {
	case NoChange:
		return "no_change"
	case CodeOnly:
		return "code_only"
	case DockerfileChanged:
		return "dockerfile_changed"
	case ComposeChanged:
		return "compose_changed"
	default:
		return "unknown"
	}
}

// DetectChanges analyzes what changed between two commits.
// It returns the most significant change type found.
func DetectChanges(ctx context.Context, workspacePath, fromSHA, toSHA string) (ChangeType, error) {
	if fromSHA == "" || toSHA == "" {
		return NoChange, fmt.Errorf("both fromSHA and toSHA must be provided")
	}

	if fromSHA == toSHA {
		return NoChange, nil
	}

	// Get list of changed files between commits
	changedFiles, err := getChangedFiles(ctx, workspacePath, fromSHA, toSHA)
	if err != nil {
		return NoChange, fmt.Errorf("failed to get changed files: %w", err)
	}

	if len(changedFiles) == 0 {
		return NoChange, nil
	}

	// Check for compose file changes (highest priority)
	for _, file := range changedFiles {
		if isComposeFile(file) {
			return ComposeChanged, nil
		}
	}

	// Check for Dockerfile changes
	for _, file := range changedFiles {
		if isDockerfile(file) {
			return DockerfileChanged, nil
		}
	}

	// All other changes are code-only
	return CodeOnly, nil
}

// GetChangeRecommendation returns a human-readable recommendation based on change type.
func GetChangeRecommendation(changeType ChangeType) string {
	switch changeType {
	case NoChange:
		return "No changes detected. Demo is up to date."
	case CodeOnly:
		return "Code changes detected. Restart the demo to apply changes."
	case DockerfileChanged:
		return "Dockerfile changed. Rebuild required to apply changes."
	case ComposeChanged:
		return "Compose file changed. Rebuild required to apply service configuration changes."
	default:
		return "Unknown change type."
	}
}

// NeedsRebuild returns true if the change type requires a full rebuild.
func NeedsRebuild(changeType ChangeType) bool {
	return changeType == DockerfileChanged || changeType == ComposeChanged
}

// NeedsRestart returns true if the change type requires at least a restart.
func NeedsRestart(changeType ChangeType) bool {
	return changeType != NoChange
}

// getChangedFiles returns the list of files changed between two commits.
func getChangedFiles(ctx context.Context, workspacePath, fromSHA, toSHA string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "diff", "--name-only", fromSHA, toSHA)
	cmd.Dir = workspacePath

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git diff failed: %w (stderr: %s)", err, stderr.String())
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return nil, nil
	}

	files := strings.Split(output, "\n")
	return files, nil
}

// isComposeFile checks if a filename is a Docker Compose file.
func isComposeFile(filename string) bool {
	baseName := getBaseName(filename)
	composeNames := []string{
		"compose.yml",
		"compose.yaml",
		"docker-compose.yml",
		"docker-compose.yaml",
	}

	for _, name := range composeNames {
		if baseName == name {
			return true
		}
	}

	return false
}

// isDockerfile checks if a filename is a Dockerfile.
func isDockerfile(filename string) bool {
	baseName := getBaseName(filename)
	return baseName == "Dockerfile" || strings.HasPrefix(baseName, "Dockerfile.")
}

// getBaseName extracts the filename from a path.
func getBaseName(path string) string {
	idx := strings.LastIndex(path, "/")
	if idx == -1 {
		return path
	}
	return path[idx+1:]
}
