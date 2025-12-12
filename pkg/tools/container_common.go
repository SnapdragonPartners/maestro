package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
)

// IMPORTANT: Tool Error Handling Pattern
// All MCP tools should return structured responses with success/error details instead of (nil, error).
// When commands fail, return map[string]any with:
//   - "success": false
//   - "error": error message
//   - "stdout": command output (when available)
//   - "stderr": command errors (when available)
// This gives LLMs full context to understand failures and avoid repeating the same mistakes.
// Only return (nil, error) for parameter validation errors, not execution failures.

const (
	// DefaultDockerfile is the standard Dockerfile name.
	DefaultDockerfile = "Dockerfile"
	// DefaultWorkspaceDir is the standard workspace directory inside containers.
	DefaultWorkspaceDir = "/workspace"
)

// extractWorkingDirectory extracts and validates the working directory from args.
func extractWorkingDirectory(args map[string]any) string {
	cwd := ""
	if cwdVal, hasCwd := args["cwd"]; hasCwd {
		if cwdStr, ok := cwdVal.(string); ok {
			cwd = cwdStr
		}
	}

	if cwd == "" {
		// Default to configured workspace path - all agent operations run inside containers
		workspacePath, err := config.GetContainerWorkspacePath()
		if err != nil {
			// Fallback to standard workspace path if config not available
			cwd = DefaultWorkspaceDir
		} else {
			cwd = workspacePath
		}
	}

	return cwd
}

// ContainerValidationResult holds the results of container capability validation.
//
//nolint:govet // Field alignment is not critical for this validation struct
type ContainerValidationResult struct {
	ErrorDetails  map[string]string `json:"error_details"`  // Detailed error messages for each failed check
	MissingTools  []string          `json:"missing_tools"`  // List of missing required capabilities
	ContainerName string            `json:"container_name"` // Name of the validated container
	Message       string            `json:"message"`        // Human-readable summary message
	Success       bool              `json:"success"`        // Overall validation result
	GitAvailable  bool              `json:"git_available"`  // Whether git CLI is available
	UserUID1000   bool              `json:"user_uid_1000"`  // Whether user with UID 1000 exists
	TmpWritable   bool              `json:"tmp_writable"`   // Whether /tmp is writable
}

// ValidateContainerCapabilities validates that a container has all required capabilities for Maestro operations.
// Required: git (for version control), user with UID 1000 (for rootless execution with read-only filesystem),
// writable /tmp (for MCP proxy installation and temp files).
// Informational: gh CLI availability (not required - PR operations run on host).
// Returns detailed validation results with verbose error messages for LLM understanding.
func ValidateContainerCapabilities(ctx context.Context, executor exec.Executor, containerName string) *ContainerValidationResult {
	result := &ContainerValidationResult{
		ContainerName: containerName,
		ErrorDetails:  make(map[string]string),
	}

	missingTools := []string{}
	opts := &exec.Opts{
		Timeout: 30 * time.Second,
	}

	// Test 1: Check if git is available
	gitResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "git", "--version"}, opts)
	if err != nil || gitResult.ExitCode != 0 {
		result.GitAvailable = false
		missingTools = append(missingTools, "git")
		result.ErrorDetails["git"] = fmt.Sprintf("Git is not available in container '%s'. Error: %v. Stdout: %s, Stderr: %s. This is required for commit operations.",
			containerName, err, gitResult.Stdout, gitResult.Stderr)
	} else {
		result.GitAvailable = true
	}

	// Test 2: Check if user with UID 1000 exists (required for --user 1000:1000 with read-only filesystem)
	// Maestro runs containers with --user 1000:1000 --read-only, so the user must be pre-created in the Dockerfile
	uidResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "id", "-u", "1000"}, opts)
	if err != nil || uidResult.ExitCode != 0 {
		result.UserUID1000 = false
		missingTools = append(missingTools, "user-uid-1000")
		result.ErrorDetails["user_uid_1000"] = fmt.Sprintf(
			"Container '%s' does not have a user with UID 1000. Error: %v. Stdout: %s, Stderr: %s. "+
				"Maestro runs containers with '--user 1000:1000 --read-only', so the user MUST be pre-created in the Dockerfile. "+
				"Add this to your Dockerfile: 'RUN adduser -D -u 1000 coder || useradd -u 1000 -m coder'. "+
				"The user cannot be created at runtime because the container filesystem is read-only.",
			containerName, err, uidResult.Stdout, uidResult.Stderr)
	} else {
		result.UserUID1000 = true
	}

	// Test 3: Check if /tmp is writable (required for MCP proxy installation and temp files)
	// Even with --read-only, /tmp should be writable (Docker mounts it as tmpfs by default)
	tmpResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", "--user", "1000:1000", containerName,
		"sh", "-c", "touch /tmp/.maestro-test && rm /tmp/.maestro-test"}, opts)
	if err != nil || tmpResult.ExitCode != 0 {
		result.TmpWritable = false
		missingTools = append(missingTools, "tmp-writable")
		result.ErrorDetails["tmp_writable"] = fmt.Sprintf(
			"Container '%s' /tmp directory is not writable by UID 1000. Error: %v. Stdout: %s, Stderr: %s. "+
				"Maestro requires a writable /tmp for MCP proxy installation and temporary files. "+
				"Ensure /tmp is mounted as a writable volume or tmpfs in the container.",
			containerName, err, tmpResult.Stdout, tmpResult.Stderr)
	} else {
		result.TmpWritable = true
	}

	// Note: gh CLI validation not performed - PR operations run on host, not in container

	result.MissingTools = missingTools
	// Container is valid if it has required capabilities: git + user UID 1000 + writable /tmp
	// Note: gh CLI is NOT required in containers - PR operations run on host
	result.Success = len(missingTools) == 0

	// Generate verbose message for LLM
	if result.Success {
		result.Message = fmt.Sprintf("Container '%s' validation passed: git available, user UID 1000 exists, /tmp writable", containerName)
	} else {
		var issues []string
		if !result.GitAvailable {
			issues = append(issues, "git command not found - required for version control operations")
		}
		if !result.UserUID1000 {
			issues = append(issues, "user with UID 1000 not found - required for rootless container execution (add 'RUN adduser -D -u 1000 coder' to Dockerfile)")
		}
		if !result.TmpWritable {
			issues = append(issues, "/tmp not writable by UID 1000 - required for MCP proxy and temp files")
		}

		result.Message = fmt.Sprintf("Container '%s' validation failed: %s. This container cannot be used for Maestro operations until these issues are fixed.",
			containerName, strings.Join(issues, "; "))
	}

	return result
}

// Note: validateGitHubAPIAccess and extractRepoPath functions removed.
// GitHub API validation was running 'gh' inside the container, but PR operations
// actually run on the host via exec.CommandContext in pkg/github/client.go.
// Container validation now only checks for git and user UID 1000.
