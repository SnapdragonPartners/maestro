package coder

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/agent"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// handleSetup processes the SETUP state
//
//nolint:unparam // bool return required by state machine interface, always false for non-terminal states
func (c *Coder) handleSetup(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	if c.cloneManager == nil {
		c.logger.Warn("No clone manager configured, skipping Git clone setup")
		return StatePlanning, false, nil
	}

	// Get story ID from state data
	storyID, exists := sm.GetStateValue(KeyStoryID)
	if !exists {
		return proto.StateError, false, logx.Errorf("no story_id found in state data during SETUP")
	}

	storyIDStr, ok := storyID.(string)
	if !ok {
		return proto.StateError, false, logx.Errorf("story_id is not a string in SETUP state: %v (type: %T)", storyID, storyID)
	}

	// Setup workspace with lightweight clone
	agentID := c.BaseStateMachine.GetAgentID()
	// Make agent ID filesystem-safe using shared sanitization helper
	fsafeAgentID := utils.SanitizeIdentifier(agentID)
	cloneResult, err := c.cloneManager.SetupWorkspace(ctx, fsafeAgentID, storyIDStr, c.workDir)
	if err != nil {
		c.logger.Error("Failed to setup workspace: %v", err)
		return proto.StateError, false, logx.Wrap(err, "workspace setup failed")
	}

	// Store clone path and branch names for subsequent states
	sm.SetStateData(KeyWorkspacePath, cloneResult.WorkDir)
	sm.SetStateData(KeyLocalBranchName, cloneResult.BranchName)
	sm.SetStateData(KeyRemoteBranchName, cloneResult.BranchName) // Initially same as local

	// Update coder's working directory to use agent work directory
	// This ensures all subsequent operations (MCP tools, testing, etc.) happen in the right place
	c.workDir = cloneResult.WorkDir
	c.logger.Info("Workspace setup complete: %s", cloneResult.WorkDir)
	c.logger.Debug("Updated coder working directory to: %s", c.workDir)
	c.logger.Debug("Coder instance pointer: %p, workDir: %s", c, c.workDir)

	// Configure container with read-only workspace for planning phase
	if c.longRunningExecutor != nil {
		if err := c.configureWorkspaceMount(ctx, true, "planning"); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to configure planning container")
		}
	}

	// Tools registered globally by orchestrator at startup
	// No need to register tools per-story or per-agent

	return StatePlanning, false, nil
}

// SetDockerImage configures the Docker image for the long-running executor.
func (c *Coder) SetDockerImage(image string) {
	if c.longRunningExecutor != nil {
		c.longRunningExecutor.SetImage(image)
	}
}

// configureWorkspaceMount configures container with readonly or readwrite workspace access.
func (c *Coder) configureWorkspaceMount(ctx context.Context, readonly bool, purpose string) error {
	// Stop current container to reconfigure
	if c.containerName != "" {
		c.logger.Info("Stopping existing container %s to reconfigure for %s", c.containerName, purpose)
		c.cleanupContainer(ctx, fmt.Sprintf("reconfigure for %s", purpose))
	}

	// Determine user based on story type
	storyType := utils.GetStateValueOr[string](c.BaseStateMachine, proto.KeyStoryType, string(proto.StoryTypeApp))
	containerUser := "1000:1000" // Default: non-root user for app stories
	if storyType == string(proto.StoryTypeDevOps) {
		containerUser = "0:0" // Run as root for DevOps stories to access Docker socket
		c.logger.Info("DevOps story detected - running container as root for Docker access")
	}

	// Create execution options for new container
	execOpts := execpkg.Opts{
		WorkDir:         c.workDir,
		ReadOnly:        readonly,
		NetworkDisabled: readonly, // Disable network during planning for security
		User:            containerUser,
		Env:             []string{},
		Timeout:         0, // No timeout for long-running container
		ResourceLimits: &execpkg.ResourceLimits{
			CPUs:   "1",    // Limited CPU for planning
			Memory: "512m", // Limited memory for planning
			PIDs:   256,    // Limited processes for planning
		},
	}

	// For coding phase, allow more resources and network access.
	if !readonly {
		execOpts.ResourceLimits.CPUs = "2"
		execOpts.ResourceLimits.Memory = "2g"
		execOpts.ResourceLimits.PIDs = 1024
		execOpts.NetworkDisabled = false
	}

	// Use sanitized agent ID for container naming (story ID not accessible from here)
	agentID := c.GetID()
	sanitizedAgentID := utils.SanitizeContainerName(agentID)

	// Start new container with appropriate configuration
	containerName, err := c.longRunningExecutor.StartContainer(ctx, sanitizedAgentID, &execOpts)
	if err != nil {
		return logx.Wrap(err, fmt.Sprintf("failed to start %s container", purpose))
	}

	c.containerName = containerName
	c.logger.Info("Started %s container: %s (readonly=%v)", purpose, containerName, readonly)

	// Update shell tool to use new container
	if err := c.updateShellToolForStory(ctx); err != nil {
		c.logger.Error("Failed to update shell tool for new container: %v", err)
		// Continue anyway - this shouldn't block the story
	}

	return nil
}

// GetContainerName returns the current container name for cleanup purposes.
func (c *Coder) GetContainerName() string {
	return c.containerName
}

// cleanupContainer stops and removes the current story's container.
func (c *Coder) cleanupContainer(ctx context.Context, reason string) {
	if c.longRunningExecutor != nil && c.containerName != "" {
		c.logger.Info("Stopping long-running container %s (%s)", c.containerName, reason)

		containerCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		if err := c.longRunningExecutor.StopContainer(containerCtx, c.containerName); err != nil {
			c.logger.Error("Failed to stop container %s: %v", c.containerName, err)
		} else {
			c.logger.Info("Container %s stopped successfully", c.containerName)
		}

		// Clear container name
		c.containerName = ""
	}
}

// updateShellToolForStory is no longer needed in the new ToolProvider system.
// The executor is provided when ToolProvider creates tools based on AgentContext.
func (c *Coder) updateShellToolForStory(_ /* storyCtx */ context.Context) error {
	// No longer needed - ToolProvider handles executor configuration
	return nil
}

// executeShellCommand runs a shell command in the current container.
func (c *Coder) executeShellCommand(ctx context.Context, args ...string) (string, error) {
	if c.longRunningExecutor == nil || c.containerName == "" {
		return "", logx.Errorf("no active container for shell execution")
	}

	opts := execpkg.Opts{
		WorkDir: "/workspace",
		Timeout: 30 * time.Second,
	}

	result, err := c.longRunningExecutor.Run(ctx, args, &opts)
	if err != nil {
		return "", fmt.Errorf("shell command failed: %w", err)
	}

	return result.Stdout, nil
}
