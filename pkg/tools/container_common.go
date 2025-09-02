package tools

import "orchestrator/pkg/config"

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
