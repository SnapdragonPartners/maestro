package build

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPythonBackend(t *testing.T) {
	// Test with pyproject.toml
	t.Run("pyproject.toml detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "python-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create pyproject.toml
		pyprojectToml := `[build-system]
requires = ["setuptools>=45", "wheel"]
build-backend = "setuptools.build_meta"

[project]
name = "test-project"
version = "0.1.0"
`
		if err := os.WriteFile(filepath.Join(tempDir, "pyproject.toml"), []byte(pyprojectToml), 0644); err != nil {
			t.Fatalf("Failed to create pyproject.toml: %v", err)
		}

		backend := NewPythonBackend()
		
		if !backend.Detect(tempDir) {
			t.Error("PythonBackend should detect directories with pyproject.toml")
		}
		
		if backend.Name() != "python" {
			t.Errorf("Expected name 'python', got '%s'", backend.Name())
		}
	})

	// Test with requirements.txt
	t.Run("requirements.txt detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "python-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create requirements.txt
		requirements := `requests>=2.25.0
flask>=2.0.0
`
		if err := os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte(requirements), 0644); err != nil {
			t.Fatalf("Failed to create requirements.txt: %v", err)
		}

		backend := NewPythonBackend()
		
		if !backend.Detect(tempDir) {
			t.Error("PythonBackend should detect directories with requirements.txt")
		}
	})

	// Test with Python files
	t.Run("Python files detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "python-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create main.py
		mainPy := `def main():
    print("Hello, World!")

if __name__ == "__main__":
    main()
`
		if err := os.WriteFile(filepath.Join(tempDir, "main.py"), []byte(mainPy), 0644); err != nil {
			t.Fatalf("Failed to create main.py: %v", err)
		}

		backend := NewPythonBackend()
		
		if !backend.Detect(tempDir) {
			t.Error("PythonBackend should detect directories with Python files")
		}
	})

	// Test build operation
	t.Run("build operation", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "python-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create minimal project
		os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte("# No dependencies"), 0644)

		backend := NewPythonBackend()
		
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		
		var buf strings.Builder
		err = backend.Build(ctx, tempDir, &buf)
		
		// Build should complete (may warn about missing tools)
		output := buf.String()
		if !strings.Contains(output, "Python") {
			t.Error("Expected 'Python' in build output")
		}
	})
}

func TestNodeBackend(t *testing.T) {
	// Test with package.json
	t.Run("package.json detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "node-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create package.json
		packageJson := `{
  "name": "test-project",
  "version": "1.0.0",
  "description": "Test project",
  "main": "index.js",
  "scripts": {
    "start": "node index.js",
    "test": "jest",
    "build": "webpack"
  },
  "dependencies": {
    "express": "^4.18.0"
  }
}
`
		if err := os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageJson), 0644); err != nil {
			t.Fatalf("Failed to create package.json: %v", err)
		}

		backend := NewNodeBackend()
		
		if !backend.Detect(tempDir) {
			t.Error("NodeBackend should detect directories with package.json")
		}
		
		if backend.Name() != "node" {
			t.Errorf("Expected name 'node', got '%s'", backend.Name())
		}
	})

	// Test with JavaScript files
	t.Run("JavaScript files detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "node-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create index.js
		indexJs := `const express = require('express');
const app = express();

app.get('/', (req, res) => {
  res.send('Hello World!');
});

app.listen(3000);
`
		if err := os.WriteFile(filepath.Join(tempDir, "index.js"), []byte(indexJs), 0644); err != nil {
			t.Fatalf("Failed to create index.js: %v", err)
		}

		backend := NewNodeBackend()
		
		if !backend.Detect(tempDir) {
			t.Error("NodeBackend should detect directories with JavaScript files")
		}
	})

	// Test package manager detection
	t.Run("package manager detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "node-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create package.json
		packageJson := `{"name": "test"}`
		os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageJson), 0644)

		backend := NewNodeBackend()
		
		// Test different lock files
		testCases := []struct {
			lockFile string
			expected string
		}{
			{"package-lock.json", "npm"},
			{"yarn.lock", "yarn"},
			{"pnpm-lock.yaml", "pnpm"},
			{"bun.lockb", "bun"},
		}

		for _, tc := range testCases {
			// Create lock file
			os.WriteFile(filepath.Join(tempDir, tc.lockFile), []byte("{}"), 0644)
			
			detected := backend.detectPackageManager(tempDir)
			if detected != tc.expected && backend.commandExists(tc.expected) {
				t.Errorf("Expected package manager '%s' for %s, got '%s'", tc.expected, tc.lockFile, detected)
			}
			
			// Clean up
			os.Remove(filepath.Join(tempDir, tc.lockFile))
		}
	})

	// Test script detection
	t.Run("script detection", func(t *testing.T) {
		tempDir, err := os.MkdirTemp("", "node-backend-test")
		if err != nil {
			t.Fatalf("Failed to create temp dir: %v", err)
		}
		defer os.RemoveAll(tempDir)

		// Create package.json with scripts
		packageJson := `{
  "name": "test",
  "scripts": {
    "start": "node index.js",
    "test": "jest",
    "build": "webpack",
    "lint": "eslint ."
  }
}
`
		os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(packageJson), 0644)

		backend := NewNodeBackend()
		
		// Test script detection
		scripts := []string{"start", "test", "build", "lint"}
		for _, script := range scripts {
			if !backend.hasScript(tempDir, script) {
				t.Errorf("Should detect script '%s'", script)
			}
		}
		
		// Test non-existent script
		if backend.hasScript(tempDir, "nonexistent") {
			t.Error("Should not detect non-existent script")
		}
	})
}

func TestRegistryWithMVPBackends(t *testing.T) {
	registry := NewRegistry()
	
	// Test that all MVP backends are registered
	backends := registry.List()
	expectedBackends := []string{"go", "python", "node", "make", "null"}
	
	if len(backends) != len(expectedBackends) {
		t.Errorf("Expected %d backends, got %d", len(expectedBackends), len(backends))
	}
	
	// Check that all expected backends are present
	foundBackends := make(map[string]bool)
	for _, registration := range backends {
		foundBackends[registration.Backend.Name()] = true
	}
	
	for _, expected := range expectedBackends {
		if !foundBackends[expected] {
			t.Errorf("Expected backend '%s' not found", expected)
		}
	}
	
	// Test priority ordering
	for i := 1; i < len(backends); i++ {
		if backends[i-1].Priority < backends[i].Priority {
			t.Error("Backends should be sorted by priority (highest first)")
		}
	}
}

func TestMultiBackendDetection(t *testing.T) {
	// Test project with multiple backend indicators
	tempDir, err := os.MkdirTemp("", "multi-backend-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create files for multiple backends
	os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte("module test"), 0644)
	os.WriteFile(filepath.Join(tempDir, "package.json"), []byte(`{"name": "test"}`), 0644)
	os.WriteFile(filepath.Join(tempDir, "requirements.txt"), []byte("# Test"), 0644)
	os.WriteFile(filepath.Join(tempDir, "Makefile"), []byte("build:\n\techo test"), 0644)

	registry := NewRegistry()
	
	// Should detect Go backend (highest priority)
	backend, err := registry.Detect(tempDir)
	if err != nil {
		t.Fatalf("Detection failed: %v", err)
	}
	
	if backend.Name() != "go" {
		t.Errorf("Expected 'go' backend for multi-backend project, got '%s'", backend.Name())
	}
}

func TestBackendFallbacks(t *testing.T) {
	// Test that operations gracefully handle missing tools
	backends := []BuildBackend{
		NewGoBackend(),
		NewPythonBackend(),
		NewNodeBackend(),
	}

	for _, backend := range backends {
		t.Run(backend.Name()+" fallbacks", func(t *testing.T) {
			tempDir, err := os.MkdirTemp("", "fallback-test")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tempDir)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			var buf strings.Builder

			// Operations should not panic even if tools are missing
			backend.Build(ctx, tempDir, &buf)
			backend.Test(ctx, tempDir, &buf)
			backend.Lint(ctx, tempDir, &buf)
			backend.Run(ctx, tempDir, []string{}, &buf)
		})
	}
}