package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
	"orchestrator/pkg/utils"
)

// ContainerUpdateTool provides MCP interface for updating container configuration.
// Uses local executor to run docker commands directly on the host.
type ContainerUpdateTool struct {
	executor exec.Executor
	agent    Agent // Agent reference for storing pending config in state
}

// NewContainerUpdateTool creates a new container update tool instance.
// Uses local executor since docker commands run on the host, not inside containers.
func NewContainerUpdateTool(agent Agent) *ContainerUpdateTool {
	return &ContainerUpdateTool{executor: exec.NewLocalExec(), agent: agent}
}

// Definition returns the tool's definition in Claude API format.
func (c *ContainerUpdateTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_update",
		Description: "Update project configuration with container settings - automatically gets image ID from Docker. Container name is auto-generated from project config if not specified.",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"container_name": {
					Type:        "string",
					Description: "Optional: Name of the container to register. If not provided, auto-generates as 'maestro-<projectname>-<dockerfile>:latest'",
				},
				"dockerfile": {
					Type:        "string",
					Description: "Path to Dockerfile within .maestro/ directory (defaults to .maestro/Dockerfile)",
				},
			},
			Required: []string{}, // No required parameters - container_name is auto-generated
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerUpdateTool) Name() string {
	return "container_update"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerUpdateTool) PromptDocumentation() string {
	return `- **container_update** - Register container for use after merge
  - Parameters:
    - container_name (optional): name of container to register - auto-generates as 'maestro-<projectname>-<dockerfile>:latest' if not provided
    - dockerfile (optional): path within .maestro/ directory (defaults to .maestro/Dockerfile)
  - IMPORTANT: Dockerfile must be in .maestro/ directory to avoid conflicts with production Dockerfiles
  - IMPORTANT: 'maestro-bootstrap' is a reserved name and cannot be used for project containers
  - Validates container capabilities before registering
  - Automatically gets and pins the current image ID from Docker
  - Configuration is applied AFTER merge is successful (not immediately)
  - Does NOT change active container; use container_switch to activate during story`
}

// Exec executes the container configuration update operation.
func (c *ContainerUpdateTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract and validate dockerfile path first - needed for name generation
	// NOTE: Dockerfile path is stored in pending config and applied after merge,
	// not written to config immediately (follows the deferred-apply contract)
	dockerfilePath := config.GetDockerfilePath()
	if path := utils.GetMapFieldOr(args, "dockerfile", ""); path != "" {
		// Normalize absolute container paths (e.g., /workspace/.maestro/Dockerfile -> .maestro/Dockerfile)
		normalizedPath := normalizeDockerfilePath(path)
		// Validate normalized path is within .maestro directory
		if !config.IsValidDockerfilePath(normalizedPath) {
			return nil, fmt.Errorf("dockerfile must be within .maestro/ directory (got: %s)", path)
		}
		dockerfilePath = normalizedPath
	}

	// Extract or auto-generate container name
	containerName := utils.GetMapFieldOr(args, "container_name", "")
	if containerName == "" {
		// Auto-generate from project config and dockerfile
		cfg, err := config.GetConfig()
		if err != nil {
			return nil, fmt.Errorf("container_name not provided and failed to get config for auto-generation: %w", err)
		}
		projectName := cfg.Project.Name
		if projectName == "" {
			return nil, fmt.Errorf("container_name not provided and project name not configured - either provide container_name or configure project name")
		}
		containerName = GenerateContainerName(projectName, dockerfilePath)
		log.Printf("INFO container_update: Auto-generated container name: %s (project: %s, dockerfile: %s)",
			containerName, projectName, dockerfilePath)
	}

	// SECURITY: Reject reserved container names to prevent overwriting the bootstrap container
	if IsReservedContainerName(containerName) {
		return nil, &ReservedContainerNameError{ContainerName: containerName}
	}

	return c.updateContainerConfiguration(ctx, containerName, dockerfilePath)
}

// updateContainerConfiguration validates container and stores pending config for post-merge application.
func (c *ContainerUpdateTool) updateContainerConfiguration(ctx context.Context, containerName, dockerfilePath string) (*ExecResult, error) {
	log.Printf("DEBUG container_update: containerName=%s, dockerfilePath=%s", containerName, dockerfilePath)

	// Validate container capabilities before registering
	// Uses the tool's local executor for docker commands
	validationResult := ValidateContainerCapabilities(ctx, c.executor, containerName)

	if !validationResult.Success {
		response := map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"validation":     validationResult,
			"error": fmt.Sprintf("Container validation failed: %s. Cannot register container that lacks required tools: %v",
				validationResult.Message, validationResult.MissingTools),
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Get the current image ID for the container to pin it
	imageID, err := c.getContainerImageID(ctx, containerName)
	if err != nil {
		response := map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"error":          fmt.Sprintf("Failed to get image ID for container '%s': %v", containerName, err),
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Store pending configuration in agent state (will be applied after successful merge)
	if c.agent != nil {
		c.agent.SetPendingContainerConfig(containerName, dockerfilePath, imageID)
		log.Printf("INFO container_update: Stored pending container config in agent state: %s, dockerfile: %s, pinned image: %s (will apply after merge)",
			containerName, dockerfilePath, imageID)
	} else {
		log.Printf("WARN container_update: No agent reference - pending config will not be stored")
	}

	result := map[string]any{
		"success":         true,
		"container_name":  containerName,
		"dockerfile":      dockerfilePath,
		"pinned_image_id": imageID,
		"pending":         true,
		"message": fmt.Sprintf("Container '%s' registered with dockerfile '%s' and image ID '%s'. "+
			"Configuration will be applied after PR is merged.", containerName, dockerfilePath, imageID),
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// getContainerImageID gets the image ID for a given container/image name.
func (c *ContainerUpdateTool) getContainerImageID(ctx context.Context, containerName string) (string, error) {
	// Use docker inspect to get the image ID
	result, err := c.executor.Run(ctx, []string{"docker", "inspect", "--format={{.Id}}", containerName}, &exec.Opts{
		Timeout: 30 * time.Second,
	})

	if err != nil {
		return "", fmt.Errorf("failed to inspect container/image '%s': %w (stdout: %s, stderr: %s)",
			containerName, err, result.Stdout, result.Stderr)
	}

	if result.ExitCode != 0 {
		return "", fmt.Errorf("docker inspect failed for '%s' with exit code %d (stdout: %s, stderr: %s)",
			containerName, result.ExitCode, result.Stdout, result.Stderr)
	}

	imageID := strings.TrimSpace(result.Stdout)
	if imageID == "" {
		return "", fmt.Errorf("empty image ID returned for container/image '%s'", containerName)
	}

	log.Printf("DEBUG: Retrieved image ID %s for container %s", imageID, containerName)
	return imageID, nil
}

// normalizeDockerfilePath converts absolute container paths to relative paths.
// For example, /workspace/.maestro/Dockerfile -> .maestro/Dockerfile
// This allows callers to pass absolute paths while storing relative paths in config.
func normalizeDockerfilePath(path string) string {
	// If already relative, return as-is
	if !strings.HasPrefix(path, "/") {
		return path
	}

	// Look for .maestro/ in the path and extract from there
	maestroMarker := "/" + config.MaestroDockerfileDir + "/"
	if idx := strings.Index(path, maestroMarker); idx >= 0 {
		// Return the path starting from .maestro/
		return path[idx+1:] // Skip the leading /
	}

	// No .maestro/ found, return original path (will fail validation)
	return path
}
