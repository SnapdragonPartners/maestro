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

// NodeBackend handles Node.js/JavaScript projects
type NodeBackend struct{}

// NewNodeBackend creates a new Node backend
func NewNodeBackend() *NodeBackend {
	return &NodeBackend{}
}

// Name returns the backend name
func (n *NodeBackend) Name() string {
	return "node"
}

// Detect checks if this is a Node.js project by looking for Node project files
func (n *NodeBackend) Detect(root string) bool {
	// Check for Node.js project files in order of preference
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

	// Check for Node.js source directories
	srcDirs := []string{"src", "lib", "app", "server"}
	for _, dir := range srcDirs {
		dirPath := filepath.Join(root, dir)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			// Check if directory contains JavaScript files
			if n.containsJavaScriptFiles(dirPath) {
				return true
			}
		}
	}

	// Check for JavaScript files in root directory
	return n.containsJavaScriptFiles(root)
}

// containsJavaScriptFiles checks if a directory contains JavaScript source files
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

// Build executes make build for the project
func (n *NodeBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔨 Building Node.js project via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "build"); err != nil {
		return fmt.Errorf("make build failed: %w", err)
	}

	fmt.Fprintf(stream, "✅ Node.js build completed successfully\n")
	return nil
}

// Test executes make test for the project
func (n *NodeBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🧪 Running Node.js tests via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "test"); err != nil {
		return fmt.Errorf("make test failed: %w", err)
	}

	fmt.Fprintf(stream, "✅ Node.js tests completed successfully\n")
	return nil
}

// Lint runs JavaScript/TypeScript linting tools
func (n *NodeBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔍 Running Node.js linting via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "lint"); err != nil {
		return fmt.Errorf("make lint failed: %w", err)
	}

	fmt.Fprintf(stream, "✅ Node.js linting completed successfully\n")
	return nil
}

// Run executes the Node.js application
func (n *NodeBackend) Run(ctx context.Context, root string, args []string, stream io.Writer) error {
	fmt.Fprintf(stream, "🚀 Running Node.js application via Makefile...\n")

	if err := n.runMakeCommand(ctx, root, stream, "run"); err != nil {
		return fmt.Errorf("make run failed: %w", err)
	}

	fmt.Fprintf(stream, "✅ Node.js application completed successfully\n")
	return nil
}

// runMakeCommand executes a make command with the given target
func (n *NodeBackend) runMakeCommand(ctx context.Context, root string, stream io.Writer, target string) error {
	cmd := exec.CommandContext(ctx, "make", target)
	cmd.Dir = root
	cmd.Stdout = stream
	cmd.Stderr = stream

	fmt.Fprintf(stream, "$ make %s\n", target)

	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("make %s failed with exit code %d", target, exitError.ExitCode())
		}
		return fmt.Errorf("make %s failed: %w", target, err)
	}

	return nil
}
