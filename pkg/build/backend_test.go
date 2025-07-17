package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNullBackend(t *testing.T) {
	// Create a temporary empty directory
	tempDir, err := os.MkdirTemp("", "null-backend-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	backend := NewNullBackend()

	// Test that it detects empty directories
	if !backend.Detect(tempDir) {
		t.Error("NullBackend should detect empty directories")
	}

	// Test that all operations succeed
	ctx := context.Background()
	var buf strings.Builder

	if err := backend.Build(ctx, tempDir, &buf); err != nil {
		t.Errorf("Build failed: %v", err)
	}

	if err := backend.Test(ctx, tempDir, &buf); err != nil {
		t.Errorf("Test failed: %v", err)
	}

	if err := backend.Lint(ctx, tempDir, &buf); err != nil {
		t.Errorf("Lint failed: %v", err)
	}

	if err := backend.Run(ctx, tempDir, []string{}, &buf); err != nil {
		t.Errorf("Run failed: %v", err)
	}

	// Create a project file and verify it no longer detects as empty
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test"), 0644)
	if backend.Detect(tempDir) {
		t.Error("NullBackend should not detect directories with project files")
	}
}

func TestGoBackend(t *testing.T) {
	// Create a temporary directory with go.mod
	tempDir, err := os.MkdirTemp("", "go-backend-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create go.mod
	goModContent := `module test
go 1.19
`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goModContent), 0644); err != nil {
		t.Fatalf("Failed to create go.mod: %v", err)
	}

	backend := NewGoBackend()

	// Test detection
	if !backend.Detect(tempDir) {
		t.Error("GoBackend should detect directories with go.mod")
	}

	// Test name
	if backend.Name() != "go" {
		t.Errorf("Expected name 'go', got '%s'", backend.Name())
	}

	// Test build (should succeed even with no source files)
	ctx := context.Background()
	var buf strings.Builder

	if err := backend.Build(ctx, tempDir, &buf); err != nil {
		t.Errorf("Build failed: %v", err)
	}
}

func TestMakeBackend(t *testing.T) {
	// Create a temporary directory with Makefile
	tempDir, err := os.MkdirTemp("", "make-backend-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create Makefile
	makefileContent := `build:
	@echo "Building..."

test:
	@echo "Testing..."

lint:
	@echo "Linting..."

run:
	@echo "Running..."
`
	if err := os.WriteFile(filepath.Join(tempDir, "Makefile"), []byte(makefileContent), 0644); err != nil {
		t.Fatalf("Failed to create Makefile: %v", err)
	}

	backend := NewMakeBackend()

	// Test detection
	if !backend.Detect(tempDir) {
		t.Error("MakeBackend should detect directories with Makefile")
	}

	// Test name
	if backend.Name() != "make" {
		t.Errorf("Expected name 'make', got '%s'", backend.Name())
	}

	// Test target validation
	requiredTargets := []string{"build", "test", "lint", "run"}
	if err := backend.ValidateTargets(tempDir, requiredTargets); err != nil {
		t.Errorf("ValidateTargets failed: %v", err)
	}

	// Test with missing target
	missingTargets := []string{"build", "test", "lint", "run", "nonexistent"}
	if err := backend.ValidateTargets(tempDir, missingTargets); err == nil {
		t.Error("ValidateTargets should fail for missing targets")
	}
}

func TestRegistry(t *testing.T) {
	registry := NewRegistry()

	// Test that backends are registered
	backends := registry.List()
	if len(backends) == 0 {
		t.Error("Registry should have default backends")
	}

	// Test priority ordering (highest first)
	for i := 1; i < len(backends); i++ {
		if backends[i-1].Priority < backends[i].Priority {
			t.Error("Backends should be sorted by priority (highest first)")
		}
	}

	// Test GetByName
	nullBackend, err := registry.GetByName("null")
	if err != nil {
		t.Errorf("Failed to get null backend: %v", err)
	}
	if nullBackend.Name() != "null" {
		t.Errorf("Expected null backend, got %s", nullBackend.Name())
	}

	// Test GetByName with non-existent backend
	_, err = registry.GetByName("nonexistent")
	if err == nil {
		t.Error("GetByName should fail for non-existent backend")
	}
}

func TestRegistryDetection(t *testing.T) {
	registry := NewRegistry()

	// Test with empty directory (should select NullBackend)
	tempDir, err := os.MkdirTemp("", "registry-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	backend, err := registry.Detect(tempDir)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	if backend.Name() != "null" {
		t.Errorf("Expected null backend for empty directory, got %s", backend.Name())
	}

	// Test with go.mod (should select GoBackend)
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test"), 0644)
	backend, err = registry.Detect(tempDir)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	if backend.Name() != "go" {
		t.Errorf("Expected go backend for go.mod, got %s", backend.Name())
	}

	// Test with Makefile (should still select GoBackend due to higher priority)
	os.WriteFile(filepath.Join(tempDir, "Makefile"), []byte("build:\n\techo test"), 0644)
	backend, err = registry.Detect(tempDir)
	if err != nil {
		t.Errorf("Detection failed: %v", err)
	}
	if backend.Name() != "go" {
		t.Errorf("Expected go backend for go.mod+Makefile, got %s", backend.Name())
	}
}

// discardWriter is a writer that discards all input
type discardWriter struct{}

func (d discardWriter) Write(p []byte) (n int, err error) {
	return len(p), nil
}

func TestBackendOperations(t *testing.T) {
	// Test that all backend operations have consistent interfaces
	backends := []BuildBackend{
		NewNullBackend(),
		NewMakeBackend(),
		NewGoBackend(),
	}

	for _, backend := range backends {
		// Test that Name() returns non-empty string
		if backend.Name() == "" {
			t.Errorf("Backend %T should have non-empty name", backend)
		}

		// Test that Detect() doesn't panic
		backend.Detect("/nonexistent")

		// Test that operations don't panic with discard writer
		ctx := context.Background()
		writer := discardWriter{}

		backend.Build(ctx, "/nonexistent", writer)
		backend.Test(ctx, "/nonexistent", writer)
		backend.Lint(ctx, "/nonexistent", writer)
		backend.Run(ctx, "/nonexistent", []string{}, writer)
	}
}
