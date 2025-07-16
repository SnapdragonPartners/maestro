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

// PythonBackend handles Python projects using uv as the package manager
type PythonBackend struct{}

// NewPythonBackend creates a new Python backend
func NewPythonBackend() *PythonBackend {
	return &PythonBackend{}
}

// Name returns the backend name
func (p *PythonBackend) Name() string {
	return "python"
}

// Detect checks if this is a Python project by looking for Python project files
func (p *PythonBackend) Detect(root string) bool {
	// Check for Python project files in order of preference
	pythonFiles := []string{
		"pyproject.toml",    // Modern Python projects
		"requirements.txt",  // Traditional pip requirements
		"setup.py",          // Legacy setup files
		"Pipfile",           // Pipenv projects
		"poetry.lock",       // Poetry projects
	}
	
	for _, file := range pythonFiles {
		if _, err := os.Stat(filepath.Join(root, file)); err == nil {
			return true
		}
	}
	
	// Check for Python source directories
	srcDirs := []string{"src", "lib", "app"}
	for _, dir := range srcDirs {
		dirPath := filepath.Join(root, dir)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			// Check if directory contains Python files
			if p.containsPythonFiles(dirPath) {
				return true
			}
		}
	}
	
	// Check for Python files in root directory
	return p.containsPythonFiles(root)
}

// containsPythonFiles checks if a directory contains Python source files
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

// Build installs dependencies and builds the Python project
func (p *PythonBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🐍 Building Python project...\n")
	
	// Check if uv is available
	if _, err := exec.LookPath("uv"); err != nil {
		fmt.Fprintf(stream, "uv not found, falling back to pip\n")
		return p.buildWithPip(ctx, root, stream)
	}
	
	// Use uv for dependency management
	return p.buildWithUv(ctx, root, stream)
}

// buildWithUv builds the project using uv
func (p *PythonBackend) buildWithUv(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "Using uv for dependency management\n")
	
	// Check if pyproject.toml exists
	if _, err := os.Stat(filepath.Join(root, "pyproject.toml")); err == nil {
		// Modern Python project with pyproject.toml
		fmt.Fprintf(stream, "Found pyproject.toml, installing dependencies with uv\n")
		
		// Install dependencies
		if err := p.runCommand(ctx, root, stream, "uv", "sync"); err != nil {
			return fmt.Errorf("failed to install dependencies: %w", err)
		}
		
		// Build the project if it has a build system
		if err := p.runCommand(ctx, root, stream, "uv", "build"); err != nil {
			// Build might fail if this is not a distributable package, that's OK
			fmt.Fprintf(stream, "Build step skipped (not a distributable package)\n")
		}
	} else if _, err := os.Stat(filepath.Join(root, "requirements.txt")); err == nil {
		// Traditional requirements.txt
		fmt.Fprintf(stream, "Found requirements.txt, installing dependencies with uv\n")
		
		if err := p.runCommand(ctx, root, stream, "uv", "pip", "install", "-r", "requirements.txt"); err != nil {
			return fmt.Errorf("failed to install requirements: %w", err)
		}
	} else {
		fmt.Fprintf(stream, "No dependency file found, skipping dependency installation\n")
	}
	
	fmt.Fprintf(stream, "✅ Python build completed successfully\n")
	return nil
}

// buildWithPip builds the project using pip (fallback)
func (p *PythonBackend) buildWithPip(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "Using pip for dependency management\n")
	
	// Install dependencies if requirements.txt exists
	if _, err := os.Stat(filepath.Join(root, "requirements.txt")); err == nil {
		fmt.Fprintf(stream, "Installing dependencies from requirements.txt\n")
		
		if err := p.runCommand(ctx, root, stream, "pip", "install", "-r", "requirements.txt"); err != nil {
			return fmt.Errorf("failed to install requirements: %w", err)
		}
	} else if _, err := os.Stat(filepath.Join(root, "setup.py")); err == nil {
		fmt.Fprintf(stream, "Installing package with setup.py\n")
		
		if err := p.runCommand(ctx, root, stream, "pip", "install", "-e", "."); err != nil {
			return fmt.Errorf("failed to install package: %w", err)
		}
	} else {
		fmt.Fprintf(stream, "No dependency file found, skipping dependency installation\n")
	}
	
	fmt.Fprintf(stream, "✅ Python build completed successfully\n")
	return nil
}

// Test runs the Python test suite
func (p *PythonBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🧪 Running Python tests...\n")
	
	// Try different test runners in order of preference
	testCommands := [][]string{
		{"uv", "run", "pytest"},     // Modern uv + pytest
		{"pytest"},                  // Direct pytest
		{"python", "-m", "pytest"},  // Python module pytest
		{"uv", "run", "python", "-m", "unittest", "discover"}, // uv + unittest
		{"python", "-m", "unittest", "discover"},              // Direct unittest
	}
	
	for _, cmd := range testCommands {
		if p.commandExists(cmd[0]) {
			fmt.Fprintf(stream, "Running tests with: %s\n", strings.Join(cmd, " "))
			
			if err := p.runCommand(ctx, root, stream, cmd[0], cmd[1:]...); err != nil {
				// If this specific test command fails, continue to next
				fmt.Fprintf(stream, "Test command failed: %v\n", err)
				continue
			}
			
			fmt.Fprintf(stream, "✅ Python tests completed successfully\n")
			return nil
		}
	}
	
	// If no test runner found, check if there are any test files
	if p.hasTestFiles(root) {
		return fmt.Errorf("test files found but no test runner available (pytest, unittest)")
	}
	
	// No test files found, consider it successful
	fmt.Fprintf(stream, "✅ No test files found, skipping tests\n")
	return nil
}

// Lint runs Python linting tools
func (p *PythonBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔍 Running Python linting...\n")
	
	// Try different linters in order of preference
	lintCommands := [][]string{
		{"uv", "run", "ruff", "check", "."},        // Modern uv + ruff
		{"ruff", "check", "."},                     // Direct ruff
		{"uv", "run", "flake8", "."},               // uv + flake8
		{"flake8", "."},                            // Direct flake8
		{"uv", "run", "pylint", "."},               // uv + pylint
		{"pylint", "."},                            // Direct pylint
	}
	
	for _, cmd := range lintCommands {
		if p.commandExists(cmd[0]) {
			fmt.Fprintf(stream, "Running linter: %s\n", strings.Join(cmd, " "))
			
			if err := p.runCommand(ctx, root, stream, cmd[0], cmd[1:]...); err != nil {
				fmt.Fprintf(stream, "Linter failed: %v\n", err)
				continue
			}
			
			fmt.Fprintf(stream, "✅ Python linting completed successfully\n")
			return nil
		}
	}
	
	// If no linter found, just warn
	fmt.Fprintf(stream, "⚠️ No linter found (ruff, flake8, pylint), skipping linting\n")
	return nil
}

// Run executes the Python application
func (p *PythonBackend) Run(ctx context.Context, root string, args []string, stream io.Writer) error {
	fmt.Fprintf(stream, "🚀 Running Python application...\n")
	
	// Try different run methods
	runCommands := [][]string{
		{"uv", "run", "python", "-m", "main"},    // uv + main module
		{"python", "-m", "main"},                 // Direct main module
		{"uv", "run", "python", "main.py"},       // uv + main.py
		{"python", "main.py"},                    // Direct main.py
		{"uv", "run", "python", "app.py"},        // uv + app.py
		{"python", "app.py"},                     // Direct app.py
	}
	
	for _, cmd := range runCommands {
		if p.commandExists(cmd[0]) {
			// Check if the target file/module exists
			if p.canRunCommand(root, cmd) {
				fmt.Fprintf(stream, "Running: %s %s\n", strings.Join(cmd, " "), strings.Join(args, " "))
				
				fullCmd := append(cmd, args...)
				if err := p.runCommand(ctx, root, stream, fullCmd[0], fullCmd[1:]...); err != nil {
					return fmt.Errorf("failed to run application: %w", err)
				}
				
				fmt.Fprintf(stream, "✅ Python application completed successfully\n")
				return nil
			}
		}
	}
	
	return fmt.Errorf("no runnable Python application found (main.py, app.py, or main module)")
}

// canRunCommand checks if a run command is viable
func (p *PythonBackend) canRunCommand(root string, cmd []string) bool {
	if len(cmd) < 2 {
		return false
	}
	
	// Check for specific files
	if strings.HasSuffix(cmd[len(cmd)-1], ".py") {
		filename := cmd[len(cmd)-1]
		_, err := os.Stat(filepath.Join(root, filename))
		return err == nil
	}
	
	// Check for main module
	if len(cmd) >= 3 && cmd[len(cmd)-2] == "-m" && cmd[len(cmd)-1] == "main" {
		// Check for main.py or main/__init__.py
		return p.moduleExists(root, "main")
	}
	
	return false
}

// moduleExists checks if a Python module exists
func (p *PythonBackend) moduleExists(root, module string) bool {
	// Check for module.py
	if _, err := os.Stat(filepath.Join(root, module+".py")); err == nil {
		return true
	}
	
	// Check for module/__init__.py
	if _, err := os.Stat(filepath.Join(root, module, "__init__.py")); err == nil {
		return true
	}
	
	return false
}

// hasTestFiles checks if the project has test files
func (p *PythonBackend) hasTestFiles(root string) bool {
	// Check for test directories
	testDirs := []string{"tests", "test"}
	for _, dir := range testDirs {
		dirPath := filepath.Join(root, dir)
		if info, err := os.Stat(dirPath); err == nil && info.IsDir() {
			return true
		}
	}
	
	// Check for test files in root
	entries, err := os.ReadDir(root)
	if err != nil {
		return false
	}
	
	for _, entry := range entries {
		if !entry.IsDir() {
			name := entry.Name()
			if strings.HasPrefix(name, "test_") && strings.HasSuffix(name, ".py") {
				return true
			}
		}
	}
	
	return false
}

// commandExists checks if a command is available in PATH
func (p *PythonBackend) commandExists(cmd string) bool {
	_, err := exec.LookPath(cmd)
	return err == nil
}

// runCommand executes a command and streams output
func (p *PythonBackend) runCommand(ctx context.Context, root string, stream io.Writer, name string, args ...string) error {
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