package build

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoOsExecImport ensures that pkg/build does not import os/exec in production code.
// This is a critical security invariant: the build service must not execute commands
// directly on the host. All command execution must go through the Executor interface,
// which routes commands to containers. Allowed exceptions: executor.go (contains
// Executor implementations that wrap os/exec) and *_test.go files.
func TestNoOsExecImport(t *testing.T) {
	// Files that are allowed to import os/exec
	allowedFiles := map[string]bool{
		"executor.go": true, // Executor implementations wrap os/exec
	}

	// Get the directory of the build package
	buildDir := "."

	// Find all Go files in the build package
	entries, err := os.ReadDir(buildDir)
	if err != nil {
		t.Fatalf("Failed to read build package directory: %v", err)
	}

	fset := token.NewFileSet()
	var violations []string

	for _, entry := range entries {
		name := entry.Name()

		// Skip non-Go files
		if !strings.HasSuffix(name, ".go") {
			continue
		}

		// Skip test files
		if strings.HasSuffix(name, "_test.go") {
			continue
		}

		// Skip allowed files
		if allowedFiles[name] {
			continue
		}

		// Parse the file
		filePath := filepath.Join(buildDir, name)
		node, err := parser.ParseFile(fset, filePath, nil, parser.ImportsOnly)
		if err != nil {
			t.Errorf("Failed to parse %s: %v", name, err)
			continue
		}

		// Check imports
		for _, imp := range node.Imports {
			// Remove quotes from import path
			importPath := strings.Trim(imp.Path.Value, `"`)

			if importPath == "os/exec" {
				violations = append(violations, name)
				break
			}
		}
	}

	if len(violations) > 0 {
		t.Errorf(`SECURITY VIOLATION: The following files import "os/exec" but should not:

  %s

The build service must not execute commands directly on the host.
All command execution must go through the Executor interface.

To fix this:
1. Refactor the code to accept an Executor parameter
2. Use executor.Run() instead of exec.Command()

If you believe this is a false positive, add the file to allowedFiles in no_exec_test.go
with a comment explaining why the exception is necessary.`,
			strings.Join(violations, "\n  "))
	}
}

// TestExecutorInterfaceCompliance verifies that both executor implementations
// satisfy the Executor interface.
func TestExecutorInterfaceCompliance(_ *testing.T) {
	// This is a compile-time check - if these lines compile, the interface is satisfied.
	var _ Executor = (*ContainerExecutor)(nil)
	var _ Executor = (*HostExecutor)(nil)
}
