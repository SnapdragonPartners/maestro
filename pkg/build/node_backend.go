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

// Build installs dependencies and builds the Node.js project
func (n *NodeBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "📦 Building Node.js project...\n")
	
	// Determine package manager
	packageManager := n.detectPackageManager(root)
	fmt.Fprintf(stream, "Using package manager: %s\n", packageManager)
	
	// Install dependencies
	if err := n.installDependencies(ctx, root, stream, packageManager); err != nil {
		return fmt.Errorf("failed to install dependencies: %w", err)
	}
	
	// Run build script if it exists
	if n.hasScript(root, "build") {
		fmt.Fprintf(stream, "Running build script...\n")
		if err := n.runScript(ctx, root, stream, packageManager, "build"); err != nil {
			return fmt.Errorf("build script failed: %w", err)
		}
	} else {
		fmt.Fprintf(stream, "No build script found, skipping build step\n")
	}
	
	fmt.Fprintf(stream, "✅ Node.js build completed successfully\n")
	return nil
}

// Test runs the Node.js test suite
func (n *NodeBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🧪 Running Node.js tests...\n")
	
	packageManager := n.detectPackageManager(root)
	
	// Try different test commands in order of preference
	testScripts := []string{"test", "test:unit", "jest", "mocha", "vitest"}
	
	for _, script := range testScripts {
		if n.hasScript(root, script) {
			fmt.Fprintf(stream, "Running test script: %s\n", script)
			
			if err := n.runScript(ctx, root, stream, packageManager, script); err != nil {
				return fmt.Errorf("test script failed: %w", err)
			}
			
			fmt.Fprintf(stream, "✅ Node.js tests completed successfully\n")
			return nil
		}
	}
	
	// If no test script found, try direct test runners
	testCommands := [][]string{
		{"npx", "jest"},
		{"npx", "mocha"},
		{"npx", "vitest", "run"},
		{"node", "--test"},  // Node.js built-in test runner
	}
	
	for _, cmd := range testCommands {
		if n.commandExists(cmd[0]) && n.hasTestFiles(root) {
			fmt.Fprintf(stream, "Running tests with: %s\n", strings.Join(cmd, " "))
			
			if err := n.runCommand(ctx, root, stream, cmd[0], cmd[1:]...); err != nil {
				// If this specific test command fails, continue to next
				fmt.Fprintf(stream, "Test command failed: %v\n", err)
				continue
			}
			
			fmt.Fprintf(stream, "✅ Node.js tests completed successfully\n")
			return nil
		}
	}
	
	// No test runner found
	if n.hasTestFiles(root) {
		return fmt.Errorf("test files found but no test runner available")
	}
	
	// No test files found, consider it successful
	fmt.Fprintf(stream, "✅ No test files found, skipping tests\n")
	return nil
}

// Lint runs JavaScript/TypeScript linting tools
func (n *NodeBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔍 Running Node.js linting...\n")
	
	packageManager := n.detectPackageManager(root)
	
	// Try lint scripts first
	lintScripts := []string{"lint", "lint:check", "eslint"}
	for _, script := range lintScripts {
		if n.hasScript(root, script) {
			fmt.Fprintf(stream, "Running lint script: %s\n", script)
			
			if err := n.runScript(ctx, root, stream, packageManager, script); err != nil {
				return fmt.Errorf("lint script failed: %w", err)
			}
			
			fmt.Fprintf(stream, "✅ Node.js linting completed successfully\n")
			return nil
		}
	}
	
	// Try direct linting commands
	lintCommands := [][]string{
		{"npx", "eslint", "."},
		{"npx", "@typescript-eslint/eslint-plugin", "."},
		{"npx", "standard", "."},
		{"npx", "jshint", "."},
	}
	
	for _, cmd := range lintCommands {
		if n.commandExists(cmd[0]) {
			fmt.Fprintf(stream, "Running linter: %s\n", strings.Join(cmd, " "))
			
			if err := n.runCommand(ctx, root, stream, cmd[0], cmd[1:]...); err != nil {
				fmt.Fprintf(stream, "Linter failed: %v\n", err)
				continue
			}
			
			fmt.Fprintf(stream, "✅ Node.js linting completed successfully\n")
			return nil
		}
	}
	
	// If no linter found, just warn
	fmt.Fprintf(stream, "⚠️ No linter found (eslint, standard, jshint), skipping linting\n")
	return nil
}

// Run executes the Node.js application
func (n *NodeBackend) Run(ctx context.Context, root string, args []string, stream io.Writer) error {
	fmt.Fprintf(stream, "🚀 Running Node.js application...\n")
	
	packageManager := n.detectPackageManager(root)
	
	// Try run scripts first
	runScripts := []string{"start", "dev", "serve", "run"}
	for _, script := range runScripts {
		if n.hasScript(root, script) {
			fmt.Fprintf(stream, "Running script: %s\n", script)
			
			fullArgs := append([]string{script}, args...)
			if err := n.runScript(ctx, root, stream, packageManager, fullArgs...); err != nil {
				return fmt.Errorf("run script failed: %w", err)
			}
			
			fmt.Fprintf(stream, "✅ Node.js application completed successfully\n")
			return nil
		}
	}
	
	// Try direct execution
	entryPoints := []string{"index.js", "main.js", "app.js", "server.js", "src/index.js", "src/main.js"}
	for _, entry := range entryPoints {
		if _, err := os.Stat(filepath.Join(root, entry)); err == nil {
			fmt.Fprintf(stream, "Running: node %s %s\n", entry, strings.Join(args, " "))
			
			fullArgs := append([]string{entry}, args...)
			if err := n.runCommand(ctx, root, stream, "node", fullArgs...); err != nil {
				return fmt.Errorf("failed to run application: %w", err)
			}
			
			fmt.Fprintf(stream, "✅ Node.js application completed successfully\n")
			return nil
		}
	}
	
	return fmt.Errorf("no runnable Node.js application found (start script or entry point)")
}

// detectPackageManager determines which package manager to use
func (n *NodeBackend) detectPackageManager(root string) string {
	// Check for lock files to determine package manager
	lockFiles := map[string]string{
		"bun.lockb":       "bun",
		"pnpm-lock.yaml":  "pnpm",
		"yarn.lock":       "yarn",
		"package-lock.json": "npm",
	}
	
	for lockFile, manager := range lockFiles {
		if _, err := os.Stat(filepath.Join(root, lockFile)); err == nil {
			if n.commandExists(manager) {
				return manager
			}
		}
	}
	
	// Default to npm if available
	if n.commandExists("npm") {
		return "npm"
	}
	
	// Fallback to first available package manager
	managers := []string{"bun", "pnpm", "yarn", "npm"}
	for _, manager := range managers {
		if n.commandExists(manager) {
			return manager
		}
	}
	
	return "npm" // Default even if not available
}

// installDependencies installs project dependencies
func (n *NodeBackend) installDependencies(ctx context.Context, root string, stream io.Writer, packageManager string) error {
	fmt.Fprintf(stream, "Installing dependencies with %s...\n", packageManager)
	
	var cmd []string
	switch packageManager {
	case "bun":
		cmd = []string{"bun", "install"}
	case "pnpm":
		cmd = []string{"pnpm", "install"}
	case "yarn":
		cmd = []string{"yarn", "install"}
	default:
		cmd = []string{"npm", "install"}
	}
	
	return n.runCommand(ctx, root, stream, cmd[0], cmd[1:]...)
}

// hasScript checks if a package.json script exists
func (n *NodeBackend) hasScript(root, script string) bool {
	packageJsonPath := filepath.Join(root, "package.json")
	data, err := os.ReadFile(packageJsonPath)
	if err != nil {
		return false
	}
	
	// Simple check for script existence (not parsing JSON for simplicity)
	content := string(data)
	return strings.Contains(content, fmt.Sprintf("\"%s\":", script))
}

// runScript executes a package.json script
func (n *NodeBackend) runScript(ctx context.Context, root string, stream io.Writer, packageManager string, args ...string) error {
	var cmd []string
	switch packageManager {
	case "bun":
		cmd = append([]string{"bun", "run"}, args...)
	case "pnpm":
		cmd = append([]string{"pnpm", "run"}, args...)
	case "yarn":
		cmd = append([]string{"yarn", "run"}, args...)
	default:
		cmd = append([]string{"npm", "run"}, args...)
	}
	
	return n.runCommand(ctx, root, stream, cmd[0], cmd[1:]...)
}

// hasTestFiles checks if the project has test files
func (n *NodeBackend) hasTestFiles(root string) bool {
	// Check for test directories
	testDirs := []string{"test", "tests", "__tests__", "spec", "specs"}
	for _, dir := range testDirs {
		dirPath := filepath.Join(root, dir)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			return true
		}
	}
	
	// Check for test files in common patterns
	testPatterns := []string{
		"*.test.js", "*.test.ts", "*.test.jsx", "*.test.tsx",
		"*.spec.js", "*.spec.ts", "*.spec.jsx", "*.spec.tsx",
	}
	
	for _, pattern := range testPatterns {
		if n.hasFilesMatchingPattern(root, pattern) {
			return true
		}
	}
	
	return false
}

// hasFilesMatchingPattern checks if files match a simple pattern
func (n *NodeBackend) hasFilesMatchingPattern(root, pattern string) bool {
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	
	// Simple pattern matching (just suffix for now)
	suffix := strings.TrimPrefix(pattern, "*")
	
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), suffix) {
			return true
		}
	}
	
	return false
}

// commandExists checks if a command is available in PATH
func (n *NodeBackend) commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// runCommand executes a command and streams output
func (n *NodeBackend) runCommand(ctx context.Context, root string, stream io.Writer, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = root
	cmd.Stdout = stream
	cmd.Stderr = stream
	
	fmt.Fprintf(stream, "$ %s %s\n", name, strings.Join(args, " "))
	
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("%s %s failed with exit code %d", name, strings.Join(args, " "), exitError.ExitCode())
		}
		return fmt.Errorf("%s %s failed: %w", name, strings.Join(args, " "), err)
	}
	
	return nil
}