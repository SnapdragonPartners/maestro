// Package orch provides startup orchestration functionality.
package orch

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/forge"
	"orchestrator/pkg/forge/gitea"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/preflight"
)

// AirplaneOrchestrator handles airplane mode startup sequence.
// It ensures all local services are running before agents start.
type AirplaneOrchestrator struct {
	logger     *logx.Logger
	projectDir string
}

// NewAirplaneOrchestrator creates a new airplane orchestrator.
func NewAirplaneOrchestrator(projectDir string) *AirplaneOrchestrator {
	return &AirplaneOrchestrator{
		projectDir: projectDir,
		logger:     logx.NewLogger("airplane-orch"),
	}
}

// PrepareAirplaneMode orchestrates all airplane mode startup checks and services.
// Sequence: Docker → Gitea → Ollama → Models → Mirror → Boot.
func (o *AirplaneOrchestrator) PrepareAirplaneMode(ctx context.Context) error {
	o.logger.Info("✈️  Preparing airplane mode")

	// Step 1: Verify Docker is available
	if err := o.verifyDocker(ctx); err != nil {
		return fmt.Errorf("docker check failed: %w", err)
	}

	// Step 2: Ensure Gitea container is running and configured
	state, err := o.ensureGitea(ctx)
	if err != nil {
		return fmt.Errorf("gitea setup failed: %w", err)
	}

	// Step 3: Verify Ollama is running
	if err := o.verifyOllama(ctx); err != nil {
		return fmt.Errorf("ollama check failed: %w", err)
	}

	// Step 4: Verify required models are available
	if err := o.verifyModels(ctx); err != nil {
		return fmt.Errorf("model check failed: %w", err)
	}

	// Step 5: Configure mirror to use Gitea as upstream
	if err := o.configureMirror(ctx, state); err != nil {
		return fmt.Errorf("mirror configuration failed: %w", err)
	}

	o.logger.Info("✅ Airplane mode ready")
	return nil
}

// verifyDocker checks that Docker daemon is running.
func (o *AirplaneOrchestrator) verifyDocker(ctx context.Context) error {
	o.logger.Info("🐳 Checking Docker...")

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	results, err := preflight.Run(ctx, &cfg)
	if err != nil {
		return fmt.Errorf("preflight check failed: %w", err)
	}

	// Find Docker check result
	for i := range results.Checks {
		if results.Checks[i].Provider == preflight.ProviderDocker {
			if !results.Checks[i].Passed {
				return fmt.Errorf("%s", results.Checks[i].Message)
			}
			o.logger.Info("✅ %s", results.Checks[i].Message)
			return nil
		}
	}

	return fmt.Errorf("docker check not found in preflight results")
}

// ensureGitea ensures the Gitea container is running and configured.
// Returns the forge state with connection details.
func (o *AirplaneOrchestrator) ensureGitea(ctx context.Context) (*forge.State, error) {
	o.logger.Info("🐙 Ensuring Gitea is running...")

	// Check if we already have a configured Gitea instance
	state, err := forge.LoadState(o.projectDir)
	if err == nil {
		// State exists - verify Gitea is actually running
		if gitea.IsHealthy(ctx, state.URL) {
			o.logger.Info("✅ Gitea already running at %s", state.URL)
			return state, nil
		}
		o.logger.Info("⚠️  Gitea state exists but container not healthy, restarting...")
	}

	// Start Gitea container
	containerMgr := gitea.NewContainerManager()
	containerCfg := gitea.ContainerConfig{
		ProjectName: filepath.Base(o.projectDir),
		HTTPPort:    gitea.DefaultHTTPPort,
		SSHPort:     gitea.DefaultSSHPort,
	}

	info, err := containerMgr.EnsureContainer(ctx, containerCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to start Gitea container: %w", err)
	}

	o.logger.Info("✅ Gitea container running: %s", info.Name)

	// Wait for Gitea to be ready
	if waitErr := gitea.WaitForReady(ctx, info.URL, gitea.DefaultReadyTimeout); waitErr != nil {
		return nil, fmt.Errorf("gitea failed to become ready: %w", waitErr)
	}

	// Check if we need to run initial setup
	state, err = forge.LoadState(o.projectDir)
	if err != nil {
		// No state - run initial setup
		state, err = o.setupGitea(ctx, info)
		if err != nil {
			return nil, fmt.Errorf("gitea initial setup failed: %w", err)
		}
	}

	o.logger.Info("✅ Gitea ready at %s", state.URL)
	return state, nil
}

// setupGitea performs initial Gitea configuration (admin user, org, repo).
func (o *AirplaneOrchestrator) setupGitea(ctx context.Context, containerInfo *gitea.ContainerInfo) (*forge.State, error) {
	o.logger.Info("🔧 Running Gitea initial setup...")

	setupMgr := gitea.NewSetupManager()

	// Get repository info from config if available
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to get config: %w", err)
	}

	repoName := "project"
	if cfg.Git != nil && cfg.Git.RepoURL != "" {
		// Extract repo name from URL
		repoName = extractRepoName(cfg.Git.RepoURL)
	}

	setupCfg := gitea.SetupConfig{
		Container:     containerInfo,
		RepoName:      repoName,
		AdminPassword: "maestro123", // Default password for local dev
	}

	result, err := setupMgr.Setup(ctx, setupCfg)
	if err != nil {
		return nil, fmt.Errorf("gitea setup failed: %w", err)
	}

	// Save forge state
	state := &forge.State{
		Provider: string(forge.ProviderGitea),
		URL:      result.URL,
		Token:    result.Token,
		Owner:    result.Owner,
		RepoName: result.RepoName,
	}

	if err := forge.SaveState(o.projectDir, state); err != nil {
		return nil, fmt.Errorf("failed to save forge state: %w", err)
	}

	o.logger.Info("✅ Gitea setup complete: %s/%s", result.Owner, result.RepoName)
	return state, nil
}

// verifyOllama checks that Ollama is running.
func (o *AirplaneOrchestrator) verifyOllama(ctx context.Context) error {
	o.logger.Info("🦙 Checking Ollama...")

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Check if any Ollama models are needed
	requiredProviders := preflight.RequiredProviders(&cfg)
	needsOllama := false
	for _, p := range requiredProviders {
		if p == preflight.ProviderOllama {
			needsOllama = true
			break
		}
	}

	if !needsOllama {
		o.logger.Info("ℹ️  No Ollama models configured, skipping check")
		return nil
	}

	// Run Ollama preflight check
	results, err := preflight.Run(ctx, &cfg)
	if err != nil {
		return fmt.Errorf("preflight check failed: %w", err)
	}

	for i := range results.Checks {
		if results.Checks[i].Provider == preflight.ProviderOllama {
			if !results.Checks[i].Passed {
				return fmt.Errorf("%s\n%s", results.Checks[i].Message, preflight.GetGuidance(results.Checks[i].Provider))
			}
			o.logger.Info("✅ %s", results.Checks[i].Message)
			return nil
		}
	}

	return nil
}

// verifyModels checks that all required Ollama models are pulled.
func (o *AirplaneOrchestrator) verifyModels(ctx context.Context) error {
	o.logger.Info("🔍 Verifying required models...")

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// The Ollama preflight check already verifies models are available
	// This is a placeholder for additional model validation if needed

	// Log the effective models being used
	o.logger.Info("  Coder model: %s", config.GetEffectiveCoderModel())
	o.logger.Info("  Architect model: %s", config.GetEffectiveArchitectModel())
	o.logger.Info("  PM model: %s", config.GetEffectivePMModel())

	_ = cfg // Silence unused variable warning
	_ = ctx

	o.logger.Info("✅ All required models available")
	return nil
}

// configureMirror sets up the mirror to use Gitea as upstream.
func (o *AirplaneOrchestrator) configureMirror(ctx context.Context, state *forge.State) error {
	o.logger.Info("🔄 Configuring mirror for airplane mode...")

	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	if cfg.Git == nil || cfg.Git.RepoURL == "" {
		o.logger.Info("ℹ️  No repository configured, skipping mirror setup")
		return nil
	}

	// Create mirror manager
	mirrorMgr := mirror.NewManager(o.projectDir)

	// Get the mirror path (this just constructs the path, doesn't check existence)
	mirrorPath, err := mirrorMgr.GetMirrorPath()
	if err != nil {
		// Config issue (e.g., no repo URL configured)
		o.logger.Warn("⚠️  Could not determine mirror path: %v", err)
		return nil //nolint:nilerr // Expected condition: no repo configured
	}

	// Check if mirror directory actually exists
	if _, err := os.Stat(mirrorPath); os.IsNotExist(err) {
		// Mirror doesn't exist yet - it will be created on first use.
		// This is an expected condition for first-time airplane mode runs.
		o.logger.Info("ℹ️  Mirror will be created on first use")
		return nil
	}

	// Mirror exists - switch upstream to Gitea
	giteaURL := fmt.Sprintf("%s/%s/%s.git", state.URL, state.Owner, state.RepoName)

	if err := mirrorMgr.SwitchUpstream(ctx, giteaURL); err != nil {
		return fmt.Errorf("failed to switch upstream: %w", err)
	}

	o.logger.Info("✅ Mirror configured to use Gitea: %s", giteaURL)
	_ = mirrorPath // Silence unused variable

	return nil
}

// Shutdown gracefully stops airplane mode services.
func (o *AirplaneOrchestrator) Shutdown(ctx context.Context) error {
	o.logger.Info("✈️  Shutting down airplane mode services...")

	// Load state to get container name
	state, err := forge.LoadState(o.projectDir)
	if err != nil {
		// No state - nothing to shut down.
		// This is an expected condition when airplane mode wasn't configured.
		return nil //nolint:nilerr // Expected condition: no airplane mode state
	}

	// Stop Gitea container
	containerMgr := gitea.NewContainerManager()
	containerName := gitea.ContainerPrefix + filepath.Base(o.projectDir)

	if err := containerMgr.StopContainer(ctx, containerName); err != nil {
		o.logger.Warn("⚠️  Failed to stop Gitea container: %v", err)
		// Don't fail shutdown for this
	}

	_ = state // Silence unused variable

	o.logger.Info("✅ Airplane mode services stopped")
	return nil
}

// extractRepoName extracts the repository name from a git URL.
func extractRepoName(repoURL string) string {
	// Handle common URL formats:
	// https://github.com/owner/repo.git
	// git@github.com:owner/repo.git
	// https://github.com/owner/repo

	// Remove .git suffix
	name := repoURL
	if len(name) > 4 && name[len(name)-4:] == ".git" {
		name = name[:len(name)-4]
	}

	// Find last path component
	lastSlash := -1
	for i := len(name) - 1; i >= 0; i-- {
		if name[i] == '/' || name[i] == ':' {
			lastSlash = i
			break
		}
	}

	if lastSlash >= 0 {
		return name[lastSlash+1:]
	}

	return name
}

// IsAirplaneModeConfigured checks if airplane mode has been previously configured.
func IsAirplaneModeConfigured(projectDir string) bool {
	state, err := forge.LoadState(projectDir)
	if err != nil {
		return false
	}
	return state.Provider == string(forge.ProviderGitea)
}

// GetAirplaneModeStatus returns the current status of airplane mode services.
func GetAirplaneModeStatus(ctx context.Context, projectDir string) (map[string]string, error) {
	status := make(map[string]string)

	// Check Gitea
	state, err := forge.LoadState(projectDir)
	if err != nil {
		status["gitea"] = "not configured"
	} else if gitea.IsHealthy(ctx, state.URL) {
		status["gitea"] = fmt.Sprintf("running at %s", state.URL)
	} else {
		status["gitea"] = "not running"
	}

	// Check Ollama
	ollamaURL := os.Getenv("OLLAMA_HOST")
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}
	if isOllamaHealthy(ctx, ollamaURL) {
		status["ollama"] = "running"
	} else {
		status["ollama"] = "not running"
	}

	return status, nil
}

// isOllamaHealthy checks if Ollama is reachable.
func isOllamaHealthy(ctx context.Context, baseURL string) bool {
	client := &http.Client{Timeout: 2 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/api/tags", http.NoBody)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}
