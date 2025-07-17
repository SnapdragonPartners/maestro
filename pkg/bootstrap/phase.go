package bootstrap

import (
	"context"
	"fmt"
	"time"

	"orchestrator/pkg/build"
	"orchestrator/pkg/logx"
)

// Phase represents the PROJECT_BOOTSTRAP orchestrator phase
type Phase struct {
	projectRoot   string
	buildRegistry *build.Registry
	logger        *logx.Logger
	config        *Config
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

	return &Phase{
		projectRoot:   projectRoot,
		buildRegistry: build.NewRegistry(),
		logger:        logx.NewLogger("bootstrap"),
		config:        config,
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

	// Step 1: Ensure we have a Git repository (required for bootstrap)
	if err := p.ensureGitRepository(ctx); err != nil {
		return p.failureResult(startTime, fmt.Errorf("git repository validation failed: %w", err))
	}

	// Step 2: Detect build backend
	backend, err := p.detectBackend(ctx)
	if err != nil {
		return p.failureResult(startTime, fmt.Errorf("backend detection failed: %w", err))
	}

	result.Backend = backend.Name()
	result.Metadata["backend_detected"] = backend.Name()
	p.logger.Info("Detected backend: %s", backend.Name())

	// Step 3: Generate bootstrap artifacts
	artifacts, err := p.generateArtifacts(ctx, backend)
	if err != nil {
		return p.failureResult(startTime, fmt.Errorf("artifact generation failed: %w", err))
	}

	result.GeneratedFiles = artifacts
	result.Metadata["artifacts_count"] = fmt.Sprintf("%d", len(artifacts))
	p.logger.Info("Generated %d bootstrap artifacts", len(artifacts))

	// Step 4: Create bootstrap branch (Git is now guaranteed to be available)
	branchName, err := p.createBootstrapBranch(ctx, artifacts)
	if err != nil {
		return p.failureResult(startTime, fmt.Errorf("failed to create bootstrap branch: %w", err))
	}

	if branchName != "" {
		result.BranchCreated = branchName
		result.Metadata["branch_created"] = branchName
		p.logger.Info("Created bootstrap branch: %s", branchName)
	}

	// Step 5: Auto-merge to base branch (if configured)
	if p.config.AutoMerge && branchName != "" {
		merged, err := p.autoMergeBootstrap(ctx, branchName)
		if err != nil {
			return p.failureResult(startTime, fmt.Errorf("failed to auto-merge bootstrap: %w", err))
		}

		result.MergeCompleted = merged
		result.Metadata["merge_completed"] = fmt.Sprintf("%t", merged)
		if merged {
			p.logger.Info("Auto-merged bootstrap to %s", p.config.BaseBranch)
		}
	}

	// Step 6: Success
	result.Success = true
	result.Duration = time.Since(startTime)
	result.Metadata["duration_ms"] = fmt.Sprintf("%d", result.Duration.Milliseconds())

	p.logger.Info("PROJECT_BOOTSTRAP phase completed successfully in %v", result.Duration)
	return result, nil
}

// detectBackend detects the appropriate build backend for the project
func (p *Phase) detectBackend(ctx context.Context) (build.BuildBackend, error) {
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

	// Auto-detect backend
	backend, err := p.buildRegistry.Detect(p.projectRoot)
	if err != nil {
		return nil, fmt.Errorf("auto-detection failed: %w", err)
	}

	return backend, nil
}

// generateArtifacts generates bootstrap artifacts based on the detected backend
func (p *Phase) generateArtifacts(ctx context.Context, backend build.BuildBackend) ([]string, error) {
	generator := NewArtifactGenerator(p.projectRoot, p.config)

	artifacts, err := generator.Generate(ctx, backend)
	if err != nil {
		return nil, fmt.Errorf("artifact generation failed: %w", err)
	}

	return artifacts, nil
}

// createBootstrapBranch creates a dedicated bootstrap branch for the artifacts
func (p *Phase) createBootstrapBranch(ctx context.Context, artifacts []string) (string, error) {
	gitManager := NewGitManager(p.projectRoot, p.logger)

	// Check if we're in a Git repository (this should never happen now due to ensureGitRepository)
	if !gitManager.IsGitRepository() {
		return "", fmt.Errorf("not in a Git repository - this should have been caught earlier")
	}

	// Create bootstrap branch
	branchName := p.config.BranchName
	if err := gitManager.CreateBranch(ctx, branchName); err != nil {
		return "", fmt.Errorf("failed to create branch %s: %w", branchName, err)
	}

	// Commit bootstrap artifacts
	if err := gitManager.CommitArtifacts(ctx, artifacts, "Bootstrap project build system"); err != nil {
		return "", fmt.Errorf("failed to commit artifacts: %w", err)
	}

	return branchName, nil
}

// autoMergeBootstrap automatically merges the bootstrap branch to the base branch
func (p *Phase) autoMergeBootstrap(ctx context.Context, branchName string) (bool, error) {
	gitManager := NewGitManager(p.projectRoot, p.logger)

	// Merge bootstrap branch to base branch
	if err := gitManager.MergeBranch(ctx, branchName, p.config.BaseBranch); err != nil {
		return false, fmt.Errorf("failed to merge %s to %s: %w", branchName, p.config.BaseBranch, err)
	}

	// Clean up bootstrap branch
	if err := gitManager.DeleteBranch(ctx, branchName); err != nil {
		p.logger.Warn("Failed to delete bootstrap branch %s: %v", branchName, err)
	}

	return true, nil
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

// ensureGitRepository ensures we have a Git repository, cloning if necessary
func (p *Phase) ensureGitRepository(ctx context.Context) error {
	gitManager := NewGitManager(p.projectRoot, p.logger)

	// Check if we're already in a Git repository
	if gitManager.IsGitRepository() {
		p.logger.Info("Already in a Git repository")
		return nil
	}

	// If no repository URL is configured, this is a hard error
	if p.config.RepoURL == "" {
		return fmt.Errorf("not in a Git repository and no repository URL configured - bootstrap requires Git for branch creation and commits")
	}

	// Clone the repository
	p.logger.Info("Not in a Git repository, cloning from: %s", p.config.RepoURL)

	if err := gitManager.Clone(ctx, p.config.RepoURL); err != nil {
		return fmt.Errorf("failed to clone repository %s: %w", p.config.RepoURL, err)
	}

	p.logger.Info("Successfully cloned repository to: %s", p.projectRoot)
	return nil
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
