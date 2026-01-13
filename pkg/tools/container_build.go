package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"path/filepath"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/exec"
)

// ContainerBuildTool provides MCP interface for building Docker containers from Dockerfile.
// Uses local executor to run docker commands directly on the host.
type ContainerBuildTool struct {
	executor          exec.Executor
	hostWorkspacePath string // Host path that maps to /workspace inside containers
}

// NewContainerBuildTool creates a new container build tool instance.
// Uses local executor since docker commands run on the host, not inside containers.
// hostWorkspacePath is the host filesystem path that corresponds to /workspace inside containers.
// If empty, path translation is disabled (for backwards compatibility in tests).
func NewContainerBuildTool(hostWorkspacePath string) *ContainerBuildTool {
	return &ContainerBuildTool{
		executor:          exec.NewLocalExec(),
		hostWorkspacePath: hostWorkspacePath,
	}
}

// Definition returns the tool's definition in Claude API format.
func (c *ContainerBuildTool) Definition() ToolDefinition {
	return ToolDefinition{
		Name:        "container_build",
		Description: "Build Docker container from Dockerfile using buildx with proper validation and testing",
		InputSchema: InputSchema{
			Type: "object",
			Properties: map[string]Property{
				"cwd": {
					Type:        "string",
					Description: "Working directory containing Dockerfile (defaults to current directory)",
				},
				"container_name": {
					Type:        "string",
					Description: "Name to tag the built container (e.g., 'maestro-hello-dev')",
				},
				"dockerfile": {
					Type:        "string",
					Description: "Path to Dockerfile within .maestro/ directory (defaults to .maestro/Dockerfile)",
				},
				"platform": {
					Type:        "string",
					Description: "Target platform for multi-arch builds (e.g., 'linux/amd64', 'linux/arm64')",
				},
			},
			Required: []string{"container_name"},
		},
	}
}

// Name returns the tool identifier.
func (c *ContainerBuildTool) Name() string {
	return "container_build"
}

// PromptDocumentation returns markdown documentation for LLM prompts.
func (c *ContainerBuildTool) PromptDocumentation() string {
	return `- **container_build** - Build Docker container from Dockerfile using buildx
  - Parameters:
    - container_name (required): name to tag the built container
    - cwd (optional): working directory (project root)
    - dockerfile (optional): path within .maestro/ directory (defaults to .maestro/Dockerfile)
    - platform (optional): target platform for multi-arch builds
  - IMPORTANT: Dockerfile must be in .maestro/ directory to avoid conflicts with production Dockerfiles
  - If adapting an existing repo Dockerfile, copy it to .maestro/ first
  - Builds container using Docker buildx with validation and testing`
}

// translateToHostPath converts a container path (like /workspace) to the actual host path.
// This is necessary because MCP tools run on the host, not inside the container.
// Paths provided by agents running inside containers need to be translated.
func (c *ContainerBuildTool) translateToHostPath(containerPath string) string {
	// If no host workspace path configured, return path unchanged
	if c.hostWorkspacePath == "" {
		return containerPath
	}

	// Check if path starts with /workspace (the container's workspace mount point)
	if containerPath == DefaultWorkspaceDir {
		// Exact match: /workspace -> host path
		return c.hostWorkspacePath
	}

	// Check for paths under /workspace/...
	prefix := DefaultWorkspaceDir + "/"
	if len(containerPath) > len(prefix) && containerPath[:len(prefix)] == prefix {
		// Path like /workspace/Dockerfile -> hostPath/Dockerfile
		relativePath := containerPath[len(prefix):]
		return filepath.Join(c.hostWorkspacePath, relativePath)
	}

	// Not a container workspace path, return unchanged
	return containerPath
}

// Exec executes the container build operation.
//
//nolint:cyclop // Temporary debugging code increases complexity
func (c *ContainerBuildTool) Exec(ctx context.Context, args map[string]any) (*ExecResult, error) {
	// Extract working directory (may be container path like /workspace)
	cwd := extractWorkingDirectory(args)

	// Translate container path to host path if we have the mapping
	// When tools run on host via MCP server, /workspace doesn't exist - we need the host path
	cwd = c.translateToHostPath(cwd)

	// Extract container name
	containerName, ok := args["container_name"].(string)
	if !ok || containerName == "" {
		return nil, fmt.Errorf("container_name is required")
	}

	// Extract dockerfile path - use config default if not provided
	dockerfilePath := config.GetDockerfilePath()
	if path, ok := args["dockerfile"].(string); ok && path != "" {
		dockerfilePath = path
	}

	// Translate dockerfile path if it's an absolute container path (e.g., /workspace/.maestro/Dockerfile)
	// Must translate BEFORE validation so container paths are properly resolved to host paths
	dockerfilePath = c.translateToHostPath(dockerfilePath)

	// Validate translated path is within .maestro directory
	if !config.IsValidDockerfilePathWithRoot(cwd, dockerfilePath) {
		return nil, fmt.Errorf("dockerfile must be within .maestro/ directory (got: %s). "+
			"This prevents accidentally modifying production Dockerfiles", dockerfilePath)
	}

	// Extract platform
	platform := ""
	if p, ok := args["platform"].(string); ok && p != "" {
		platform = p
	}

	log.Printf("DEBUG container_build: cwd=%s, dockerfilePath=%s, containerName=%s, hostWorkspacePath=%s", cwd, dockerfilePath, containerName, c.hostWorkspacePath)

	// Skip dockerfile existence check - docker build will validate and provide clear error messages
	log.Printf("DEBUG container_build: skipping existence check, docker will validate dockerfile: %s", dockerfilePath)

	// Calculate the dockerfile path relative to cwd for docker command
	relDockerfilePath := dockerfilePath
	if filepath.IsAbs(dockerfilePath) {
		var err error
		relDockerfilePath, err = filepath.Rel(cwd, dockerfilePath)
		if err != nil {
			// If we can't make it relative, use absolute path
			relDockerfilePath = dockerfilePath
		}
	}

	return c.buildAndTestContainer(ctx, cwd, containerName, relDockerfilePath, platform)
}

// buildAndTestContainer builds and tests a container, returning the result map.
func (c *ContainerBuildTool) buildAndTestContainer(ctx context.Context, cwd, containerName, dockerfilePath, platform string) (*ExecResult, error) {
	// Build the container
	if err := c.buildContainer(ctx, cwd, containerName, dockerfilePath, platform); err != nil {
		// Return structured response with build failure details (error already includes stdout/stderr)
		response := map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"platform":       platform,
			"error":          fmt.Sprintf("Failed to build container: %v", err),
			"stage":          "build",
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	// Test the container
	if err := c.testContainer(ctx, containerName); err != nil {
		// Return structured response with test failure details (error already includes stdout/stderr)
		response := map[string]any{
			"success":        false,
			"container_name": containerName,
			"dockerfile":     dockerfilePath,
			"platform":       platform,
			"error":          fmt.Sprintf("Container built successfully but failed testing: %v", err),
			"stage":          "test",
		}
		content, marshalErr := json.Marshal(response)
		if marshalErr != nil {
			return nil, fmt.Errorf("failed to marshal error response: %w", marshalErr)
		}
		return &ExecResult{Content: string(content)}, nil
	}

	result := map[string]any{
		"success":        true,
		"container_name": containerName,
		"dockerfile":     dockerfilePath,
		"platform":       platform,
		"message":        fmt.Sprintf("Successfully built container '%s'", containerName),
	}

	content, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %w", err)
	}

	return &ExecResult{Content: string(content)}, nil
}

// buildContainer builds the Docker container from the specified dockerfile using buildx or docker build as fallback.
func (c *ContainerBuildTool) buildContainer(ctx context.Context, cwd, containerName, dockerfilePath, platform string) error {
	// Get config to check buildx availability
	cfg, err := config.GetConfig()
	if err != nil {
		log.Printf("WARNING: Failed to get config, defaulting to docker build: %v", err)
		return c.buildWithDockerBuild(ctx, cwd, containerName, dockerfilePath, platform)
	}

	// Check if multi-platform build is requested but buildx not available
	if platform != "" && (cfg.Container == nil || !cfg.Container.BuildxAvailable) {
		return fmt.Errorf("multi-platform builds require buildx, but buildx is not available on this host")
	}

	// Use buildx if available, otherwise fall back to docker build
	if cfg.Container != nil && cfg.Container.BuildxAvailable {
		return c.buildWithBuildx(ctx, cwd, containerName, dockerfilePath, platform)
	} else {
		log.Printf("INFO: Using docker build (buildx not available)")
		return c.buildWithDockerBuild(ctx, cwd, containerName, dockerfilePath, platform)
	}
}

// buildWithBuildx builds using docker buildx.
func (c *ContainerBuildTool) buildWithBuildx(ctx context.Context, cwd, containerName, dockerfilePath, platform string) error {
	args := []string{"docker", "buildx", "build", "-t", containerName, "-f", dockerfilePath}
	if platform != "" {
		args = append(args, "--platform", platform)
	}
	args = append(args, "--load", ".")

	opts := &exec.Opts{
		WorkDir: cwd,
		Timeout: 5 * time.Minute,
		// Note: Don't override DOCKER_CONFIG - it breaks buildx plugin discovery
	}

	result, err := c.executor.Run(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("docker buildx build failed: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("docker buildx build failed with exit code %d (stdout: %s, stderr: %s)", result.ExitCode, result.Stdout, result.Stderr)
	}
	return nil
}

// buildWithDockerBuild builds using legacy docker build with BuildKit enabled.
func (c *ContainerBuildTool) buildWithDockerBuild(ctx context.Context, cwd, containerName, dockerfilePath, _ string) error {
	args := []string{"docker", "build", "-t", containerName, "-f", dockerfilePath}
	// Note: --platform not supported in legacy docker build (parameter ignored)
	args = append(args, ".")

	opts := &exec.Opts{
		WorkDir: cwd,
		Timeout: 5 * time.Minute,
		// Enable BuildKit for legacy build (inherits current env, just adds DOCKER_BUILDKIT)
		Env: []string{"DOCKER_BUILDKIT=1"},
	}

	result, err := c.executor.Run(ctx, args, opts)
	if err != nil {
		return fmt.Errorf("docker build failed: %w (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
	}
	if result.ExitCode != 0 {
		return fmt.Errorf("docker build failed with exit code %d (stdout: %s, stderr: %s)", result.ExitCode, result.Stdout, result.Stderr)
	}
	return nil
}

// testContainer performs validation that the container has all required tools for Maestro operations.
func (c *ContainerBuildTool) testContainer(ctx context.Context, containerName string) error {
	// Use centralized validation helper with comprehensive checks
	validationResult := ValidateContainerCapabilities(ctx, c.executor, containerName)

	if !validationResult.Success {
		// Return detailed error with specific missing tools
		return fmt.Errorf("container validation failed: %s. Missing tools: %v. Details: %v",
			validationResult.Message, validationResult.MissingTools, validationResult.ErrorDetails)
	}

	return nil
}
