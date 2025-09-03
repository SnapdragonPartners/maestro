package coder

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"orchestrator/pkg/agent"
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
	// Clean and recreate work directory for fresh workspace
	c.logger.Info("🧹 Cleaning work directory for fresh workspace: %s", c.workDir)
	if err := os.RemoveAll(c.workDir); err != nil {
		c.logger.Warn("Failed to remove existing work directory: %v", err)
	}
	if err := os.MkdirAll(c.workDir, 0755); err != nil {
		return proto.StateError, false, logx.Wrap(err, "failed to create fresh work directory")
	}
	c.logger.Info("✅ Created fresh work directory: %s", c.workDir)

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

	// Update story status to PLANNING via dispatcher (non-blocking)
	if c.dispatcher != nil {
		if err := c.dispatcher.UpdateStoryStatus(storyIDStr, "planning"); err != nil {
			c.logger.Warn("Failed to update story status to planning: %v", err)
			// Continue anyway - status update failure shouldn't block the workflow
		} else {
			c.logger.Info("✅ Story %s status updated to PLANNING", storyIDStr)
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

	// Determine container image based on story type and configuration
	var containerImage string
	if storyType == string(proto.StoryTypeDevOps) {
		// DevOps stories always use the safe bootstrap container
		containerImage = config.BootstrapContainerTag
		containerUser = "0:0" // Run as root for DevOps stories to access Docker socket
		c.logger.Info("DevOps story detected - using safe container %s as root", containerImage)
	} else {
		// App stories try configured container first, fall back to bootstrap if it fails
		containerImage = getDockerImageForAgent(c.workDir)
		c.logger.Info("App story detected - attempting to use configured container: %s", containerImage)
	}

	// Update container image before starting new container
	c.SetDockerImage(containerImage)
	c.logger.Info("Set container image to: %s", containerImage)

	// Create execution options for new container
	execOpts := execpkg.Opts{
		WorkDir:         c.workDir,
		ReadOnly:        readonly,
		NetworkDisabled: false, // Network enabled for builds/tests
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

// setupGitHubAuthentication sets up GitHub authentication using GitHub CLI.
// Configures git credential helper to use GitHub CLI for push operations.
func (c *Coder) setupGitHubAuthentication(ctx context.Context) error {
	c.logger.Info("🔑 Setting up GitHub authentication")

	// FATAL CHECK: GITHUB_TOKEN must exist in environment
	if !config.HasGitHubToken() {
		return fmt.Errorf("GITHUB_TOKEN not found in environment - this is required for git operations and cannot be fixed by coder")
	}

	// Configure git to use GitHub CLI for authentication
	c.logger.Info("🔧 Configuring git credential helper to use GitHub CLI")
	setupResult, err := c.longRunningExecutor.Run(ctx, []string{"gh", "auth", "setup-git"}, &execpkg.Opts{
		WorkDir: "/workspace",
		Timeout: 30 * time.Second,
	})
	if err != nil || setupResult.ExitCode != 0 {
		return fmt.Errorf("GitHub git setup failed: %w (stdout: %s, stderr: %s)", err, setupResult.Stdout, setupResult.Stderr)
	}
	c.logger.Info("✅ Git credential helper configured for GitHub")

	c.logger.Info("✅ GitHub authentication setup completed successfully")

	// Verify the authentication setup by checking tools and configuration
	if err := c.verifyGitHubAuthSetup(ctx); err != nil {
		c.logger.Warn("⚠️ GitHub auth verification failed: %v", err)
		c.contextManager.AddMessage("system", fmt.Sprintf("GitHub authentication verification failed: %v. Authentication may be incomplete.", err))
		// Don't fail completely - let the coder try to work with potentially partial auth
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

	return nil
}

// verifyGitHubAuthSetup verifies that GitHub authentication is working correctly after script setup.
func (c *Coder) verifyGitHubAuthSetup(ctx context.Context) error {
	opts := &execpkg.Opts{
		WorkDir: "/workspace",
		Timeout: 10 * time.Second,
	}

	c.logger.Info("🔍 Verifying GitHub authentication setup")

	// Check if git is available and working
	gitResult, err := c.longRunningExecutor.Run(ctx, []string{"git", "--version"}, opts)
	if err != nil || gitResult.ExitCode != 0 {
		return fmt.Errorf("git not available or not working: %w (stdout: %s, stderr: %s)", err, gitResult.Stdout, gitResult.Stderr)
	}
	c.logger.Info("✅ Git is available: %s", strings.TrimSpace(gitResult.Stdout))

	// Check if gh (GitHub CLI) is available and working
	ghResult, err := c.longRunningExecutor.Run(ctx, []string{"gh", "--version"}, opts)
	if err != nil || ghResult.ExitCode != 0 {
		return fmt.Errorf("GitHub CLI not available or not working: %w (stdout: %s, stderr: %s)", err, ghResult.Stdout, ghResult.Stderr)
	}
	c.logger.Info("✅ GitHub CLI is available: %s", strings.TrimSpace(strings.Split(ghResult.Stdout, "\n")[0]))

	// Check GitHub API connectivity with lightweight validation (replaces gh auth status)
	if apiErr := c.validateGitHubAPIConnectivity(ctx, opts); apiErr != nil {
		return fmt.Errorf("GitHub API connectivity validation failed: %w", apiErr)
	}
	c.logger.Info("✅ GitHub API connectivity validated")

	// Check if git credential helper is configured for GitHub
	configResult, err := c.longRunningExecutor.Run(ctx, []string{"git", "config", "--list"}, opts)
	if err == nil && strings.Contains(configResult.Stdout, "credential.https://github.com.helper") {
		c.logger.Info("✅ Git credential helper configured for GitHub")
	} else {
		c.logger.Warn("⚠️ Git credential helper may not be configured properly")
	}

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

// validateGitHubAPIConnectivity performs lightweight GitHub API validation using gh CLI.
// This replaces the problematic 'gh auth status' with scope-free API calls.
func (c *Coder) validateGitHubAPIConnectivity(ctx context.Context, opts *execpkg.Opts) error {
	// Get repository info from config for API validation
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config for GitHub API validation: %w", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		return fmt.Errorf("no repository URL configured - cannot validate GitHub API access")
	}

	// Extract owner/repo from URL for API validation
	repoPath := extractRepoPath(cfg.Git.RepoURL)
	if repoPath == "" {
		return fmt.Errorf("cannot extract repository path from URL: %s", cfg.Git.RepoURL)
	}

	c.logger.Info("🔍 Validating GitHub API connectivity for repository: %s", repoPath)

	// Test 1: Validate token with /user endpoint
	userResult, err := c.longRunningExecutor.Run(ctx, []string{"gh", "api", "/user"}, opts)
	if err != nil || userResult.ExitCode != 0 {
		return fmt.Errorf("GitHub API /user validation failed: %w (stdout: %s, stderr: %s). This indicates the GITHUB_TOKEN is invalid or GitHub API is unreachable",
			err, userResult.Stdout, userResult.Stderr)
	}
	c.logger.Info("✅ GitHub API token validated")

	// Test 2: Validate repository access
	repoResult, err := c.longRunningExecutor.Run(ctx, []string{"gh", "api", fmt.Sprintf("/repos/%s", repoPath)}, opts)
	if err != nil || repoResult.ExitCode != 0 {
		return fmt.Errorf("GitHub API repository access validation failed for %s: %w (stdout: %s, stderr: %s). This indicates the token lacks repository access permissions",
			repoPath, err, repoResult.Stdout, repoResult.Stderr)
	}
	c.logger.Info("✅ GitHub API repository access validated for: %s", repoPath)

	return nil
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
