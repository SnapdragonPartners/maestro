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
	// Use getent passwd 1000 which works on both Alpine (BusyBox) and Debian/Ubuntu
	uidResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "getent", "passwd", "1000"}, opts)
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

// Reserved container name prefix. This name is reserved for the embedded bootstrap container
// built from the internal Dockerfile. Project containers must not use this name.
const reservedContainerNamePrefix = "maestro-bootstrap"

// GenerateContainerName creates a standardized container name from project name and dockerfile path.
// Format: maestro-<projectname>-<dockerfile>:latest
// Examples:
//   - project "myapp", dockerfile ".maestro/Dockerfile" -> "maestro-myapp-dockerfile:latest"
//   - project "myapp", dockerfile ".maestro/Dockerfile.gpu" -> "maestro-myapp-dockerfile-gpu:latest"
//   - project "myapp", dockerfile ".maestro/Dockerfile-dev" -> "maestro-myapp-dockerfile-dev:latest"
func GenerateContainerName(projectName, dockerfilePath string) string {
	// Sanitize project name: lowercase, replace non-alphanumeric with dash
	sanitizedProject := sanitizeForContainerName(projectName)
	if sanitizedProject == "" {
		sanitizedProject = "project"
	}

	// Extract dockerfile identifier from path
	dockerfileID := extractDockerfileIdentifier(dockerfilePath)

	return fmt.Sprintf("maestro-%s-%s:latest", sanitizedProject, dockerfileID)
}

// sanitizeForContainerName converts a string to be valid for container names.
// Docker container names must match: [a-zA-Z0-9][a-zA-Z0-9_.-]*
// We enforce lowercase and use dashes for consistency.
func sanitizeForContainerName(s string) string {
	s = strings.ToLower(s)
	var result strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			result.WriteRune(r)
		} else if r == '-' || r == '_' || r == '.' || r == ' ' {
			// Convert spaces and underscores to dashes
			result.WriteRune('-')
		}
		// Skip other characters
	}

	// Remove leading/trailing dashes and collapse multiple dashes
	out := result.String()
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	out = strings.Trim(out, "-")

	return out
}

// extractDockerfileIdentifier extracts an identifier from a dockerfile path.
// Examples:
//   - ".maestro/Dockerfile" -> "dockerfile"
//   - ".maestro/Dockerfile.gpu" -> "dockerfile-gpu"
//   - ".maestro/Dockerfile-dev" -> "dockerfile-dev"
//   - "/workspace/.maestro/Dockerfile.test" -> "dockerfile-test"
func extractDockerfileIdentifier(dockerfilePath string) string {
	// Get just the filename
	filename := dockerfilePath
	if idx := strings.LastIndex(dockerfilePath, "/"); idx >= 0 {
		filename = dockerfilePath[idx+1:]
	}

	// Default if empty
	if filename == "" {
		return "dockerfile"
	}

	// Convert to lowercase
	filename = strings.ToLower(filename)

	// Handle variations: Dockerfile, Dockerfile.gpu, Dockerfile-dev
	// Remove "dockerfile" prefix if present
	if strings.HasPrefix(filename, "dockerfile") {
		suffix := filename[len("dockerfile"):]
		if suffix == "" {
			return "dockerfile"
		}
		// Replace . with - for consistency
		suffix = strings.ReplaceAll(suffix, ".", "-")
		// Ensure it starts with a dash
		if !strings.HasPrefix(suffix, "-") {
			suffix = "-" + suffix
		}
		return "dockerfile" + suffix
	}

	// Not a standard dockerfile name, just sanitize it
	return sanitizeForContainerName(filename)
}

// IsReservedContainerName checks if a container name is reserved (e.g., maestro-bootstrap:latest).
// Returns true if the name is reserved and should not be used for project containers.
// The maestro-bootstrap name is reserved for the safe fallback container that is built
// from the embedded Dockerfile and should never be overwritten by project-specific containers.
func IsReservedContainerName(containerName string) bool {
	// Extract base name (before any tag)
	baseName := containerName
	if idx := strings.Index(containerName, ":"); idx > 0 {
		baseName = containerName[:idx]
	}

	// Check if the base name matches the reserved prefix
	return baseName == reservedContainerNamePrefix
}

// ReservedContainerNameError is returned when attempting to use a reserved container name.
type ReservedContainerNameError struct {
	ContainerName string
}

func (e *ReservedContainerNameError) Error() string {
	return fmt.Sprintf("container name '%s' is reserved for system use. "+
		"The '%s' container is the safe fallback environment and cannot be overwritten. "+
		"Please use a different name (e.g., 'maestro-<projectname>-dev')",
		e.ContainerName, reservedContainerNamePrefix)
}
