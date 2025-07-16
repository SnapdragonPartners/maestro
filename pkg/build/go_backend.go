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

// GoBackend handles Go projects with go.mod files
type GoBackend struct{}

// NewGoBackend creates a new Go backend
func NewGoBackend() *GoBackend {
	return &GoBackend{}
}

// Name returns the backend name
func (g *GoBackend) Name() string {
	return "go"
}

// Detect checks if a go.mod file exists in the project root
func (g *GoBackend) Detect(root string) bool {
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	return err == nil
}

// Build executes go build for the project
func (g *GoBackend) Build(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔨 Building Go project...\n")
	
	// First, download dependencies
	if err := g.runGoCommand(ctx, root, stream, "mod", "download"); err != nil {
		return fmt.Errorf("failed to download dependencies: %w", err)
	}
	
	// Then build the project
	if err := g.runGoCommand(ctx, root, stream, "build", "./..."); err != nil {
		return fmt.Errorf("build failed: %w", err)
	}
	
	fmt.Fprintf(stream, "✅ Go build completed successfully\n")
	return nil
}

// Test executes go test for the project
func (g *GoBackend) Test(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🧪 Running Go tests...\n")
	
	if err := g.runGoCommand(ctx, root, stream, "test", "./..."); err != nil {
		return fmt.Errorf("tests failed: %w", err)
	}
	
	fmt.Fprintf(stream, "✅ Go tests completed successfully\n")
	return nil
}

// Lint executes Go linting (golangci-lint if available, otherwise go vet)
func (g *GoBackend) Lint(ctx context.Context, root string, stream io.Writer) error {
	fmt.Fprintf(stream, "🔍 Running Go linting...\n")
	
	// Try golangci-lint first
	if _, err := exec.LookPath("golangci-lint"); err == nil {
		fmt.Fprintf(stream, "Using golangci-lint for linting\n")
		cmd := exec.CommandContext(ctx, "golangci-lint", "run", "./...")
		cmd.Dir = root
		cmd.Stdout = stream
		cmd.Stderr = stream
		
		if err := cmd.Run(); err != nil {
			if exitError, ok := err.(*exec.ExitError); ok {
				return fmt.Errorf("golangci-lint failed with exit code %d", exitError.ExitCode())
			}
			return fmt.Errorf("golangci-lint failed: %w", err)
		}
	} else {
		// Fall back to go vet
		fmt.Fprintf(stream, "golangci-lint not found, using go vet\n")
		if err := g.runGoCommand(ctx, root, stream, "vet", "./..."); err != nil {
			return fmt.Errorf("go vet failed: %w", err)
		}
	}
	
	fmt.Fprintf(stream, "✅ Go linting completed successfully\n")
	return nil
}

// Run executes the Go application
func (g *GoBackend) Run(ctx context.Context, root string, args []string, stream io.Writer) error {
	fmt.Fprintf(stream, "🚀 Running Go application...\n")
	
	// Check if there's a cmd directory with main packages
	cmdDir := filepath.Join(root, "cmd")
	if info, err := os.Stat(cmdDir); err == nil && info.IsDir() {
		// Find the first main package in cmd directory
		entries, err := os.ReadDir(cmdDir)
		if err != nil {
			return fmt.Errorf("failed to read cmd directory: %w", err)
		}
		
		for _, entry := range entries {
			if entry.IsDir() {
				mainPkg := filepath.Join("cmd", entry.Name())
				fmt.Fprintf(stream, "Running %s\n", mainPkg)
				
				goArgs := append([]string{"run", "./" + mainPkg}, args...)
				if err := g.runGoCommand(ctx, root, stream, goArgs...); err != nil {
					return fmt.Errorf("failed to run %s: %w", mainPkg, err)
				}
				
				fmt.Fprintf(stream, "✅ Go application completed successfully\n")
				return nil
			}
		}
	}
	
	// Check if there's a main.go in the root
	if _, err := os.Stat(filepath.Join(root, "main.go")); err == nil {
		fmt.Fprintf(stream, "Running main.go\n")
		goArgs := append([]string{"run", "main.go"}, args...)
		if err := g.runGoCommand(ctx, root, stream, goArgs...); err != nil {
			return fmt.Errorf("failed to run main.go: %w", err)
		}
		
		fmt.Fprintf(stream, "✅ Go application completed successfully\n")
		return nil
	}
	
	// Try to run the module directly
	fmt.Fprintf(stream, "Running module directly\n")
	goArgs := append([]string{"run", "."}, args...)
	if err := g.runGoCommand(ctx, root, stream, goArgs...); err != nil {
		return fmt.Errorf("failed to run module: %w", err)
	}
	
	fmt.Fprintf(stream, "✅ Go application completed successfully\n")
	return nil
}

// runGoCommand executes a go command with the given arguments
func (g *GoBackend) runGoCommand(ctx context.Context, root string, stream io.Writer, args ...string) error {
	cmd := exec.CommandContext(ctx, "go", args...)
	cmd.Dir = root
	cmd.Stdout = stream
	cmd.Stderr = stream
	
	fmt.Fprintf(stream, "$ go %s\n", strings.Join(args, " "))
	
	if err := cmd.Run(); err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("go %s failed with exit code %d", strings.Join(args, " "), exitError.ExitCode())
		}
		return fmt.Errorf("go %s failed: %w", strings.Join(args, " "), err)
	}
	
	return nil
}