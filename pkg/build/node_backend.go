package build

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// NodeBackend handles Node.js/JavaScript projects.
type NodeBackend struct{}

// NewNodeBackend creates a new Node backend.
func NewNodeBackend() *NodeBackend {
	return &NodeBackend{}
}

// Name returns the backend name.
func (n *NodeBackend) Name() string {
	return "node"
}

// Detect checks if this is a Node.js project by looking for Node project files.
func (n *NodeBackend) Detect(root string) bool {
	// Check for Node.js project files in order of preference.
	nodeFiles := []string{
		"package.json",      // Primary Node.js project file
		"package-lock.json", // npm lock file
		"yarn.lock",         // yarn lock file
		"pnpm-lock.yaml",    // pnpm lock file
		"bun.lockb",         // bun lock file
	}

	for _, file := range nodeFiles {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			return true
		}
	}

	// Check for Node.js source directories.
	srcDirs := []string{"src", "lib", "app", "server"}
	for _, dir := range srcDirs {
		dirPath := filepath.Join(root, dir)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			// Check if directory contains JavaScript files.
			if n.containsJavaScriptFiles(dirPath) {
				return true
			}
		}
	}

	// Check for JavaScript files in root directory.
	return n.containsJavaScriptFiles(root)
}

// containsJavaScriptFiles checks if a directory contains JavaScript source files.
func (n *NodeBackend) containsJavaScriptFiles(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}

	jsExtensions := []string{".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx"}

	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			for _, ext := range jsExtensions {
				if strings.HasSuffix(name, ext) {
					return true
				}
			}
		}
	}

	return false
}

// Build executes make build for the project.
func (n *NodeBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔨 Building Node.js project via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "build"); err != nil {
		return fmt.Errorf("make build failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Node.js build completed successfully\n")
	return nil
}

// Test executes make test for the project.
func (n *NodeBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🧪 Running Node.js tests via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "test"); err != nil {
		return fmt.Errorf("make test failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Node.js tests completed successfully\n")
	return nil
}

// Lint runs JavaScript/TypeScript linting tools.
func (n *NodeBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🔍 Running Node.js linting via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "lint"); err != nil {
		return fmt.Errorf("make lint failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Node.js linting completed successfully\n")
	return nil
}

// Run executes the Node.js application.
func (n *NodeBackend) Run(ctx context.Context, root string, _ []string, stream io.Writer) error {
	_, _ = fmt.Fprintf(stream, "🚀 Running Node.js application via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "run"); err != nil {
		return fmt.Errorf("make run failed: %w", err)
	}

	_, _ = fmt.Fprintf(stream, "✅ Node.js application completed successfully\n")
	return nil
}

// GetDockerImage returns the appropriate Docker image for Node.js projects.
// It attempts to detect the Node.js version from package.json and returns the corresponding image.
func (n *NodeBackend) GetDockerImage(_ string) string {
	// TODO: Parse package.json to detect Node.js version
	// For now, return the default Node.js image.
	return "node:20-alpine"
}

// runMakeCommand executes a make command with the given target.
func (n *NodeBackend) runMakeCommand(ctx context.Context, root string, stream io.Writer, target string) error {
	return runMakeCommand(ctx, root, stream, target)
}
