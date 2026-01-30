package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// NullBackend is a no-op backend for empty repositories or unsupported project types.
type NullBackend struct{}

// NewNullBackend creates a new null backend.
func NewNullBackend() *NullBackend {
	return &NullBackend{}
}

// Name returns the backend name.
func (n *NullBackend) Name() string {
	return "null"
}

// Detect returns true only for empty repositories.
func (n *NullBackend) Detect(root string) bool {
	// Only detect for empty repositories.
	return n.isEmptyRepo(root)
}

// Build is a no-op for null backend.
// The exec and execDir parameters are unused since no commands are executed.
// Note: Detect() already validated this is an empty repo using the host path,
// so we don't re-check here (execDir is a container path which may not exist on host).
func (n *NullBackend) Build(_ context.Context, _ Executor, _ string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "✅ Build successful (no build configured for empty repository)\n")
	return nil
}

// Test is a no-op for null backend.
// The exec and execDir parameters are unused since no commands are executed.
func (n *NullBackend) Test(_ context.Context, _ Executor, _ string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "✅ Tests passed (no tests configured for empty repository)\n")
	return nil
}

// Lint is a no-op for null backend.
// The exec and execDir parameters are unused since no commands are executed.
func (n *NullBackend) Lint(_ context.Context, _ Executor, _ string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "✅ Linting passed (no linting configured for empty repository)\n")
	return nil
}

// Run is a no-op for null backend.
// The exec and execDir parameters are unused since no commands are executed.
func (n *NullBackend) Run(_ context.Context, _ Executor, _ string, _ []string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "✅ Run successful (no run target configured for empty repository)\n")
	return nil
}

// isEmptyRepo checks if the repository is effectively empty (no significant files).
func (n *NullBackend) isEmptyRepo(root string) bool {
	// Check for common project files that would indicate this is not an empty repo.
	projectFiles := []string{
		"go.mod", "go.sum",
		"package.json", "package-lock.json", "yarn.lock",
		"pyproject.toml", "requirements.txt", "setup.py",
		"Makefile", "makefile",
		"Cargo.toml", "Cargo.lock",
		"pom.xml", "build.gradle",
		"CMakeLists.txt",
		"Dockerfile",
	}

	for _, file := range projectFiles {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			return false
		}
	}

	// Check for source code directories.
	srcDirs := []string{"src", "lib", "cmd", "internal", "pkg"}
	for _, dir := range srcDirs {
		if info, err := os.Stat(filepath.Join(root, dir)); err == nil && info.IsDir() {
			return false
		}
	}

	return true
}

// GetDockerImage returns the appropriate Docker image for null backend.
// Since null backend is for empty repositories, we use a generic Ubuntu image.
func (n *NullBackend) GetDockerImage(_ string) string {
	// Empty repositories get a generic Ubuntu image.
	return "ubuntu:22.04"
}
