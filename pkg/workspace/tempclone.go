package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"orchestrator/pkg/logx"
)

// TempCloneOptions configures temporary clone behavior.
type TempCloneOptions struct {
	// Logger for clone operations (optional)
	Logger *logx.Logger
	// Branch to checkout (optional, uses default branch if empty)
	Branch string
	// Shallow clone (--depth=1) for faster operations
	Shallow bool
}

// WithTempClone creates a temporary clone from a git mirror, executes a callback, and cleans up.
//
// The temporary clone is created in <projectDir>/.tmp/clone-<timestamp>/ to ensure:
//   - All temporary files stay within project directory
//   - No conflicts with system /tmp (works on all platforms)
//   - Automatic cleanup even on errors or panics
//
// Parameters:
//   - ctx: Context for cancellation
//   - projectDir: Project directory (temp will be in <projectDir>/.tmp/)
//   - mirrorPath: Path to git mirror repository
//   - opts: Clone options (branch, shallow, logger)
//   - fn: Callback function that receives the temp clone path
//
// Returns: Error from callback or clone operations
//
// Usage:
//
//	err := workspace.WithTempClone(ctx, projectDir, mirrorPath, opts, func(clonePath string) error {
//	    // Work with temporary clone
//	    return someOperation(clonePath)
//	})
func WithTempClone(ctx context.Context, projectDir, mirrorPath string, opts TempCloneOptions, fn func(clonePath string) error) error {
	logger := opts.Logger
	if logger == nil {
		logger = logx.NewLogger("tempclone")
	}

	// Create temporary directory in project directory
	tempDir := filepath.Join(projectDir, ".tmp", fmt.Sprintf("clone-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Ensure cleanup on exit (even if panic occurs)
	defer func() {
		logger.Debug("Cleaning up temp clone: %s", tempDir)
		if err := os.RemoveAll(tempDir); err != nil {
			logger.Warn("Failed to clean up temp clone: %v", err)
		}
	}()

	// Build git clone command
	cloneArgs := []string{"clone"}
	if opts.Shallow {
		cloneArgs = append(cloneArgs, "--depth=1", "--no-single-branch")
	}
	cloneArgs = append(cloneArgs, mirrorPath, tempDir)

	logger.Debug("Cloning from mirror: %s to %s", mirrorPath, tempDir)
	cloneCmd := exec.CommandContext(ctx, "git", cloneArgs...)
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("failed to clone from mirror: %w\nOutput: %s", err, string(output))
	}

	// Checkout specific branch if requested
	if opts.Branch != "" {
		logger.Debug("Checking out branch: %s", opts.Branch)
		checkoutCmd := exec.CommandContext(ctx, "git", "checkout", opts.Branch)
		checkoutCmd.Dir = tempDir
		if output, err := checkoutCmd.CombinedOutput(); err != nil {
			// Non-fatal: continue with whatever branch we have
			logger.Debug("Failed to checkout %s, continuing with default branch: %v", opts.Branch, err)
			logger.Debug("Git output: %s", string(output))
		}
	}

	// Execute callback with temp clone path
	logger.Debug("Executing callback with temp clone at: %s", tempDir)
	return fn(tempDir)
}

// CreateTempClone creates a temporary clone without automatic cleanup.
// Returns the clone path and a cleanup function that should be called manually.
//
// This is useful when you need to return data from the clone but still want
// cleanup control. Prefer WithTempClone when possible for automatic cleanup.
//
// Usage:
//
//	clonePath, cleanup, err := workspace.CreateTempClone(ctx, projectDir, mirrorPath, opts)
//	if err != nil {
//	    return err
//	}
//	defer cleanup()
//	// Work with clonePath
func CreateTempClone(ctx context.Context, projectDir, mirrorPath string, opts TempCloneOptions) (string, func(), error) {
	logger := opts.Logger
	if logger == nil {
		logger = logx.NewLogger("tempclone")
	}

	// Create temporary directory in project directory
	tempDir := filepath.Join(projectDir, ".tmp", fmt.Sprintf("clone-%d", time.Now().UnixNano()))
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return "", nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	// Cleanup function
	cleanup := func() {
		logger.Debug("Cleaning up temp clone: %s", tempDir)
		if err := os.RemoveAll(tempDir); err != nil {
			logger.Warn("Failed to clean up temp clone: %v", err)
		}
	}

	// Build git clone command
	cloneArgs := []string{"clone"}
	if opts.Shallow {
		cloneArgs = append(cloneArgs, "--depth=1", "--no-single-branch")
	}
	cloneArgs = append(cloneArgs, mirrorPath, tempDir)

	logger.Debug("Cloning from mirror: %s to %s", mirrorPath, tempDir)
	cloneCmd := exec.CommandContext(ctx, "git", cloneArgs...)
	if output, err := cloneCmd.CombinedOutput(); err != nil {
		cleanup() // Clean up on error
		return "", nil, fmt.Errorf("failed to clone from mirror: %w\nOutput: %s", err, string(output))
	}

	// Checkout specific branch if requested
	if opts.Branch != "" {
		logger.Debug("Checking out branch: %s", opts.Branch)
		checkoutCmd := exec.CommandContext(ctx, "git", "checkout", opts.Branch)
		checkoutCmd.Dir = tempDir
		if output, err := checkoutCmd.CombinedOutput(); err != nil {
			// Non-fatal: continue with whatever branch we have
			logger.Debug("Failed to checkout %s, continuing with default branch: %v", opts.Branch, err)
			logger.Debug("Git output: %s", string(output))
		}
	}

	return tempDir, cleanup, nil
}

// AtomicReplace atomically replaces a target directory with a source directory.
// This is done by:
// 1. Moving target out of the way (to target.old)
// 2. Moving source into place
// 3. Removing old version
//
// If the target directory doesn't exist, this simply moves source to target.
//
// Usage:
//
//	err := workspace.AtomicReplace("/path/to/workspace", "/path/to/temp-clone")
func AtomicReplace(target, source string) error {
	// Check if source exists
	if _, err := os.Stat(source); err != nil {
		return fmt.Errorf("source directory does not exist: %w", err)
	}

	// Check if target exists
	targetExists := true
	if _, err := os.Stat(target); err != nil {
		if os.IsNotExist(err) {
			targetExists = false
		} else {
			return fmt.Errorf("failed to check target directory: %w", err)
		}
	}

	if targetExists {
		// Move target out of the way
		oldPath := target + ".old"
		if err := os.Rename(target, oldPath); err != nil {
			return fmt.Errorf("failed to move target out of the way: %w", err)
		}

		// Move source into place
		if err := os.Rename(source, target); err != nil {
			// Try to restore old target on failure
			_ = os.Rename(oldPath, target)
			return fmt.Errorf("failed to move source into place: %w", err)
		}

		// Remove old version (non-fatal if this fails)
		_ = os.RemoveAll(oldPath)
	} else {
		// Target doesn't exist, just move source to target
		if err := os.Rename(source, target); err != nil {
			return fmt.Errorf("failed to move source to target: %w", err)
		}
	}

	return nil
}
