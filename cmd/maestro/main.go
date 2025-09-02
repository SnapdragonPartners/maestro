package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"orchestrator/internal/kernel"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

func main() {
	// Parse command line flags
	var (
		gitRepo    = flag.String("git-repo", "", "Git repository URL for bootstrap mode")
		specFile   = flag.String("spec-file", "", "Path to specification file")
		webUI      = flag.Bool("webui", false, "Enable web UI for main mode")
		bootstrap  = flag.Bool("bootstrap", false, "Run in bootstrap mode")
		projectDir = flag.String("projectdir", ".", "Project directory")
	)
	flag.Parse()

	// Universal setup (Steps 1-3): Always run these regardless of mode
	configWasCreated, err := setupProjectInfrastructure(*projectDir, *gitRepo, *specFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Project setup failed: %v\n", err)
		os.Exit(1)
	}

	// Determine mode - auto-offer bootstrap if config was created from defaults
	shouldBootstrap := *bootstrap || configWasCreated
	if shouldBootstrap && !*bootstrap {
		fmt.Printf("New configuration created - entering bootstrap mode to set up repository\n")
	}

	if shouldBootstrap {
		if err := runBootstrapMode(*projectDir, *gitRepo, *specFile); err != nil {
			fmt.Fprintf(os.Stderr, "Bootstrap failed: %v\n", err)
			os.Exit(1)
		}
	} else {
		if err := runMainMode(*projectDir, *specFile, *webUI); err != nil {
			fmt.Fprintf(os.Stderr, "Main mode failed: %v\n", err)
			os.Exit(1)
		}
	}
}

// setupProjectInfrastructure handles universal setup steps 1-3:
// 1. Load/create config, 2. Merge command line params, 3. Run VerifyProject
// Returns whether config was created from defaults (indicating need for bootstrap).
func setupProjectInfrastructure(projectDir, gitRepo, specFile string) (bool, error) {
	// Step 1: Check if config exists before loading
	configPath := filepath.Join(projectDir, ".maestro", "config.json")
	configWasCreated := false
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		configWasCreated = true
	}

	// Load or create config
	if err := config.LoadConfig(projectDir); err != nil {
		return false, fmt.Errorf("failed to load config: %w", err)
	}

	// Step 2: Merge command line parameters into config
	if err := mergeCommandLineParams(gitRepo, specFile); err != nil {
		return false, fmt.Errorf("failed to merge command line params: %w", err)
	}

	// Step 3: Run VerifyProject (auto-fixes infrastructure issues)
	if err := verifyProject(projectDir); err != nil {
		return false, fmt.Errorf("project verification failed: %w", err)
	}

	return configWasCreated, nil
}

// mergeCommandLineParams updates config with command line arguments.
func mergeCommandLineParams(gitRepo, specFile string) error {
	// Get current config
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Update git repo URL if provided
	if gitRepo != "" && cfg.Git != nil {
		cfg.Git.RepoURL = gitRepo
		if err := config.UpdateGit(cfg.Git); err != nil {
			return fmt.Errorf("failed to update git config: %w", err)
		}
	}

	// TODO: Handle specFile parameter if needed for config
	_ = specFile

	return nil
}

// verifyProject implements VerifyProject() - auto-fixes deterministic infrastructure.
//
//nolint:cyclop // TODO: refactor to reduce complexity - extract helper functions for each setup step
func verifyProject(projectDir string) error {
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// 1. Create .maestro/ directory structure
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		return fmt.Errorf("failed to create .maestro directory: %w", mkdirErr)
	}

	// No subdirectories needed in .maestro/ - it only contains config and database

	// 2. Validate/create database
	dbPath := filepath.Join(maestroDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("failed to initialize database: %w", err)
	}
	if closeErr := db.Close(); closeErr != nil {
		return fmt.Errorf("failed to close database: %w", closeErr)
	}

	// 3. Create CODER.md and ARCHITECT.md system prompt files
	coderPath := filepath.Join(maestroDir, "CODER.md")
	if _, err := os.Stat(coderPath); os.IsNotExist(err) {
		coderContent := `# CODER.md

This file provides guidance to Coder agents when working with code in this repository.

## Project Overview

This project uses Maestro for multi-agent AI development coordination.

## Development Commands

Follow the build and test commands specified in the project configuration.

## Code Style

Follow existing patterns and conventions in the codebase.
`
		if err := os.WriteFile(coderPath, []byte(coderContent), 0644); err != nil {
			return fmt.Errorf("failed to create CODER.md: %w", err)
		}
	}

	architectPath := filepath.Join(maestroDir, "ARCHITECT.md")
	if _, err := os.Stat(architectPath); os.IsNotExist(err) {
		architectContent := `# ARCHITECT.md

This file provides guidance to Architect agents when coordinating development in this repository.

## Project Architecture

This project uses Maestro for coordinated AI development.

## Story Management

Generate focused, well-scoped stories with clear acceptance criteria.
`
		if err := os.WriteFile(architectPath, []byte(architectContent), 0644); err != nil {
			return fmt.Errorf("failed to create ARCHITECT.md: %w", err)
		}
	}

	// 4. Create git mirror if git config exists
	if cfg.Git != nil && cfg.Git.RepoURL != "" {
		// Use the actual .mirrors directory in projectDir (not .maestro/mirrors)
		mirrorDir := filepath.Join(projectDir, ".mirrors")
		if err := os.MkdirAll(mirrorDir, 0755); err != nil {
			return fmt.Errorf("failed to create .mirrors directory: %w", err)
		}

		// Extract repo name from URL for mirror directory
		repoName := extractRepoName(cfg.Git.RepoURL)
		repoMirrorPath := filepath.Join(mirrorDir, repoName)

		// Check if mirror already exists
		if _, err := os.Stat(filepath.Join(repoMirrorPath, ".git")); os.IsNotExist(err) {
			// Clone as bare mirror
			if err := cloneGitMirror(cfg.Git.RepoURL, repoMirrorPath); err != nil {
				return fmt.Errorf("failed to create git mirror: %w", err)
			}
		}
	}

	fmt.Printf("Project infrastructure verification completed for %s\n", projectDir)
	return nil
}

func runBootstrapMode(projectDir, gitRepo, specFile string) error {
	logger := logx.NewLogger("maestro-bootstrap")
	logger.Info("Starting Maestro in bootstrap mode")

	// Initialize common kernel infrastructure
	k, ctx, err := initializeKernel(projectDir)
	if err != nil {
		return fmt.Errorf("failed to initialize kernel: %w", err)
	}
	defer func() {
		if stopErr := k.Stop(); stopErr != nil {
			logger.Error("Error stopping kernel: %v", stopErr)
		}
	}()

	// Create and run bootstrap flow
	flow := NewBootstrapFlow(gitRepo, specFile)
	return flow.Run(ctx, k)
}

func runMainMode(projectDir, specFile string, webUI bool) error {
	logger := logx.NewLogger("maestro-main")
	logger.Info("Starting Maestro in main mode")

	// Initialize common kernel infrastructure
	k, ctx, err := initializeKernel(projectDir)
	if err != nil {
		return fmt.Errorf("failed to initialize kernel: %w", err)
	}
	defer func() {
		if stopErr := k.Stop(); stopErr != nil {
			logger.Error("Error stopping kernel: %v", stopErr)
		}
	}()

	// Create and run main flow
	flow := NewMainFlow(specFile, webUI)
	return flow.Run(ctx, k)
}

// initializeKernel consolidates the common kernel initialization logic.
// Config must already be loaded via setupProjectInfrastructure().
func initializeKernel(projectDir string) (*kernel.Kernel, context.Context, error) {
	// Get already-loaded configuration (no reload needed)
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get config: %w", err)
	}

	// Create context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	_ = cancel // Will be called when context is cancelled

	// Initialize kernel with shared infrastructure
	k, err := kernel.NewKernel(ctx, &cfg, projectDir)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create kernel: %w", err)
	}

	// Start kernel services
	if err := k.Start(); err != nil {
		return nil, nil, fmt.Errorf("failed to start kernel: %w", err)
	}

	return k, ctx, nil
}

// extractRepoName extracts the repository name from a Git URL.
func extractRepoName(repoURL string) string {
	// Remove .git suffix if present
	repoURL = strings.TrimSuffix(repoURL, ".git")

	// Extract the last path component
	parts := strings.Split(repoURL, "/")
	if len(parts) == 0 {
		return "repo"
	}

	repoName := parts[len(parts)-1]
	if repoName == "" {
		return "repo"
	}

	return repoName
}

// cloneGitMirror creates a bare git mirror clone of the repository.
func cloneGitMirror(repoURL, mirrorPath string) error {
	cmd := exec.Command("git", "clone", "--mirror", repoURL, mirrorPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("git clone --mirror failed: %w", err)
	}
	return nil
}
