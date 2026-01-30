package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// MakeBackend handles projects with existing Makefiles.
type MakeBackend struct{}

// NewMakeBackend creates a new make backend.
func NewMakeBackend() *MakeBackend {
	return &MakeBackend{}
}

// Name returns the backend name.
func (m *MakeBackend) Name() string {
	return "make"
}

// Detect checks if a Makefile exists in the project root.
func (m *MakeBackend) Detect(root string) bool {
	makefiles := []string{"Makefile", "makefile", "GNUmakefile"}

	for _, makefile := range makefiles {
		if _, err := os.Stat(filepath.Join(root, makefile)); err == nil {
			return true
		}
	}

	return false
}

// Build executes the make build target.
func (m *MakeBackend) Build(ctx context.Context, exec Executor, execDir string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔨 Running make build...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "build"); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stream, "✅ make build completed successfully\n")
	return nil
}

// Test executes the make test target.
func (m *MakeBackend) Test(ctx context.Context, exec Executor, execDir string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🧪 Running make test...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "test"); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stream, "✅ make test completed successfully\n")
	return nil
}

// Lint executes the make lint target.
func (m *MakeBackend) Lint(ctx context.Context, exec Executor, execDir string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔍 Running make lint...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "lint"); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stream, "✅ make lint completed successfully\n")
	return nil
}

// Run executes the make run target.
func (m *MakeBackend) Run(ctx context.Context, exec Executor, execDir string, _ []string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🚀 Running make run...\n")

	// For make run, we typically don't pass additional arguments.
	// The run target should be configured in the Makefile.
	if err := runMakeTarget(ctx, exec, execDir, stream, "run"); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(stream, "✅ make run completed successfully\n")
	return nil
}

// ValidateTargets checks if the required targets exist in the Makefile.
// This uses the host filesystem to read the Makefile (detection runs on host).
func (m *MakeBackend) ValidateTargets(root string, targets []string) error {
	// Find the Makefile.
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

	// Read the Makefile content.
	content, err := os.ReadFile(makefilePath)
	if err != nil {
		return fmt.Errorf("failed to read Makefile: %w", err)
	}

	makefileContent := string(content)

	// Check for each required target.
	var missingTargets []string
	for _, target := range targets {
		// Look for target definitions (target: or target ::)
		targetPattern := fmt.Sprintf("%s:", target)
		if !strings.Contains(makefileContent, targetPattern) {
			// Also check for double-colon rules.
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

// GetDockerImage returns the appropriate Docker image for Makefile projects.
// Since Makefile projects can be any language, we return a generic Ubuntu image.
func (m *MakeBackend) GetDockerImage(_ string) string {
	// Generic Makefile projects get a Ubuntu image with build tools.
	return "ubuntu:22.04"
}
