package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"orchestrator/internal/kernel"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/mirror"
	"orchestrator/pkg/persistence"
	"orchestrator/pkg/tools"
)

// Version information - set by goreleaser via ldflags.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Parse command line flags
	var (
		gitRepo      = flag.String("git-repo", "", "Git repository URL (optional)")
		specFile     = flag.String("spec-file", "", "Path to specification file")
		noWebUI      = flag.Bool("nowebui", false, "Disable web UI")
		bootstrap    = flag.Bool("bootstrap", false, "[DEPRECATED] Bootstrap mode is deprecated - PM mode is now default")
		projectDir   = flag.String("projectdir", ".", "Project directory")
		tee          = flag.Bool("tee", false, "Output logs to both console and file (default: file only)")
		showVersion  = flag.Bool("version", false, "Show version information")
		continueMode = flag.Bool("continue", false, "Resume from the most recent shutdown session")
	)
	flag.Parse()

	// Handle version flag
	if *showVersion {
		fmt.Printf("maestro %s\n", version)
		fmt.Printf("  commit: %s\n", commit)
		fmt.Printf("  built:  %s\n", date)
		os.Exit(0)
	}

	// User-friendly startup message
	fmt.Println("⏳ Starting up...")

	// Warn if deprecated bootstrap flag is used
	if *bootstrap {
		fmt.Println("⚠️  WARNING: The -bootstrap flag is deprecated and will be removed in a future version.")
		fmt.Println("   PM mode is now the default behavior. This flag is ignored.")
	}

	// Initialize log file rotation BEFORE any logging occurs
	// This ensures all subsequent logs (including config loading) are captured
	logsDir := filepath.Join(*projectDir, ".maestro", "logs")
	if err := logx.InitializeLogFile(logsDir, 4, *tee); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to initialize log file: %v\n", err)
		os.Exit(1)
	}

	// Run main logic and get exit code
	// PM mode is now always enabled (bootstrap flag ignored)
	exitCode := run(*projectDir, *gitRepo, *specFile, *noWebUI, *continueMode)

	// Close log file before exiting
	if closeErr := logx.CloseLogFile(); closeErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to close log file: %v\n", closeErr)
	}

	os.Exit(exitCode)
}

// run contains the main application logic and returns an exit code.
// This allows defers in main() to execute before os.Exit is called.
func run(projectDir, gitRepo, specFile string, noWebUI, continueMode bool) int {
	// Warn if projectdir is using default value
	if projectDir == "." {
		config.LogInfo("⚠️  -projectdir not set. Using the current directory.")
	}

	// Universal setup (Steps 1-3): Always run these regardless of mode
	_, err := setupProjectInfrastructure(projectDir, gitRepo, specFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Project setup failed: %v\n", err)
		return 1
	}

	// Handle secrets file decryption if present (loads credentials into memory)
	if err := handleSecretsDecryption(projectDir); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to handle secrets: %v\n", err)
		return 1
	}

	// Check for resume mode
	if continueMode {
		config.LogInfo("🔄 Resume mode enabled - looking for resumable session...")
		if err := runResumeMode(projectDir, noWebUI); err != nil {
			fmt.Fprintf(os.Stderr, "Resume failed: %v\n", err)
			return 1
		}
		return 0
	}

	// PM mode is now the default (and only) mode
	config.LogInfo("🚀 Starting Maestro (PM mode enabled by default)")
	config.LogInfo("📁 Working directory: %s", projectDir)

	// Always run main mode (which includes PM)
	if err := runMainMode(projectDir, specFile, noWebUI); err != nil {
		fmt.Fprintf(os.Stderr, "Maestro failed: %v\n", err)
		return 1
	}

	return 0
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

	// 4. Create or update git mirror if git config exists and is valid
	// Use bootstrap detector to validate the git URL first
	// If invalid, skip mirror creation and let PM bootstrap handle it
	if cfg.Git != nil && cfg.Git.RepoURL != "" {
		detector := tools.NewBootstrapDetector(projectDir)
		reqs, detectErr := detector.Detect(context.Background())

		if detectErr == nil && !reqs.NeedsGitRepo {
			// Git repo is configured and valid - create/update mirror
			mirrorMgr := mirror.NewManager(projectDir)
			if _, err := mirrorMgr.EnsureMirror(context.Background()); err != nil {
				return fmt.Errorf("failed to setup git mirror: %w", err)
			}
		} else {
			// Git repo is invalid or missing - skip mirror creation
			// PM bootstrap will handle this
			config.LogInfo("⚠️  Git repository not configured or invalid - skipping mirror creation (PM will bootstrap)")
		}
	}

	// 5. Pre-create all agent workspace directories for container mounting
	// Pre-create architect and PM directories first
	config.LogInfo("📁 Pre-creating agent workspace directories...")
	agentDirs := []string{"architect-001", "pm-001"}

	// Add coder directories
	if cfg.Agents != nil && cfg.Agents.MaxCoders > 0 {
		for i := 1; i <= cfg.Agents.MaxCoders; i++ {
			agentDirs = append(agentDirs, fmt.Sprintf("coder-%03d", i))
		}
	}

	// Create all directories
	for _, dir := range agentDirs {
		agentPath := filepath.Join(projectDir, dir)
		if err := os.MkdirAll(agentPath, 0755); err != nil {
			return fmt.Errorf("failed to create workspace directory %s: %w", dir, err)
		}
	}
	config.LogInfo("✅ Created %d agent workspace directories", len(agentDirs))

	config.LogInfo("✅ Project infrastructure verification completed for %s", projectDir)
	return nil
}

func runMainMode(projectDir, specFile string, noWebUI bool) error {
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

	// Determine WebUI status: read from config, but respect -nowebui flag override
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	webUIEnabled := false
	if cfg.WebUI != nil && cfg.WebUI.Enabled && !noWebUI {
		webUIEnabled = true
	}

	// Create and run main flow
	flow := NewMainFlow(specFile, webUIEnabled)
	return flow.Run(ctx, k)
}

// runResumeMode handles resuming from a previous shutdown session.
// It finds the most recent resumable session and restores agent state.
func runResumeMode(projectDir string, noWebUI bool) error {
	logger := logx.NewLogger("maestro-resume")
	logger.Info("Starting Maestro in resume mode")

	// Open database to query for resumable sessions
	dbPath := filepath.Join(projectDir, config.ProjectConfigDir, "maestro.db")
	db, err := persistence.InitializeDatabase(dbPath)
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Find the most recent resumable session
	session, err := persistence.GetMostRecentResumableSession(db)
	if err != nil {
		return fmt.Errorf("failed to find resumable session: %w", err)
	}

	if session == nil {
		fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
		fmt.Println("║                    ❌ No Resumable Session Found                   ║")
		fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
		fmt.Println("║  There are no sessions with 'shutdown' status to resume.           ║")
		fmt.Println("║                                                                    ║")
		fmt.Println("║  Start a new session with: maestro                                 ║")
		fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
		return nil
	}

	// Display resume information
	fmt.Println()
	fmt.Println("╔════════════════════════════════════════════════════════════════════╗")
	fmt.Println("║                    🔄 Resuming Previous Session                    ║")
	fmt.Println("╠════════════════════════════════════════════════════════════════════╣")
	fmt.Printf("║  Session ID: %-54s ║\n", session.SessionID)
	fmt.Printf("║  Started:    %-54s ║\n", session.StartedAt)
	fmt.Println("╚════════════════════════════════════════════════════════════════════╝")
	fmt.Println()

	// Restore session ID in config (this is the key - we reuse the session ID)
	if err := config.SetSessionID(session.SessionID); err != nil {
		return fmt.Errorf("failed to restore session ID: %w", err)
	}

	// Update session status to 'active' to indicate we're running
	if err := persistence.UpdateSessionStatus(db, session.SessionID, persistence.SessionStatusActive); err != nil {
		return fmt.Errorf("failed to update session status: %w", err)
	}

	logger.Info("Resuming session %s", session.SessionID)

	// Now run the resume flow which will create agents and restore their state
	return runResumeFlow(projectDir, noWebUI, session.SessionID)
}

// runResumeFlow creates the kernel, agents, restores state, and runs the main loop.
func runResumeFlow(projectDir string, noWebUI bool, sessionID string) error {
	logger := logx.NewLogger("maestro-resume")

	// Get configuration (session ID is already set)
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	config.LogInfo("🔄 Resuming session: %s", sessionID)

	// Create context with signal handling
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	_ = cancel

	// Initialize kernel with shared infrastructure
	k, err := kernel.NewKernel(ctx, &cfg, projectDir)
	if err != nil {
		return fmt.Errorf("failed to create kernel: %w", err)
	}
	defer func() {
		if stopErr := k.Stop(); stopErr != nil {
			logger.Error("Error stopping kernel: %v", stopErr)
		}
	}()

	// Mark kernel as resuming to skip session creation (session already exists)
	k.SetResuming(true)

	// Start kernel services
	if err := k.Start(); err != nil {
		return fmt.Errorf("failed to start kernel: %w", err)
	}

	// Determine WebUI status
	webUIEnabled := false
	if cfg.WebUI != nil && cfg.WebUI.Enabled && !noWebUI {
		webUIEnabled = true
	}

	// Create and run resume flow
	flow := NewResumeFlow(sessionID, webUIEnabled)
	return flow.Run(ctx, k)
}

// initializeKernel consolidates the common kernel initialization logic.
// Config must already be loaded via setupProjectInfrastructure().
func initializeKernel(projectDir string) (*kernel.Kernel, context.Context, error) {
	// Generate session ID for this orchestrator run (used for database session isolation)
	if sessionErr := config.GenerateSessionID(); sessionErr != nil {
		return nil, nil, fmt.Errorf("failed to generate session ID: %w", sessionErr)
	}

	// Get configuration AFTER generating session ID (session ID is stored in global config)
	cfg, err := config.GetConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get config: %w", err)
	}
	config.LogInfo("📋 Session ID: %s", cfg.SessionID)

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
// Mirror management functions have been moved to pkg/mirror package
