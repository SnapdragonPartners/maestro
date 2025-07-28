// Package workspace provides workspace verification and validation functionality.
package workspace

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3" // SQLite driver

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/persistence"
)

// VerifyOptions configures workspace verification behavior.
type VerifyOptions struct {
	Logger  *logx.Logger  // Logger for verification process
	Timeout time.Duration // Upper bound for long-running steps
	Fast    bool          // Skip expensive checks (build, docker ping, etc.)
}

// VerifyReport contains the results of workspace verification.
type VerifyReport struct {
	Durations map[string]time.Duration // Step timings for performance telemetry
	Warnings  []string                 // Non-fatal diagnostics (missing gh, docker, etc.)
	Failures  []string                 // Fatal errors with context
	OK        bool                     // High-level success flag
}

// VerifyWorkspace performs comprehensive workspace verification.
// It checks maestro infrastructure, git mirrors, build system, and external tools.
func VerifyWorkspace(ctx context.Context, projectDir string, opts VerifyOptions) (*VerifyReport, error) {
	// Set defaults
	if opts.Logger == nil {
		opts.Logger = logx.NewLogger("verify")
	}
	if opts.Timeout == 0 {
		opts.Timeout = 30 * time.Second
	}

	rep := &VerifyReport{
		OK:        true,
		Warnings:  []string{},
		Failures:  []string{},
		Durations: map[string]time.Duration{},
	}

	// Helper functions for reporting
	fail := func(msg string, args ...interface{}) {
		formatted := fmt.Sprintf(msg, args...)
		rep.Failures = append(rep.Failures, formatted)
		rep.OK = false
		opts.Logger.Error("Verification failure: %s", formatted)
	}

	warn := func(msg string, args ...interface{}) {
		formatted := fmt.Sprintf(msg, args...)
		rep.Warnings = append(rep.Warnings, formatted)
		opts.Logger.Warn("Verification warning: %s", formatted)
	}

	opts.Logger.Info("Starting workspace verification: %s", projectDir)

	// 1. Maestro Infrastructure
	start := time.Now()
	if err := verifyMaestroInfrastructure(projectDir, fail); err != nil {
		return rep, fmt.Errorf("maestro infrastructure check failed: %w", err)
	}
	rep.Durations["infra"] = time.Since(start)

	// 2. Git Mirror
	start = time.Now()
	if err := verifyGitMirror(ctx, projectDir, opts.Timeout, fail); err != nil {
		return rep, fmt.Errorf("git mirror check failed: %w", err)
	}
	rep.Durations["mirror"] = time.Since(start)

	// 3. Build System (optional for fast mode, and only if infrastructure is OK)
	if !opts.Fast && len(rep.Failures) == 0 {
		start = time.Now()
		if err := verifyBuildSystem(ctx, projectDir, opts.Timeout, fail); err != nil {
			return rep, fmt.Errorf("build system check failed: %w", err)
		}
		rep.Durations["build"] = time.Since(start)
	}

	// 4. External Tools (warnings only, never fatal)
	start = time.Now()
	verifyExternalTools(warn)
	rep.Durations["tools"] = time.Since(start)

	opts.Logger.Info("Workspace verification completed: ok=%v, warnings=%d, failures=%d",
		rep.OK, len(rep.Warnings), len(rep.Failures))

	return rep, nil
}

// verifyMaestroInfrastructure checks .maestro directory, config, and database.
func verifyMaestroInfrastructure(projectDir string, fail func(string, ...interface{})) error {
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)

	// Check .maestro directory exists
	if _, err := os.Stat(maestroDir); err != nil {
		fail("missing %s directory: %v", config.ProjectConfigDir, err)
		return nil // Expected failure, not runtime error
	}

	// Check config.json exists and is valid
	configPath := filepath.Join(maestroDir, config.ProjectConfigFilename)
	if err := validateConfigJSON(configPath, fail); err != nil {
		return err // Runtime error during validation
	}

	// Check database exists and has correct schema
	dbPath := filepath.Join(maestroDir, "database.db")
	if err := validateDatabaseSchema(dbPath, fail); err != nil {
		return err // Runtime error during validation
	}

	return nil
}

// validateConfigJSON checks if the config file exists and contains valid JSON.
func validateConfigJSON(configPath string, fail func(string, ...interface{})) error {
	if _, err := os.Stat(configPath); err != nil {
		fail("missing config file: %s", configPath)
		return fmt.Errorf("config file not found: %w", err)
	}

	// Try to load and parse the config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var projectConfig config.ProjectConfig
	if err := json.Unmarshal(data, &projectConfig); err != nil {
		fail("invalid JSON in config file %s: %v", configPath, err)
		return nil
	}

	// Basic validation - check required fields
	if projectConfig.SchemaVersion == "" {
		fail("config missing schema_version field")
	}
	if projectConfig.Project.GitRepo == "" {
		fail("config missing project.git_repo field")
	}

	return nil
}

// validateDatabaseSchema checks if the database exists and has the correct schema.
func validateDatabaseSchema(dbPath string, fail func(string, ...interface{})) error {
	if _, err := os.Stat(dbPath); err != nil {
		fail("missing database file: %s", dbPath)
		return nil
	}

	// Open database and check schema version
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=ON", dbPath))
	if err != nil {
		return fmt.Errorf("failed to open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	// Test connection
	if pingErr := db.Ping(); pingErr != nil {
		fail("database connection failed: %v", pingErr)
		return nil
	}

	// Check schema version
	currentVersion, err := persistence.GetSchemaVersion(db)
	if err != nil {
		return fmt.Errorf("failed to get schema version: %w", err)
	}

	expectedVersion := persistence.CurrentSchemaVersion
	if currentVersion != expectedVersion {
		fail("database schema version mismatch: expected %d, found %d", expectedVersion, currentVersion)
	}

	return nil
}

// verifyGitMirror checks git mirror infrastructure and connectivity.
func verifyGitMirror(ctx context.Context, projectDir string, timeout time.Duration, fail func(string, ...interface{})) error {
	mirrorDir := filepath.Join(projectDir, ".mirrors")

	if _, err := os.Stat(mirrorDir); err != nil {
		fail("missing .mirrors directory: %v", err)
		return nil
	}

	// Find the git mirror repository (should be only one)
	entries, err := os.ReadDir(mirrorDir)
	if err != nil {
		return fmt.Errorf("failed to read mirrors directory: %w", err)
	}

	var gitMirrorPath string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".git") {
			gitMirrorPath = filepath.Join(mirrorDir, entry.Name())
			break
		}
	}

	if gitMirrorPath == "" {
		fail("no git mirror found in .mirrors directory")
		return nil
	}

	// Create context with timeout for git operations
	gitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Verify it's a valid git repository
	if err := runGitCommand(gitCtx, gitMirrorPath, "rev-parse", "--git-dir"); err != nil {
		fail("invalid git repository at %s: %v", gitMirrorPath, err)
		return nil
	}

	// Check if we can create and remove a temporary worktree
	tempWorktreePath := filepath.Join(gitMirrorPath, "tmp-verify")

	// Clean up any existing temp worktree
	_ = runGitCommand(gitCtx, gitMirrorPath, "worktree", "remove", "--force", tempWorktreePath)

	// Try to create temporary worktree
	if err := runGitCommand(gitCtx, gitMirrorPath, "worktree", "add", "--detach", "--no-checkout", tempWorktreePath, "HEAD"); err != nil {
		fail("cannot create git worktree: %v", err)
		return nil
	}

	// Clean up the temporary worktree
	_ = runGitCommand(gitCtx, gitMirrorPath, "worktree", "remove", "--force", tempWorktreePath)

	return nil
}

// verifyBuildSystem checks build targets in a temporary worktree.
func verifyBuildSystem(ctx context.Context, projectDir string, timeout time.Duration, fail func(string, ...interface{})) error {
	// Load config to get expected build commands
	configPath := filepath.Join(projectDir, config.ProjectConfigDir, config.ProjectConfigFilename)
	_, err := config.LoadProjectConfigFromPath(configPath)
	if err != nil {
		return fmt.Errorf("failed to load project config: %w", err)
	}

	// Find git mirror
	mirrorDir := filepath.Join(projectDir, ".mirrors")
	entries, err := os.ReadDir(mirrorDir)
	if err != nil {
		return fmt.Errorf("failed to read mirrors directory: %w", err)
	}

	var gitMirrorPath string
	for _, entry := range entries {
		if entry.IsDir() && strings.HasSuffix(entry.Name(), ".git") {
			gitMirrorPath = filepath.Join(mirrorDir, entry.Name())
			break
		}
	}

	if gitMirrorPath == "" {
		fail("no git mirror found for build verification")
		return nil
	}

	// Create temporary worktree
	buildCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tempWorktreePath := filepath.Join(gitMirrorPath, "tmp-build-verify")

	// Clean up any existing temp worktree
	_ = runGitCommand(buildCtx, gitMirrorPath, "worktree", "remove", "--force", tempWorktreePath)

	// Create worktree with checkout for build verification
	if err := runGitCommand(buildCtx, gitMirrorPath, "worktree", "add", "--detach", tempWorktreePath, "HEAD"); err != nil {
		fail("cannot create build verification worktree: %v", err)
		return nil
	}

	// Ensure cleanup happens
	defer func() {
		_ = runGitCommand(buildCtx, gitMirrorPath, "worktree", "remove", "--force", tempWorktreePath)
	}()

	// Check for Makefile
	makefilePath := filepath.Join(tempWorktreePath, "Makefile")
	if _, err := os.Stat(makefilePath); err != nil {
		fail("missing Makefile in repository")
		return nil
	}

	// Verify build targets exist (dry run)
	buildTargets := []string{"build", "test", "lint", "run"}
	for _, target := range buildTargets {
		if err := runMakeTarget(buildCtx, tempWorktreePath, target, true); err != nil {
			fail("Makefile missing or invalid '%s' target: %v", target, err)
		}
	}

	return nil
}

// verifyExternalTools checks for required external dependencies.
func verifyExternalTools(warn func(string, ...interface{})) {
	tools := map[string]string{
		"git":    "Git is required for repository operations",
		"docker": "Docker is required for containerized builds",
		"gh":     "GitHub CLI is required for pull request operations",
	}

	for tool, description := range tools {
		if err := which(tool); err != nil {
			warn("%s not found on PATH: %s", tool, description)
		}
	}

	// Check for GITHUB_TOKEN environment variable
	if _, ok := os.LookupEnv("GITHUB_TOKEN"); !ok {
		warn("GITHUB_TOKEN environment variable not set: GitHub operations may fail")
	}
}

// runGitCommand executes a git command with proper error handling.
func runGitCommand(ctx context.Context, workDir string, args ...string) error {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = workDir
	cmd.Env = append(os.Environ(),
		"GIT_SSH_COMMAND=ssh -o BatchMode=yes -o ConnectTimeout=10",
		"GIT_TERMINAL_PROMPT=0",
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("git %s failed: %w (output: %s)", strings.Join(args, " "), err, string(output))
	}
	return nil
}

// runMakeTarget tests if a Makefile target exists and can be executed.
func runMakeTarget(ctx context.Context, workDir, target string, dryRun bool) error {
	args := []string{target}
	if dryRun {
		args = append(args, "-n") // Dry run flag
	}

	cmd := exec.CommandContext(ctx, "make", args...)
	cmd.Dir = workDir

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("make %s failed: %w (output: %s)", target, err, string(output))
	}
	return nil
}

// which checks if a command is available on the system PATH.
func which(name string) error {
	_, err := exec.LookPath(name)
	if err != nil {
		return fmt.Errorf("command %s not found: %w", name, err)
	}
	return nil
}

// IsLockFileError checks if an error is related to git lock files.
func IsLockFileError(err error) bool {
	if err == nil {
		return false
	}
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "index.lock") ||
		strings.Contains(errStr, "locked") ||
		strings.Contains(errStr, "unable to create")
}

// RetryWithBackoff retries an operation with exponential backoff.
func RetryWithBackoff(ctx context.Context, maxRetries int, operation func() error) error {
	var lastErr error

	for i := 0; i < maxRetries; i++ {
		if err := operation(); err != nil {
			lastErr = err

			// If it's a lock file error, retry with backoff
			if IsLockFileError(err) && i < maxRetries-1 {
				backoff := time.Duration(1<<i) * 100 * time.Millisecond
				select {
				case <-time.After(backoff):
					continue
				case <-ctx.Done():
					return fmt.Errorf("context cancelled: %w", ctx.Err())
				}
			}

			// For other errors, don't retry
			return err
		}

		// Success
		return nil
	}

	return lastErr
}
