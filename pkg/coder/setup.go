package coder

import (
	"context"
	"fmt"
	"strings"
	"time"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
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

	// Git user identity is now configured during CloneManager.SetupWorkspace() on the host
	// This avoids read-only filesystem issues with container mounts

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

		// Inject GITHUB_TOKEN for git operations during coding phase
		if config.HasGitHubToken() {
			execOpts.Env = append(execOpts.Env, "GITHUB_TOKEN")
			c.logger.Debug("Injected GITHUB_TOKEN into coding container environment")
		} else {
			c.logger.Warn("GITHUB_TOKEN not found in environment - git push operations may fail")
		}
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

	// For coding containers, ensure GitHub authentication is set up
	if !readonly {
		if err := c.ensureGitHubAuthentication(ctx, true); err != nil {
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

// ensureGitHubAuthentication ensures GitHub authentication is properly configured in the container.
// This includes checking for GITHUB_TOKEN, verifying git/gh tools, configuring git user, and setting up auth.
// addContextMessage controls whether helpful context messages are added for the coder (true for container setup, false for PREPARE_MERGE).
func (c *Coder) ensureGitHubAuthentication(ctx context.Context, addContextMessage bool) error {
	opts := &execpkg.Opts{
		WorkDir: c.workDir,
		Timeout: 15 * time.Second,
		Env:     []string{},
	}

	// FATAL CHECK: GITHUB_TOKEN must exist
	if !config.HasGitHubToken() {
		return fmt.Errorf("GITHUB_TOKEN not found in environment - this is required for git operations and cannot be fixed by coder")
	}
	opts.Env = append(opts.Env, "GITHUB_TOKEN")

	// Check if git is available
	gitResult, gitErr := c.longRunningExecutor.Run(ctx, []string{"which", "git"}, opts)
	gitAvailable := gitErr == nil && gitResult.ExitCode == 0

	// Check if gh (GitHub CLI) is available
	ghResult, ghErr := c.longRunningExecutor.Run(ctx, []string{"which", "gh"}, opts)
	ghAvailable := ghErr == nil && ghResult.ExitCode == 0

	c.logger.Info("🔑 Ensuring GitHub authentication (git: %t, gh: %t)", gitAvailable, ghAvailable)

	// Add context messages for missing tools (only during container setup)
	if addContextMessage {
		if !gitAvailable {
			c.contextManager.AddMessage("system", "Git is not installed in the container. You will need to install git before making commits or pushes.")
		}
		if !ghAvailable {
			c.contextManager.AddMessage("system", "GitHub CLI (gh) is not installed in the container. You will need to install gh before creating pull requests.")
		}
	}

	// Configure git user identity (if git is available)
	if gitAvailable {
		if err := c.configureGitUser(ctx, opts); err != nil {
			c.logger.Warn("⚠️ Failed to configure git user identity: %v", err)
			if addContextMessage {
				c.contextManager.AddMessage("system", fmt.Sprintf("Could not configure git user identity: %v. You may need to set git user.name and user.email manually.", err))
			}
		}
	}

	// Setup GitHub CLI authentication (if both tools are available)
	if gitAvailable && ghAvailable {
		result, err := c.longRunningExecutor.Run(ctx, []string{"gh", "auth", "setup-git"}, opts)
		if err != nil {
			c.logger.Warn("⚠️ GitHub CLI auth setup failed: %v (stdout: %s, stderr: %s)", err, result.Stdout, result.Stderr)
			if addContextMessage {
				c.contextManager.AddMessage("system", fmt.Sprintf("GitHub CLI authentication setup failed: %v. You may need to troubleshoot GitHub authentication before making git operations.", err))
			}
		} else {
			c.logger.Info("✅ Git authentication configured - GitHub CLI will handle git credentials")

			// Verify the setup worked
			configResult, configErr := c.longRunningExecutor.Run(ctx, []string{"git", "config", "--list"}, opts)
			if configErr == nil && strings.Contains(configResult.Stdout, "credential.https://github.com.helper=!/usr/bin/gh auth git-credential") {
				c.logger.Info("✅ Git credential helper verified: GitHub CLI is configured")
			}
		}
	}

	return nil
}

// configureGitUser configures git user.name and user.email in the container using config values.
func (c *Coder) configureGitUser(ctx context.Context, opts *execpkg.Opts) error {
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	agentID := c.GetID()
	userName := strings.ReplaceAll(cfg.Git.GitUserName, "{AGENT_ID}", agentID)
	userEmail := strings.ReplaceAll(cfg.Git.GitUserEmail, "{AGENT_ID}", agentID)

	// Set user.name
	_, err = c.longRunningExecutor.Run(ctx, []string{"git", "config", "user.name", userName}, opts)
	if err != nil {
		return fmt.Errorf("failed to set git user.name: %w", err)
	}

	// Set user.email
	_, err = c.longRunningExecutor.Run(ctx, []string{"git", "config", "user.email", userEmail}, opts)
	if err != nil {
		return fmt.Errorf("failed to set git user.email: %w", err)
	}

	c.logger.Info("✅ Configured git user identity: %s <%s>", userName, userEmail)
	return nil
}
