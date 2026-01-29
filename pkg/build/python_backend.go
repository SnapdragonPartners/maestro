package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// PythonBackend handles Python projects using uv as the package manager.
type PythonBackend struct{}

// NewPythonBackend creates a new Python backend.
func NewPythonBackend() *PythonBackend {
	return &PythonBackend{}
}

// Name returns the backend name.
func (p *PythonBackend) Name() string {
	return "python"
}

// Detect checks if this is a Python project by looking for Python project files.
func (p *PythonBackend) Detect(root string) bool {
	// Check for Python project files in order of preference.
	pythonFiles := []string{
		"pyproject.toml",   // Modern Python projects
		"requirements.txt", // Traditional pip requirements
		"setup.py",         // Legacy setup files
		"Pipfile",          // Pipenv projects
		"poetry.lock",      // Poetry projects
	}

	for _, file := range pythonFiles {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			return true
		}
	}

	// Check for Python source directories.
	srcDirs := []string{"src", "lib", "app"}
	for _, dir := range srcDirs {
		dirPath := filepath.Join(root, dir)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			// Check if directory contains Python files.
			if p.containsPythonFiles(dirPath) {
				return true
			}
		}
	}

	// Check for Python files in root directory.
	return p.containsPythonFiles(root)
}

// containsPythonFiles checks if a directory contains Python source files.
func (p *PythonBackend) containsPythonFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".py") {
			return true
		}
	}

	return false
}

// Build installs dependencies and builds the Python project.
func (p *PythonBackend) Build(ctx context.Context, exec Executor, execDir string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔨 Building Python project via Makefile...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "build"); err != nil {
		return fmt.Errorf("make build failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Python build completed successfully\n")
	return nil
}

// Test runs the Python test suite.
func (p *PythonBackend) Test(ctx context.Context, exec Executor, execDir string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🧪 Running Python tests via Makefile...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "test"); err != nil {
		return fmt.Errorf("make test failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Python tests completed successfully\n")
	return nil
}

// Lint executes make lint for the project.
func (p *PythonBackend) Lint(ctx context.Context, exec Executor, execDir string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔍 Running Python linting via Makefile...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "lint"); err != nil {
		return fmt.Errorf("make lint failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Python linting completed successfully\n")
	return nil
}

// Run executes make run for the project.
func (p *PythonBackend) Run(ctx context.Context, exec Executor, execDir string, _ []string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🚀 Running Python application via Makefile...\n")

	if err := runMakeTarget(ctx, exec, execDir, stream, "run"); err != nil {
		return fmt.Errorf("make run failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Python application completed successfully\n")
	return nil
}

// GetDockerImage returns the appropriate Docker image for Python projects.
// It attempts to detect the Python version from project files and returns the corresponding image.
func (p *PythonBackend) GetDockerImage(_ string) string {
	// TODO: Parse pyproject.toml or other files to detect Python version
	// For now, return the default Python image.
	return "python:3.11-alpine"
}
