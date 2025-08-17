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

// BootstrapFailureType categorizes verification failures for bootstrap generation.
type BootstrapFailureType string

const (
	// BootstrapFailureBuildSystem indicates missing or invalid Makefile targets.
	BootstrapFailureBuildSystem BootstrapFailureType = "build_system"
	// BootstrapFailureContainer indicates container validation failures.
	BootstrapFailureContainer BootstrapFailureType = "container"
	// BootstrapFailureBinarySize indicates large file violations.
	BootstrapFailureBinarySize BootstrapFailureType = "binary_size"
	// BootstrapFailureGitAccess indicates git mirror or worktree issues.
	BootstrapFailureGitAccess BootstrapFailureType = "git_access"
	// BootstrapFailureInfrastructure indicates maestro directory, config, or database issues.
	BootstrapFailureInfrastructure BootstrapFailureType = "infrastructure"
	// BootstrapFailureExternalTools indicates missing required tools.
	BootstrapFailureExternalTools BootstrapFailureType = "external_tools"
)

// BootstrapFailure represents a structured failure that can be used for bootstrap spec generation.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type BootstrapFailure struct {
	Type        BootstrapFailureType `json:"type"`        // Category of failure
	Component   string               `json:"component"`   // Specific component that failed (e.g., "makefile", "docker_image")
	Description string               `json:"description"` // Human-readable description
	Details     map[string]string    `json:"details"`     // Additional structured data for remediation
	Priority    int                  `json:"priority"`    // Priority for fix ordering (1=highest)
}

// VerifyOptions configures workspace verification behavior.
type VerifyOptions struct {
	Logger  *logx.Logger  // Logger for verification process
	Timeout time.Duration // Upper bound for long-running steps
	Fast    bool          // Skip expensive checks (build, docker ping, etc.)
}

// VerifyReport contains the results of workspace verification.
//
//nolint:govet // Logical field grouping preferred over memory optimization
type VerifyReport struct {
	Durations         map[string]time.Duration   // Step timings for performance telemetry
	Warnings          []string                   // Non-fatal diagnostics (missing gh, docker, etc.)
	Failures          []string                   // Fatal errors with context (legacy)
	BootstrapFailures []BootstrapFailure         `json:"bootstrap_failures"`           // Structured failures for bootstrap generation
	BinarySizeResult  *BinarySizeCheckResult     `json:"binary_size_result,omitempty"` // Binary size check results
	ContainerResult   *ContainerValidationResult `json:"container_result,omitempty"`   // Container validation results
	OK                bool                       // High-level success flag
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
		OK:                true,
		Warnings:          []string{},
		Failures:          []string{},
		BootstrapFailures: []BootstrapFailure{},
		Durations:         map[string]time.Duration{},
	}

	// Helper functions for reporting
	fail := func(msg string, args ...interface{}) {
		formatted := fmt.Sprintf(msg, args...)
		rep.Failures = append(rep.Failures, formatted)
		rep.OK = false
		opts.Logger.Error("Verification failure: %s", formatted)
	}

	bootstrapFail := func(failureType BootstrapFailureType, component, description string, details map[string]string, priority int) {
		failure := BootstrapFailure{
			Type:        failureType,
			Component:   component,
			Description: description,
			Details:     details,
			Priority:    priority,
		}
		rep.BootstrapFailures = append(rep.BootstrapFailures, failure)
		// Also add to legacy failures for backward compatibility
		fail(description)
	}

	warn := func(msg string, args ...interface{}) {
		formatted := fmt.Sprintf(msg, args...)
		rep.Warnings = append(rep.Warnings, formatted)
		opts.Logger.Warn("Verification warning: %s", formatted)
	}

	opts.Logger.Info("Starting workspace verification: %s", projectDir)

	// 1. Maestro Infrastructure
	start := time.Now()
	if err := verifyMaestroInfrastructure(projectDir, fail, bootstrapFail); err != nil {
		return rep, fmt.Errorf("maestro infrastructure check failed: %w", err)
	}
	rep.Durations["infra"] = time.Since(start)

	// 2. Git Mirror
	start = time.Now()
	if err := verifyGitMirror(ctx, projectDir, opts.Timeout, fail, bootstrapFail); err != nil {
		return rep, fmt.Errorf("git mirror check failed: %w", err)
	}
	rep.Durations["mirror"] = time.Since(start)

	// 3. Build System (optional for fast mode, and only if infrastructure is OK)
	if !opts.Fast && len(rep.Failures) == 0 {
		start = time.Now()
		if err := verifyBuildSystem(ctx, projectDir, opts.Timeout, fail, bootstrapFail); err != nil {
			return rep, fmt.Errorf("build system check failed: %w", err)
		}
		rep.Durations["build"] = time.Since(start)
	}

	// 4. Binary Size Check (Phase 1 extension)
	start = time.Now()
	verifyBinarySizes(projectDir, rep, fail, warn, bootstrapFail, opts.Logger)
	rep.Durations["binary_size"] = time.Since(start)

	// 5. Container Validation (Phase 1 extension) - only if not in fast mode
	if !opts.Fast {
		start = time.Now()
		verifyContainerSetup(ctx, rep, fail, bootstrapFail, opts)
		rep.Durations["container"] = time.Since(start)
	}

	// 6. External Tools (warnings only, never fatal)
	start = time.Now()
	verifyExternalTools(warn)
	rep.Durations["tools"] = time.Since(start)

	opts.Logger.Info("Workspace verification completed: ok=%v, warnings=%d, failures=%d",
		rep.OK, len(rep.Warnings), len(rep.Failures))

	return rep, nil
}

// BootstrapFailFunc represents a function that reports structured bootstrap failures.
type BootstrapFailFunc func(BootstrapFailureType, string, string, map[string]string, int)

// verifyMaestroInfrastructure checks .maestro directory, config, and database.

func verifyMaestroInfrastructure(projectDir string, fail func(string, ...interface{}), bootstrapFail BootstrapFailFunc) error {
	maestroDir := filepath.Join(projectDir, config.ProjectConfigDir)

	// Check .maestro directory exists
	if _, err := os.Stat(maestroDir); err != nil {
		fail("missing %s directory: %v", config.ProjectConfigDir, err)
		return nil // Expected failure, not runtime error
	}

	// Check config.json exists and is valid
	configPath := filepath.Join(maestroDir, config.ProjectConfigFilename)
	if err := validateConfigJSON(configPath, fail, bootstrapFail); err != nil {
		return err // Runtime error during validation
	}

	// Check database exists and has correct schema
	dbPath := filepath.Join(maestroDir, config.DatabaseFilename)
	if err := validateDatabaseSchema(dbPath, fail, bootstrapFail); err != nil {
		return err // Runtime error during validation
	}

	return nil
}

// validateConfigJSON checks if the config file exists and contains valid JSON.
func validateConfigJSON(configPath string, fail func(string, ...interface{}), _ BootstrapFailFunc) error {
	if _, err := os.Stat(configPath); err != nil {
		fail("missing config file: %s", configPath)
		return fmt.Errorf("config file not found: %w", err)
	}

	// Try to load and parse the config
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	var config config.Config
	if err := json.Unmarshal(data, &config); err != nil {
		fail("invalid JSON in config file %s: %v", configPath, err)
		return nil
	}

	// Basic validation - check required fields
	if config.SchemaVersion == "" {
		fail("config missing schema_version field")
	}
	// Check for git repository URL in new structure
	if config.Git != nil && config.Git.RepoURL == "" {
		fail("config missing git.repo_url field")
	}

	return nil
}

// validateDatabaseSchema checks if the database exists and has the correct schema.
func validateDatabaseSchema(dbPath string, fail func(string, ...interface{}), _ BootstrapFailFunc) error {
	if _, err := os.Stat(dbPath); err != nil {
		fail("missing database file: %s", dbPath)
		return nil
	}

	// Open database and check schema version (using same connection string as persistence layer)
	db, err := sql.Open("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=ON&_journal_mode=WAL", dbPath))
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
func verifyGitMirror(ctx context.Context, projectDir string, timeout time.Duration, fail func(string, ...interface{}), bootstrapFail BootstrapFailFunc) error {
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

	// Check if target branch exists
	if err := verifyTargetBranch(gitCtx, gitMirrorPath, bootstrapFail); err != nil {
		return err
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
func verifyBuildSystem(ctx context.Context, projectDir string, timeout time.Duration, fail func(string, ...interface{}), bootstrapFail BootstrapFailFunc) error {
	// Config should already be loaded

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
			bootstrapFail(BootstrapFailureBuildSystem, "makefile",
				fmt.Sprintf("Makefile missing or invalid '%s' target: %v", target, err),
				map[string]string{
					"target": target,
					"error":  err.Error(),
					"action": "create_makefile_target",
				}, 1) // High priority - blocking development
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
	if !config.HasGitHubToken() {
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

// verifyBinarySizes performs binary size checking and reports results.
func verifyBinarySizes(projectDir string, rep *VerifyReport, _, warn func(string, ...interface{}), bootstrapFail BootstrapFailFunc, logger *logx.Logger) {
	binarySizeResult, err := CheckBinarySizes(projectDir)
	if err != nil {
		logger.Error("Binary size check failed: %v", err)
		// Don't fail verification for binary size check errors, just warn
		warn("Binary size check failed: %v", err)
		return
	}

	rep.BinarySizeResult = binarySizeResult
	// Report violations as structured failures
	if binarySizeResult.HasViolations() {
		for _, file := range binarySizeResult.OversizeFiles {
			bootstrapFail(BootstrapFailureBinarySize, "large_file",
				fmt.Sprintf("File %s (%s) exceeds 100MB limit - use Git LFS or remove", file.Path, FormatFileSize(file.Size)),
				map[string]string{
					"file_path":  file.Path,
					"file_size":  FormatFileSize(file.Size),
					"size_bytes": fmt.Sprintf("%d", file.Size),
					"action":     "setup_git_lfs",
					"threshold":  "100MB",
				}, 2) // Medium priority - doesn't block development but blocks deployment
		}
	}
	// Report warnings for large files
	if binarySizeResult.HasWarnings() {
		for _, file := range binarySizeResult.LargeFiles {
			warn("Large file %s (%s) - consider Git LFS",
				file.Path, FormatFileSize(file.Size))
		}
	}
}

// verifyContainerSetup performs container validation and reports results.
func verifyContainerSetup(ctx context.Context, rep *VerifyReport, _ func(string, ...interface{}), bootstrapFail BootstrapFailFunc, opts VerifyOptions) {
	containerResult, containerErr := ValidateContainer(ctx, opts.Timeout)
	if containerErr != nil {
		bootstrapFail(BootstrapFailureContainer, "container_setup",
			fmt.Sprintf("Container validation failed: %v", containerErr),
			map[string]string{
				"error":  containerErr.Error(),
				"action": "fix_container_config",
			}, 1) // High priority - blocks all development
		return
	}

	rep.ContainerResult = containerResult

	// Handle different validation states
	switch containerResult.State {
	case ValidationPass:
		opts.Logger.Info("Container validation passed: %s", containerResult.ValidationMethod)

	case ValidationNeedBootstrap:
		// Bootstrap needed - this is expected for detect/dockerfile modes
		details := map[string]string{
			"reason": containerResult.Reason,
			"action": "bootstrap_container",
		}
		if containerResult.Details != nil {
			details["details"] = containerResult.Details.Error()
		}

		bootstrapFail(BootstrapFailureContainer, "container_bootstrap",
			fmt.Sprintf("Container bootstrap required: %s", containerResult.Reason),
			details, 1) // High priority - container setup needed

	case ValidationConfigError:
		// Configuration error - user needs to fix config
		details := map[string]string{
			"reason": containerResult.Reason,
			"action": "fix_container_config",
		}
		if containerResult.Details != nil {
			details["error"] = containerResult.Details.Error()
		}

		bootstrapFail(BootstrapFailureContainer, "container_config_error",
			fmt.Sprintf("Container configuration error: %s", containerResult.Reason),
			details, 1) // High priority - blocks all development

	case ValidationTransientError:
		// Transient error - may be retryable
		details := map[string]string{
			"reason": containerResult.Reason,
			"action": "retry_container_validation",
		}
		if containerResult.Details != nil {
			details["error"] = containerResult.Details.Error()
		}

		bootstrapFail(BootstrapFailureContainer, "container_transient_error",
			fmt.Sprintf("Container transient error: %s", containerResult.Reason),
			details, 2) // Medium priority - may resolve on retry
	}
}

// GetBootstrapFailuresByType groups bootstrap failures by type for easier processing.
func (r *VerifyReport) GetBootstrapFailuresByType() map[BootstrapFailureType][]BootstrapFailure {
	grouped := make(map[BootstrapFailureType][]BootstrapFailure)
	for i := range r.BootstrapFailures {
		failure := &r.BootstrapFailures[i]
		grouped[failure.Type] = append(grouped[failure.Type], *failure)
	}
	return grouped
}

// GetBootstrapFailuresByPriority returns bootstrap failures sorted by priority (1=highest).
func (r *VerifyReport) GetBootstrapFailuresByPriority() []BootstrapFailure {
	failures := make([]BootstrapFailure, len(r.BootstrapFailures))
	copy(failures, r.BootstrapFailures)

	// Sort by priority (1=highest priority first)
	for i := 0; i < len(failures); i++ {
		for j := i + 1; j < len(failures); j++ {
			if failures[i].Priority > failures[j].Priority {
				failures[i], failures[j] = failures[j], failures[i]
			}
		}
	}
	return failures
}

// RequiresBootstrap returns true if there are any bootstrap failures that require remediation.
func (r *VerifyReport) RequiresBootstrap() bool {
	return len(r.BootstrapFailures) > 0
}

// GenerateBootstrapSummary creates a human-readable summary of bootstrap requirements.
func (r *VerifyReport) GenerateBootstrapSummary() string {
	if !r.RequiresBootstrap() {
		return "✅ No bootstrap required - all verifications passed"
	}

	var summary strings.Builder
	summary.WriteString("🔧 Bootstrap Required - Infrastructure Issues Found\n\n")

	grouped := r.GetBootstrapFailuresByType()
	priorities := r.GetBootstrapFailuresByPriority()

	// Summary by priority
	summary.WriteString("## Issues by Priority\n\n")
	for i := range priorities {
		failure := &priorities[i]
		priority := "Low"
		if failure.Priority == 1 {
			priority = "🔴 Critical"
		} else if failure.Priority == 2 {
			priority = "🟡 High"
		} else if failure.Priority == 3 {
			priority = "🟢 Medium"
		}

		summary.WriteString(fmt.Sprintf("**%s**: %s (%s)\n", priority, failure.Description, failure.Component))
	}

	// Details by category
	summary.WriteString("\n## Remediation Details\n\n")
	for failureType, failures := range grouped {
		typeName := string(failureType)
		summary.WriteString(fmt.Sprintf("### %s\n\n", strings.Title(strings.ReplaceAll(typeName, "_", " "))))

		for i := range failures {
			failure := &failures[i]
			summary.WriteString(fmt.Sprintf("- **Component**: %s\n", failure.Component))
			summary.WriteString(fmt.Sprintf("  **Issue**: %s\n", failure.Description))
			if action, ok := failure.Details["action"]; ok {
				summary.WriteString(fmt.Sprintf("  **Action**: %s\n", action))
			}
			summary.WriteString("\n")
		}
	}

	return summary.String()
}

// verifyTargetBranch checks if the target branch specified in project config exists in the git repository.
func verifyTargetBranch(ctx context.Context, gitMirrorPath string, bootstrapFail BootstrapFailFunc) error {
	// Get target branch from config (should already be loaded)
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("config not loaded for branch verification: %w", err)
	}

	targetBranch := ""
	if cfg.Git != nil {
		targetBranch = cfg.Git.TargetBranch
	}
	if targetBranch == "" {
		targetBranch = "main" // Default fallback
	}

	// Check if branch exists in the mirror (bare repos don't have remotes, check heads directly)
	if err := runGitCommand(ctx, gitMirrorPath, "show-ref", "--verify", "--quiet", "refs/heads/"+targetBranch); err != nil {
		// Branch doesn't exist - this requires bootstrap
		bootstrapFail(BootstrapFailureGitAccess, "missing_branch",
			fmt.Sprintf("Target branch '%s' does not exist in repository. Branch must be created and pushed.", targetBranch),
			map[string]string{
				"branch": targetBranch,
				"action": "create_branch",
				"remote": "origin",
			}, 1) // High priority - blocks development
		// Don't return error - this is handled by bootstrapFail
	}

	return nil
}
