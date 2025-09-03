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
	ErrorDetails   map[string]string `json:"error_details"`    // 8 bytes (pointer to map)
	MissingTools   []string          `json:"missing_tools"`    // 8 bytes (pointer to slice)
	ContainerName  string            `json:"container_name"`   // 16 bytes (string header)
	Message        string            `json:"message"`          // 16 bytes (string header)
	Success        bool              `json:"success"`          // 1 byte
	GitAvailable   bool              `json:"git_available"`    // 1 byte
	GHAvailable    bool              `json:"gh_available"`     // 1 byte
	GitHubAPIValid bool              `json:"github_api_valid"` // 1 byte + 4 bytes padding
}

// validateContainerCapabilities validates that a container has all required tools for Maestro operations.
// This includes git, GitHub CLI, and validates GitHub API connectivity.
// Returns detailed validation results with verbose error messages for LLM understanding.
func validateContainerCapabilities(ctx context.Context, executor exec.Executor, containerName string) *ContainerValidationResult {
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

	// Test 2: Check if GitHub CLI is available
	ghResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", containerName, "gh", "--version"}, opts)
	if err != nil || ghResult.ExitCode != 0 {
		result.GHAvailable = false
		missingTools = append(missingTools, "gh")
		result.ErrorDetails["gh"] = fmt.Sprintf("GitHub CLI (gh) is not available in container '%s'. Error: %v. Stdout: %s, Stderr: %s. This is required for GitHub authentication and pull request operations. Please ensure 'gh' is installed in your container.",
			containerName, err, ghResult.Stdout, ghResult.Stderr)
	} else {
		result.GHAvailable = true
	}

	// Test 3: Validate GitHub API connectivity (only if gh CLI is available)
	if result.GHAvailable {
		apiValid, apiError := validateGitHubAPIAccess(ctx, executor, containerName)
		result.GitHubAPIValid = apiValid
		if !apiValid {
			result.ErrorDetails["github_api"] = apiError
		}
	} else {
		result.GitHubAPIValid = false
		result.ErrorDetails["github_api"] = "Cannot test GitHub API access because GitHub CLI is not available"
	}

	result.MissingTools = missingTools
	result.Success = len(missingTools) == 0 && result.GitHubAPIValid

	// Generate verbose message for LLM
	if result.Success {
		result.Message = fmt.Sprintf("Container '%s' validation passed: git available, GitHub CLI available, GitHub API access validated", containerName)
	} else {
		var issues []string
		if !result.GitAvailable {
			issues = append(issues, "git command not found - required for version control operations")
		}
		if !result.GHAvailable {
			issues = append(issues, "GitHub CLI (gh) not found - required for authentication and PR operations")
		}
		if !result.GitHubAPIValid {
			issues = append(issues, "GitHub API access failed - check token permissions")
		}

		result.Message = fmt.Sprintf("Container '%s' validation failed: %s. This container cannot be used for Maestro operations until these tools are installed.",
			containerName, strings.Join(issues, ", "))
	}

	return result
}

// validateGitHubAPIAccess performs lightweight GitHub API validation using gh CLI.
// This replaces the problematic 'gh auth status' with scope-free API calls.
func validateGitHubAPIAccess(ctx context.Context, executor exec.Executor, containerName string) (bool, string) {
	// Get repository info from config for API validation
	cfg, err := config.GetConfig()
	if err != nil {
		return false, fmt.Sprintf("Failed to get config for GitHub API validation: %v", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return false, "No repository URL configured - cannot validate GitHub API access"
	}

	// Extract owner/repo from URL for API validation
	repoPath := extractRepoPath(cfg.Git.RepoURL)
	if repoPath == "" {
		return false, fmt.Sprintf("Cannot extract repository path from URL: %s", cfg.Git.RepoURL)
	}

	opts := &exec.Opts{
		Timeout: 30 * time.Second,
		Env:     []string{"GITHUB_TOKEN"}, // Pass through GITHUB_TOKEN
	}

	// Test 1: Validate token with /user endpoint
	userResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", "-e", "GITHUB_TOKEN", containerName, "gh", "api", "/user"}, opts)
	if err != nil || userResult.ExitCode != 0 {
		return false, fmt.Sprintf("GitHub API /user validation failed. Error: %v. Stdout: %s, Stderr: %s. This indicates the GITHUB_TOKEN is invalid or GitHub API is unreachable.",
			err, userResult.Stdout, userResult.Stderr)
	}

	// Test 2: Validate repository access
	repoResult, err := executor.Run(ctx, []string{"docker", "run", "--rm", "-e", "GITHUB_TOKEN", containerName, "gh", "api", fmt.Sprintf("/repos/%s", repoPath)}, opts)
	if err != nil || repoResult.ExitCode != 0 {
		return false, fmt.Sprintf("GitHub API repository access validation failed for %s. Error: %v. Stdout: %s, Stderr: %s. This indicates the token lacks repository access permissions.",
			repoPath, err, repoResult.Stdout, repoResult.Stderr)
	}

	return true, ""
}

// extractRepoPath extracts owner/repo from a GitHub URL.
// Supports both HTTPS and SSH formats.
func extractRepoPath(repoURL string) string {
	// Remove .git suffix if present
	url := strings.TrimSuffix(repoURL, ".git")

	// Handle HTTPS URLs: https://github.com/owner/repo
	if strings.HasPrefix(url, "https://github.com/") {
		path := strings.TrimPrefix(url, "https://github.com/")
		if strings.Count(path, "/") >= 1 {
			return path
		}
	}

	// Handle SSH URLs: git@github.com:owner/repo
	if strings.HasPrefix(url, "git@github.com:") {
		path := strings.TrimPrefix(url, "git@github.com:")
		if strings.Count(path, "/") >= 1 {
			return path
		}
	}

	return ""
}
