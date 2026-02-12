package coder

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
	execpkg "orchestrator/pkg/exec"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/templates"
	"orchestrator/pkg/utils"
)

// handleSetup processes the SETUP state
//
//nolint:unparam // bool return required by state machine interface, always false for non-terminal states
func (c *Coder) handleSetup(ctx context.Context, sm *agent.BaseStateMachine) (proto.State, bool, error) {
	// Reset Claude Code availability check for new story (will be re-checked after container starts)
	c.claudeCodeAvailabilityChecked = false
	c.claudeCodeAvailable = false

	// Reset NEEDS_CHANGES counter for new story (temperature laddering starts fresh)
	sm.SetStateData(KeyNeedsChangesCount, 0)

	// Clean work directory contents while preserving the directory itself.
	// IMPORTANT: We must NOT delete and recreate the directory because Docker bind mounts
	// on macOS track the inode. If we delete/recreate the directory, the architect container's
	// mount to /mnt/coders/{agent-id} becomes stale (points to deleted inode).
	// Instead, we empty the contents which preserves the inode and keeps bind mounts working.
	c.logger.Info("🧹 Cleaning work directory contents for fresh workspace: %s", c.workDir)
	if err := utils.CleanDirectoryContents(c.workDir); err != nil {
		c.logger.Warn("Failed to clean work directory contents: %v", err)
	}
	// Ensure directory exists (creates if it doesn't, no-op if it does)
	if err := os.MkdirAll(c.workDir, 0755); err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to ensure work directory exists")
	}
	c.logger.Info("✅ Work directory ready: %s", c.workDir)

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

	// Git user identity is now configured during CloneManager.SetupWorkspace() on the host
	// This avoids read-only filesystem issues with container mounts

	// Configure container with read-only workspace for planning phase
	if c.longRunningExecutor != nil {
		if err := c.configureWorkspaceMount(ctx, true, "planning"); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to configure planning container")
		}
	}

	// Check if this is an express story (knowledge update, hotfix, etc.)
	// Express stories skip planning and go straight to coding
	isExpress := false
	if expressVal, exists := sm.GetStateValue(KeyExpress); exists {
		if express, ok := expressVal.(bool); ok && express {
			isExpress = true
			c.logger.Info("⚡ Express story detected - skipping planning phase")
		}
	}

	// Update story status via dispatcher (non-blocking)
	nextStatus := "planning"
	if isExpress {
		nextStatus = "coding"
	}
	if c.dispatcher != nil {
		if err := c.dispatcher.UpdateStoryStatus(storyIDStr, nextStatus); err != nil {
			c.logger.Warn("Failed to update story status to %s: %v", nextStatus, err)
			// Continue anyway - status update failure shouldn't block the workflow
		} else {
			c.logger.Info("✅ Story %s status updated to %s", storyIDStr, strings.ToUpper(nextStatus))
		}
	}

	// Tools registered globally by orchestrator at startup
	// No need to register tools per-story or per-agent

	// Express stories skip planning and go straight to coding with read-write workspace
	if isExpress {
		// Reconfigure workspace as read-write for coding
		if err := c.configureWorkspaceMount(ctx, false, "coding"); err != nil {
			return proto.StateError, false, logx.Wrap(err, "failed to configure coding container for express story")
		}
		return StateCoding, false, nil
	}

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

	// All story types run as non-root user for security
	// Container tools (container_build, container_switch, etc.) use local executor to run
	// docker commands directly on the host, so containers don't need docker.sock access
	storyType := utils.GetStateValueOr[string](c.BaseStateMachine, proto.KeyStoryType, string(proto.StoryTypeApp))
	containerUser := "1000:1000" // Non-root user for all story types

	// Determine container image based on story type and configuration
	var containerImage string
	if storyType == string(proto.StoryTypeDevOps) {
		// DevOps stories use the safe bootstrap container (non-root, no docker.sock needed)
		containerImage = config.BootstrapContainerTag
		c.logger.Info("DevOps story detected - using safe container %s as non-root", containerImage)
	} else {
		// App stories try configured container first, fall back to bootstrap if it fails
		containerImage = getDockerImageForAgent(c.workDir)
		c.logger.Info("App story detected - attempting to use configured container: %s", containerImage)
	}

	// Update container image before starting new container
	c.SetDockerImage(containerImage)
	c.logger.Info("Set container image to: %s", containerImage)

	// Check if running in Claude Code mode (for session persistence volume)
	isClaudeCodeConfigured := false
	if cfg, cfgErr := config.GetConfig(); cfgErr == nil && cfg.Agents != nil {
		isClaudeCodeConfigured = cfg.Agents.CoderMode == config.CoderModeClaudeCode
	}

	// Create execution options for new container
	execOpts := execpkg.Opts{
		WorkDir:         c.workDir,
		ReadOnly:        readonly,
		NetworkDisabled: false, // Network enabled for builds/tests
		User:            containerUser,
		Env:             []string{"HOME=/tmp"}, // Set HOME to writable location for git config
		Timeout:         0,                     // No timeout for long-running container
		ClaudeCodeMode:  isClaudeCodeConfigured,
		ResourceLimits: &execpkg.ResourceLimits{
			CPUs:   "1",    // Limited CPU for planning (standard mode)
			Memory: "512m", // Limited memory for planning (standard mode)
			PIDs:   256,    // Limited processes for planning (standard mode)
		},
	}

	// Claude Code mode needs full resources even for planning because it runs
	// a Node.js subprocess (Claude Code CLI) that needs significant RAM and CPU.
	if isClaudeCodeConfigured && readonly {
		execOpts.ResourceLimits.CPUs = config.GetContainerCPUs()
		execOpts.ResourceLimits.Memory = config.GetContainerMemory()
		execOpts.ResourceLimits.PIDs = config.GetContainerPIDs()
		c.logger.Info("Claude Code planning container resources: cpus=%s memory=%s pids=%d",
			execOpts.ResourceLimits.CPUs, execOpts.ResourceLimits.Memory, execOpts.ResourceLimits.PIDs)
	}

	// For coding phase, allow more resources and network access.
	if !readonly {
		execOpts.ResourceLimits.CPUs = config.GetContainerCPUs()
		execOpts.ResourceLimits.Memory = config.GetContainerMemory()
		execOpts.ResourceLimits.PIDs = config.GetContainerPIDs()
		execOpts.NetworkDisabled = false
		// SECURITY: No GITHUB_TOKEN injection into container.
		// Git push operations run on the HOST (not in container) to prevent
		// coders from pushing unapproved code. See pushBranch() in prepare_merge.go.
	}

	// Use sanitized agent ID for container naming (story ID not accessible from here)
	agentID := c.GetID()
	sanitizedAgentID := utils.SanitizeContainerName(agentID)

	// Start new container with appropriate configuration
	containerName, err := c.longRunningExecutor.StartContainer(ctx, sanitizedAgentID, &execOpts)
	if err != nil {
		// For app stories, try falling back to bootstrap container if configured container fails
		if storyType == string(proto.StoryTypeApp) && containerImage != config.BootstrapContainerTag {
			c.logger.Warn("Failed to start configured container %s, falling back to safe container %s: %v",
				containerImage, config.BootstrapContainerTag, err)

			// Update to bootstrap container and retry
			c.SetDockerImage(config.BootstrapContainerTag)
			containerName, err = c.longRunningExecutor.StartContainer(ctx, sanitizedAgentID, &execOpts)
			if err != nil {
				return logx.Wrap(err, fmt.Sprintf("failed to start %s container with fallback %s", purpose, config.BootstrapContainerTag))
			}
			c.logger.Info("Successfully started fallback container: %s", config.BootstrapContainerTag)
		} else {
			return logx.Wrap(err, fmt.Sprintf("failed to start %s container", purpose))
		}
	}

	c.containerName = containerName
	c.logger.Info("Started %s container: %s (readonly=%v)", purpose, containerName, readonly)

	// Configure build service to use this container for execution.
	// This ensures build/test/lint commands run inside the container, not on the host.
	if c.buildService != nil {
		executor := build.NewContainerExecutor(containerName)
		c.buildService.SetExecutor(executor)
		c.logger.Info("Configured build service with container executor: %s", containerName)
	}

	// For coding containers, ensure GitHub authentication is set up using embedded script
	if !readonly {
		if err := c.setupGitHubAuthentication(ctx); err != nil {
			return logx.Wrap(err, "GitHub authentication setup failed - cannot proceed with coding")
		}
	}

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

// setupGitHubAuthentication verifies prerequisites and configures git for commits.
// Note: Git push operations run on the host (not in container) so no credentials are injected here.
// The GITHUB_TOKEN check ensures host-side push will work later.
func (c *Coder) setupGitHubAuthentication(ctx context.Context) error {
	c.logger.Info("🔑 Setting up git configuration")

	// FATAL CHECK: GITHUB_TOKEN must exist in environment (for host-side push later)
	if !config.HasGitHubToken() {
		return fmt.Errorf("GITHUB_TOKEN not found in environment - this is required for git operations and cannot be fixed by coder")
	}

	// Verify git is available in container
	if err := c.verifyGitAvailable(ctx); err != nil {
		return fmt.Errorf("git verification failed: %w", err)
	}

	// Configure git user identity using our config values
	if err := c.configureGitUserIdentity(ctx); err != nil {
		c.logger.Warn("⚠️ Failed to configure git user identity: %v", err)
		// Add helpful context message for the coder
		globalConfig, configErr := config.GetConfig()
		if configErr == nil {
			templateData := map[string]string{
				"Error":        err.Error(),
				"GitUserName":  globalConfig.Git.GitUserName,
				"GitUserEmail": globalConfig.Git.GitUserEmail,
			}
			if renderedMessage, renderErr := c.renderer.RenderSimple(templates.GitConfigFailureTemplate, templateData); renderErr == nil {
				c.contextManager.AddMessage("system", renderedMessage)
			} else {
				c.contextManager.AddMessage("system", fmt.Sprintf("Could not configure git user identity: %v. You may need to set git user.name and user.email manually.", err))
			}
		}
		// Don't fail completely - git user config can be set manually by the coder if needed
	}

	c.logger.Info("✅ Git configuration completed")
	return nil
}

// verifyGitAvailable verifies that git is available in the container.
func (c *Coder) verifyGitAvailable(ctx context.Context) error {
	opts := &execpkg.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	c.logger.Info("🔍 Verifying git is available")

	// Check if git is available and working
	gitResult, err := c.longRunningExecutor.Run(ctx, []string{"git", "--version"}, opts)
	if err != nil || gitResult.ExitCode != 0 {
		return fmt.Errorf("git not available or not working: %w (stdout: %s, stderr: %s)", err, gitResult.Stdout, gitResult.Stderr)
	}
	c.logger.Info("✅ Git is available: %s", strings.TrimSpace(gitResult.Stdout))

	return nil
}

// configureGitUserIdentity configures git user.name and user.email in the container using config values.
// This is called after the GitHub auth script to ensure proper git user identity.
func (c *Coder) configureGitUserIdentity(ctx context.Context) error {
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	agentID := c.GetID()
	userName := strings.ReplaceAll(cfg.Git.GitUserName, "{AGENT_ID}", agentID)
	userEmail := strings.ReplaceAll(cfg.Git.GitUserEmail, "{AGENT_ID}", agentID)

	opts := &execpkg.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	c.logger.Info("🧑 Configuring git user identity: %s <%s>", userName, userEmail)

	// Set user.name - this overrides any settings from the auth script
	nameResult, err := c.longRunningExecutor.Run(ctx, []string{"git", "config", "user.name", userName}, opts)
	if err != nil || nameResult.ExitCode != 0 {
		return fmt.Errorf("failed to set git user.name: %w (stdout: %s, stderr: %s)", err, nameResult.Stdout, nameResult.Stderr)
	}

	// Set user.email - this overrides any settings from the auth script
	emailResult, err := c.longRunningExecutor.Run(ctx, []string{"git", "config", "user.email", userEmail}, opts)
	if err != nil || emailResult.ExitCode != 0 {
		return fmt.Errorf("failed to set git user.email: %w (stdout: %s, stderr: %s)", err, emailResult.Stdout, emailResult.Stderr)
	}

	// Verify the configuration was set correctly
	verifyResult, err := c.longRunningExecutor.Run(ctx, []string{"git", "config", "--list"}, opts)
	if err == nil {
		if strings.Contains(verifyResult.Stdout, fmt.Sprintf("user.name=%s", userName)) &&
			strings.Contains(verifyResult.Stdout, fmt.Sprintf("user.email=%s", userEmail)) {
			c.logger.Info("✅ Git user identity configured and verified: %s <%s>", userName, userEmail)
		} else {
			c.logger.Warn("⚠️ Git user identity may not have been set correctly")
		}
	}

	return nil
}
