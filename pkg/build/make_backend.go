package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// MakeBackend handles projects with existing Makefiles
type MakeBackend struct{}

// NewMakeBackend creates a new make backend
func NewMakeBackend() *MakeBackend {
	return &MakeBackend{}
}

// Name returns the backend name
func (m *MakeBackend) Name() string {
	return "make"
}

// Detect checks if a Makefile exists in the project root
func (m *MakeBackend) Detect(root string) bool {
	makefiles := []string{"Makefile", "makefile", "GNUmakefile"}

	for _, makefile := range makefiles {
		if _, err := os.Stat(filepath.Join(root, makefile)); err == nil {
			return true
		}
	}

	return false
}

// Build executes the make build target
func (m *MakeBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	return m.runMakeTarget(ctx, root, "build", stream)
}

// Test executes the make test target
func (m *MakeBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	return m.runMakeTarget(ctx, root, "test", stream)
}

// Lint executes the make lint target
func (m *MakeBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	return m.runMakeTarget(ctx, root, "lint", stream)
}

// Run executes the make run target
func (m *MakeBackend) Run(ctx context.Context, root string, args []string, stream io.Writer) error {
	// For make run, we typically don't pass additional arguments
	// The run target should be configured in the Makefile
	return m.runMakeTarget(ctx, root, "run", stream)
}

// runMakeTarget executes a specific make target
func (m *MakeBackend) runMakeTarget(ctx context.Context, root, target string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔨 Running make %s...\n", target)

	cmd := exec.CommandContext(ctx, "make", target)
	cmd.Dir = root
	cmd.Stdout = stream
	cmd.Stderr = stream

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("make %s failed with exit code %d", target, exitError.ExitCode())
		}
		return fmt.Errorf("make %s failed: %w", target, err)
	}

	fmt.Fprintf(stream, "✅ make %s completed successfully\n", target)
	return nil
}

// ValidateTargets checks if the required targets exist in the Makefile
func (m *MakeBackend) ValidateTargets(root string, targets []string) error {
	// Find the Makefile
	var makefilePath string
	makefiles := []string{"Makefile", "makefile", "GNUmakefile"}

	for _, makefile := range makefiles {
		path := filepath.Join(root, makefile)
		if _, err := os.Stat(path); err == nil {
			makefilePath = path
			break
		}
	}

	if makefilePath == "" {
		return fmt.Errorf("no Makefile found in %s", root)
	}

	// Read the Makefile content
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}

	makefileContent := string(content)

	// Check for each required target
	var missingTargets []string
	for _, target := range targets {
		// Look for target definitions (target: or target ::)
		targetPattern := fmt.Sprintf("%s:", target)
		if !strings.Contains(makefileContent, targetPattern) {
			// Also check for double-colon rules
			targetPattern = fmt.Sprintf("%s::", target)
			if !strings.Contains(makefileContent, targetPattern) {
				missingTargets = append(missingTargets, target)
			}
		}
	}

	if len(missingTargets) > 0 {
		return fmt.Errorf("missing required targets in Makefile: %s", strings.Join(missingTargets, ", "))
	}

	return nil
}
