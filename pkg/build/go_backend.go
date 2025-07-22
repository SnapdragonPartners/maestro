package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// GoBackend handles Go projects with go.mod files.
type GoBackend struct{}

// NewGoBackend creates a new Go backend.
func NewGoBackend() *GoBackend {
	return &GoBackend{}
}

// Name returns the backend name.
func (g *GoBackend) Name() string {
	return "go"
}

// Detect checks if a go.mod file exists in the project root.
func (g *GoBackend) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil
}

// Build executes make build for the project.
func (g *GoBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔨 Building Go project via Makefile...\n")

	if err := g.runMakeCommand(ctx, root, stream, "build"); err != nil {
		return fmt.Errorf("make build failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Go build completed successfully\n")
	return nil
}

// Test executes make test for the project.
func (g *GoBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🧪 Running Go tests via Makefile...\n")

	if err := g.runMakeCommand(ctx, root, stream, "test"); err != nil {
		return fmt.Errorf("make test failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Go tests completed successfully\n")
	return nil
}

// Lint executes make lint for the project.
func (g *GoBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔍 Running Go linting via Makefile...\n")

	if err := g.runMakeCommand(ctx, root, stream, "lint"); err != nil {
		return fmt.Errorf("make lint failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Go linting completed successfully\n")
	return nil
}

// Run executes make run for the project.
func (g *GoBackend) Run(ctx context.Context, root string, _ []string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🚀 Running Go application via Makefile...\n")

	if err := g.runMakeCommand(ctx, root, stream, "run"); err != nil {
		return fmt.Errorf("make run failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Go application completed successfully\n")
	return nil
}

// GetDockerImage returns the appropriate Docker image for Go projects.
// It attempts to detect the Go version from go.mod and returns the corresponding image.
func (g *GoBackend) GetDockerImage(_ string) string {
	// TODO: Parse go.mod to detect Go version and return appropriate image
	// For now, return the default Go image.
	return "golang:1.24-alpine"
}

// runMakeCommand executes a make command with the given target.
func (g *GoBackend) runMakeCommand(ctx context.Context, root string, stream io.Writer, target string) error {
	return runMakeCommand(ctx, root, stream, target)
}
