package tools

import (
	"context"
	"fmt"
	"os"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// ContainerTestTool provides unified MCP interface for container testing with optional TTL and command execution.
type ContainerTestTool struct {
	executor   exec.Executor
	hostRunner *HostRunner // For host execution strategy
	agent      Agent       // Required agent reference for state access and workDir
	workDir    string      // Agent working directory for host mounts
}

// NewContainerTestTool creates a new container test tool instance with required agent context.
func NewContainerTestTool(executor exec.Executor, agent Agent, workDir string) *ContainerTestTool {
	return &ContainerTestTool{
		executor:   executor,
		agent:      agent,
		workDir:    workDir,
		hostRunner: NewHostRunner(),
	}
}

// Definition returns the tool's definition in Claude API format.
func (c *ContainerTestTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_test",
		Description: "Unified container testing tool - boot test, command execution, or long-running containers with TTL",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"container_name": {
					Type:        "string",
					Description: "Name of container to test",
				},
				"command": {
					Type:        "string",
					Description: "Command to execute in container (optional, if not provided does boot test)",
				},
				"ttl_seconds": {
					Type:        "integer",
					Description: "Time-to-live for container in seconds (0=boot test, >0=persistent container, default: 0)",
				},
				"working_dir": {
					Type:        "string",
					Description: "Working directory inside container",
				},
				"env_vars": {
					Type:        "object",
					Description: "Environment variables to set in container",
				},
				"volumes": {
					Type:        "array",
					Description: "Volume mounts in format 'host_path:container_path'",
				},
				"timeout_seconds": {
					Type:        "integer",
					Description: "Maximum seconds to wait for command completion (default: 60, max: 300)",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerTestTool) Name() string {
	return "container_test"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerTestTool) PromptDocumentation() string {
	return `- **container_test** - Run target (or safe) image in throwaway container to validate environment and/or run tests
  - Purpose: Test container functionality without modifying active container
  - Parameters:
    - container_name (required): name of container to test
    - command (optional): command to execute in container (if not provided, does boot test)
    - ttl_seconds (optional): time-to-live for container (0=boot test, >0=persistent container, default: 0)
    - working_dir (optional): working directory inside container (default: /workspace)
    - env_vars (optional): environment variables to set
    - volumes (optional): additional volume mounts (workspace auto-mounted)
    - timeout_seconds (optional): max seconds to wait for command completion (default: 60, max: 300)
  - Mount Policy:
    - CODING: /workspace mounted READ-WRITE; /tmp writable
    - All other states: /workspace mounted READ-ONLY; /tmp writable
    - Polyglot: Don't assume language; tests may cache under /tmp
  - Behavior: May compile/run tests but must not modify sources except in CODING mode
  - Returns: { "status": "pass" | "fail", "details": { "image_id", "role": "target|safe", ... } }
  - Note: This tool NEVER changes the active container`
}

// Exec executes the unified container test tool using host execution strategy.
func (c *ContainerTestTool) Exec(ctx context.Context, args map[string]any) (any, error) {
	// Extract container name
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Strict validation - agent context is required
	if c.agent == nil {
		return nil, fmt.Errorf("container_test requires agent context for proper workDir mounting - no agent provided")
	}

	// Get host workspace path from agent
	hostWorkspacePath := c.agent.GetHostWorkspacePath()
	if hostWorkspacePath == "" {
		return nil, fmt.Errorf("agent returned empty host workspace path - cannot mount workspace")
	}

	// Validate that the workspace directory exists and is accessible
	if stat, err := os.Stat(hostWorkspacePath); err != nil {
		return nil, fmt.Errorf("host workspace path '%s' is not accessible: %w", hostWorkspacePath, err)
	} else if !stat.IsDir() {
		return nil, fmt.Errorf("host workspace path '%s' is not a directory", hostWorkspacePath)
	}

	args["host_workspace_path"] = hostWorkspacePath
	fmt.Printf("🗂️  ContainerTestTool: Validated workspace path: %s\n", hostWorkspacePath)

	// Add mount permissions based on agent state
	currentState := c.agent.GetCurrentState()
	if currentState == proto.State("CODING") {
		args["mount_permissions"] = "rw"
	} else {
		args["mount_permissions"] = "ro"
	}
	fmt.Printf("🗂️  ContainerTestTool: Agent state=%s, mount_permissions=%s\n", currentState, args["mount_permissions"])

	// Use host execution strategy for all container_test operations
	return c.hostRunner.RunContainerTest(ctx, args)
}

// buildDockerArgs builds docker run command arguments based on parameters.
// Automatically mounts workspace with appropriate permissions based on agent state.
func (c *ContainerTestTool) buildDockerArgs(args map[string]any, containerName, command string, detached bool) []string {
	dockerArgs := []string{"docker", "run"}

	if detached {
		dockerArgs = append(dockerArgs, "-d") // Detached mode for persistent containers
	} else {
		dockerArgs = append(dockerArgs, "--rm") // Remove after execution
	}

	// Add tmpfs for writable /tmp (always writable)
	dockerArgs = append(dockerArgs, "--tmpfs", fmt.Sprintf("/tmp:rw,noexec,nosuid,size=%s", config.GetContainerTmpfsSize()))

	// Automatically mount workspace based on agent state
	workspaceMount := c.getWorkspaceMount()
	if workspaceMount != "" {
		dockerArgs = append(dockerArgs, "-v", workspaceMount)
	}

	// Set default working directory to /workspace
	workingDir := utils.GetMapFieldOr(args, "working_dir", DefaultWorkspaceDir)
	if workingDir != "" {
		dockerArgs = append(dockerArgs, "-w", workingDir)
	}

	// Add environment variables
	if envVarsRaw, exists := args["env_vars"]; exists {
		if envVars, ok := envVarsRaw.(map[string]any); ok {
			for key, value := range envVars {
				if strValue, ok := value.(string); ok {
					dockerArgs = append(dockerArgs, "-e", fmt.Sprintf("%s=%s", key, strValue))
				}
			}
		}
	}

	// Add additional volume mounts (in addition to automatic workspace mount)
	if volumesRaw, exists := args["volumes"]; exists {
		if volumes, ok := volumesRaw.([]any); ok {
			for _, volume := range volumes {
				if volumeStr, ok := volume.(string); ok {
					dockerArgs = append(dockerArgs, "-v", volumeStr)
				}
			}
		}
	}

	// Add container name
	dockerArgs = append(dockerArgs, containerName)

	// Add command if specified
	if command != "" {
		dockerArgs = append(dockerArgs, "sh", "-c", command)
	}

	return dockerArgs
}

// getWorkspaceMount returns the appropriate workspace mount string based on agent state.
// Implements mount policy: PLANNING=RO, CODING=RW, /tmp always writable.
func (c *ContainerTestTool) getWorkspaceMount() string {
	// Get the host workspace path from the agent
	var hostWorkspacePath string
	if c.agent != nil {
		hostWorkspacePath = c.agent.GetHostWorkspacePath()
	}

	// Fallback to workDir if agent doesn't provide host path
	if hostWorkspacePath == "" {
		hostWorkspacePath = c.workDir
	}

	// Final fallback to current directory
	if hostWorkspacePath == "" {
		hostWorkspacePath = "."
	}

	// Determine mount permissions based on agent state
	permissions := c.getWorkspacePermissions()

	// Return workspace mount: host_path:/workspace:permissions
	return fmt.Sprintf("%s:/workspace:%s", hostWorkspacePath, permissions)
}

// getWorkspacePermissions determines workspace mount permissions based on agent state.
// Returns "rw" only for CODING state, "ro" for all other states (safer default).
func (c *ContainerTestTool) getWorkspacePermissions() string {
	// Try to get current agent state
	// This would need to be enhanced to access the actual agent context
	currentState := c.getCurrentAgentState()

	// Only CODING state gets read-write access, all others are read-only for security
	if currentState == proto.State("CODING") {
		return "rw"
	}

	// All other states (PLANNING, TESTING, SETUP, CODE_REVIEW, etc.) get read-only
	return "ro"
}

// getCurrentAgentState gets the current agent state if available.
func (c *ContainerTestTool) getCurrentAgentState() proto.State {
	if c.agent != nil {
		return c.agent.GetCurrentState()
	}

	// If no agent reference is available, default to empty state (read-only)
	return proto.State("")
}
