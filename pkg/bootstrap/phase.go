package bootstrap

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"orchestrator/pkg/build"
	"orchestrator/pkg/coder"
	"orchestrator/pkg/logx"
)

// Phase represents the PROJECT_BOOTSTRAP orchestrator phase
type Phase struct {
	projectRoot      string
	buildRegistry    *build.Registry
	logger           *logx.Logger
	config           *Config
	workspaceManager *coder.WorkspaceManager
}

// Config holds bootstrap configuration options
type Config struct {
	Enabled                 bool                    `json:"enabled"`
	ForceBackend            string                  `json:"force_backend"`            // Override auto-detection
	SkipMakefile            bool                    `json:"skip_makefile"`            // Skip Makefile generation
	AdditionalArtifacts     []string                `json:"additional_artifacts"`     // Custom artifacts to generate
	TemplateOverrides       map[string]string       `json:"template_overrides"`       // Custom template paths
	BranchName              string                  `json:"branch_name"`              // Bootstrap branch name
	AutoMerge               bool                    `json:"auto_merge"`               // Auto-merge to main
	BaseBranch              string                  `json:"base_branch"`              // Base branch for merge
	RepoURL                 string                  `json:"repo_url"`                 // Git repository URL
	ArchitectRecommendation *PlatformRecommendation `json:"architect_recommendation"` // Architect's stack recommendation
}

// DefaultConfig returns default bootstrap configuration
func DefaultConfig() *Config {
	return &Config{
		Enabled:                 true,
		ForceBackend:            "",
		SkipMakefile:            false,
		AdditionalArtifacts:     []string{},
		TemplateOverrides:       make(map[string]string),
		BranchName:              "bootstrap-init",
		AutoMerge:               true,
		BaseBranch:              "main",
		ArchitectRecommendation: nil,
	}
}

// NewPhase creates a new bootstrap phase
func NewPhase(projectRoot string, config *Config) *Phase {
	if config == nil {
		config = DefaultConfig()
	}

	// Create workspace manager for git operations using the same pattern as coder
	gitRunner := coder.NewDefaultGitRunner()
	workspaceManager := coder.NewWorkspaceManager(
		gitRunner,
		filepath.Dir(projectRoot), // Use parent of projectRoot as projectWorkDir
		config.RepoURL,
		config.BaseBranch,
		".mirrors",
		"bootstrap-{STORY_ID}", // Won't be used since we commit to main
		"{STORY_ID}",           // Won't be used since we use projectRoot directly
	)

	return &Phase{
		projectRoot:      projectRoot,
		buildRegistry:    build.NewRegistry(),
		logger:           logx.NewLogger("bootstrap"),
		config:           config,
		workspaceManager: workspaceManager,
	}
}

// PhaseResult represents the result of bootstrap phase execution
type PhaseResult struct {
	Success        bool              `json:"success"`
	Backend        string            `json:"backend"`
	GeneratedFiles []string          `json:"generated_files"`
	BranchCreated  string            `json:"branch_created"`
	MergeCompleted bool              `json:"merge_completed"`
	Duration       time.Duration     `json:"duration"`
	Error          string            `json:"error,omitempty"`
	Metadata       map[string]string `json:"metadata"`
}

// Execute runs the PROJECT_BOOTSTRAP phase
func (p *Phase) Execute(ctx context.Context) (*PhaseResult, error) {
	if !p.config.Enabled {
		p.logger.Info("Bootstrap phase disabled in configuration")
		return &PhaseResult{
			Success:  true,
			Backend:  "disabled",
			Duration: 0,
			Metadata: map[string]string{"status": "disabled"},
		}, nil
	}

	startTime := time.Now()
	p.logger.Info("Starting PROJECT_BOOTSTRAP phase for project: %s", p.projectRoot)

	result := &PhaseResult{
		GeneratedFiles: []string{},
		Metadata:       make(map[string]string),
	}

	// Step 1: Setup workspace using WorkspaceManager (ensures mirror exists and creates worktree)
	workDir, err := p.setupWorkspace(ctx)
	if err != nil {
		return p.failureResult(startTime, fmt.Errorf("workspace setup failed: %w", err))
	}
	p.logger.Info("Bootstrap workspace ready at: %s", workDir)

	// Step 2: Detect build backend (using workspace directory)
	backend, err := p.detectBackend(ctx, workDir)
	if err != nil {
		return p.failureResult(startTime, fmt.Errorf("backend detection failed: %w", err))
	}

	result.Backend = backend.Name()
	result.Metadata["backend_detected"] = backend.Name()
	p.logger.Info("Detected backend: %s", backend.Name())

	// Step 3: Check if bootstrap artifacts already exist
	if p.bootstrapArtifactsExist(workDir) {
		p.logger.Info("Bootstrap artifacts already exist, skipping generation")
		result.GeneratedFiles = []string{}
		result.Metadata["artifacts_count"] = "0"
		result.Metadata["artifacts_skipped"] = "already_exist"
	} else {
		// Generate bootstrap artifacts in workspace
		artifacts, err := p.generateArtifacts(ctx, backend, workDir)
		if err != nil {
			return p.failureResult(startTime, fmt.Errorf("artifact generation failed: %w", err))
		}

		result.GeneratedFiles = artifacts
		result.Metadata["artifacts_count"] = fmt.Sprintf("%d", len(artifacts))
		p.logger.Info("Generated %d bootstrap artifacts", len(artifacts))
	}

	// Step 4: Commit artifacts directly to main branch (only if new artifacts were generated)
	if len(result.GeneratedFiles) > 0 {
		if err := p.commitToMain(ctx, workDir, result.GeneratedFiles); err != nil {
			return p.failureResult(startTime, fmt.Errorf("failed to commit bootstrap artifacts: %w", err))
		}
	} else {
		p.logger.Info("No new artifacts to commit")
	}

	// Step 6: Success
	result.Success = true
	result.Duration = time.Since(startTime)
	result.Metadata["duration_ms"] = fmt.Sprintf("%d", result.Duration.Milliseconds())

	p.logger.Info("PROJECT_BOOTSTRAP phase completed successfully in %v", result.Duration)
	return result, nil
}

// detectBackend detects the appropriate build backend for the project
func (p *Phase) detectBackend(ctx context.Context, workDir string) (build.BuildBackend, error) {
	// Check for forced backend override
	if p.config.ForceBackend != "" {
		backend, err := p.buildRegistry.GetByName(p.config.ForceBackend)
		if err != nil {
			return nil, fmt.Errorf("forced backend '%s' not found: %w", p.config.ForceBackend, err)
		}
		p.logger.Info("Using forced backend: %s", p.config.ForceBackend)
		return backend, nil
	}

	// Check for architect recommendation
	if p.config.ArchitectRecommendation != nil {
		platformName := p.config.ArchitectRecommendation.Platform
		backend, err := p.buildRegistry.GetByName(platformName)
		if err != nil {
			p.logger.Warn("Architect recommended platform '%s' not found, falling back to auto-detection: %v", platformName, err)
		} else {
			p.logger.Info("Using architect recommended backend: %s (confidence: %.2f)",
				platformName, p.config.ArchitectRecommendation.Confidence)
			return backend, nil
		}
	}

	// Auto-detect backend using workspace directory
	backend, err := p.buildRegistry.Detect(workDir)
	if err != nil {
		return nil, fmt.Errorf("auto-detection failed: %w", err)
	}

	return backend, nil
}

// generateArtifacts generates bootstrap artifacts based on the detected backend
func (p *Phase) generateArtifacts(ctx context.Context, backend build.BuildBackend, workDir string) ([]string, error) {
	generator := NewArtifactGenerator(workDir, p.config)

	artifacts, err := generator.Generate(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("artifact generation failed: %w", err)
	}

	return artifacts, nil
}

// setupWorkspace uses WorkspaceManager to create a workspace for bootstrap operations
func (p *Phase) setupWorkspace(ctx context.Context) (string, error) {
	// For bootstrap, we'll use a dummy story ID since we're working directly on main
	// The important thing is that we get a proper worktree from the mirror
	dummyStoryID := "bootstrap"
	agentID := "bootstrap"

	// Use projectRoot as the agent work directory for bootstrap
	workspaceResult, err := p.workspaceManager.SetupWorkspace(ctx, agentID, dummyStoryID, p.projectRoot)
	if err != nil {
		return "", fmt.Errorf("failed to setup workspace: %w", err)
	}

	return workspaceResult.WorkDir, nil
}

// commitToMain commits the bootstrap artifacts directly to the main branch
func (p *Phase) commitToMain(ctx context.Context, workDir string, artifacts []string) error {
	gitRunner := coder.NewDefaultGitRunner()

	// Ensure we're on the main branch
	if _, err := gitRunner.Run(ctx, workDir, "checkout", p.config.BaseBranch); err != nil {
		return fmt.Errorf("failed to checkout %s: %w", p.config.BaseBranch, err)
	}

	// Add all artifacts to staging
	for _, artifact := range artifacts {
		if _, err := gitRunner.Run(ctx, workDir, "add", artifact); err != nil {
			return fmt.Errorf("failed to add %s to staging: %w", artifact, err)
		}
	}

	// Check if there are any changes to commit
	output, err := gitRunner.Run(ctx, workDir, "diff", "--cached", "--name-only")
	if err != nil {
		return fmt.Errorf("failed to check staging area: %w", err)
	}

	if len(output) == 0 {
		p.logger.Info("No changes to commit")
		return nil
	}

	// Commit changes
	commitMessage := "Bootstrap project build system\n\nGenerated bootstrap artifacts:\n"
	for _, artifact := range artifacts {
		commitMessage += "- " + artifact + "\n"
	}
	commitMessage += "\n🤖 Generated with [Claude Code](https://claude.ai/code)\n\nCo-Authored-By: Claude <noreply@anthropic.com>"

	if _, err := gitRunner.Run(ctx, workDir, "commit", "-m", commitMessage); err != nil {
		return fmt.Errorf("failed to commit artifacts: %w", err)
	}

	p.logger.Info("Committed bootstrap artifacts to %s", p.config.BaseBranch)

	// Push the changes to remote to ensure other agents see the bootstrap artifacts
	p.logger.Info("Pushing bootstrap artifacts to remote %s", p.config.BaseBranch)
	if _, err := gitRunner.Run(ctx, workDir, "push", "origin", p.config.BaseBranch); err != nil {
		return fmt.Errorf("failed to push bootstrap artifacts to remote: %w", err)
	}

	p.logger.Info("Successfully pushed bootstrap artifacts to remote %s", p.config.BaseBranch)
	return nil
}

// failureResult creates a failure result with timing information
func (p *Phase) failureResult(startTime time.Time, err error) (*PhaseResult, error) {
	duration := time.Since(startTime)

	result := &PhaseResult{
		Success:  false,
		Duration: duration,
		Error:    err.Error(),
		Metadata: make(map[string]string),
	}

	result.Metadata["duration_ms"] = fmt.Sprintf("%d", duration.Milliseconds())
	result.Metadata["error"] = err.Error()

	p.logger.Error("PROJECT_BOOTSTRAP phase failed after %v: %v", duration, err)
	return result, err
}

// GetStatus returns the current status of the bootstrap phase
func (p *Phase) GetStatus() map[string]interface{} {
	return map[string]interface{}{
		"enabled":      p.config.Enabled,
		"project_root": p.projectRoot,
		"config":       p.config,
		"backends":     p.buildRegistry.List(),
	}
}

// bootstrapArtifactsExist checks if key bootstrap artifacts already exist in the work directory
func (p *Phase) bootstrapArtifactsExist(workDir string) bool {
	// Check for key bootstrap artifacts that indicate bootstrap has already run
	keyArtifacts := []string{
		"Makefile",       // Root Makefile is always generated unless explicitly skipped
		".gitignore",     // Git ignore file
		".gitattributes", // Git attributes file
	}

	// Skip Makefile check if it's disabled in config
	if p.config.SkipMakefile {
		keyArtifacts = keyArtifacts[1:] // Remove Makefile from the list
	}

	for _, artifact := range keyArtifacts {
		artifactPath := filepath.Join(workDir, artifact)
		if _, err := os.Stat(artifactPath); os.IsNotExist(err) {
			p.logger.Debug("Bootstrap artifact missing: %s", artifact)
			return false
		}
	}

	p.logger.Debug("All key bootstrap artifacts exist in %s", workDir)
	return true
}
