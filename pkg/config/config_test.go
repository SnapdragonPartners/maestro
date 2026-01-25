package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGetDockerfilePath_Default(t *testing.T) {
	// Set up a temporary config for testing
	SetConfigForTesting(&Config{
		Container: &ContainerConfig{
			// Dockerfile not set
		},
	})
	defer SetConfigForTesting(nil)

	path := GetDockerfilePath()
	if path != DefaultDockerfilePath {
		t.Errorf("GetDockerfilePath() = %q, want %q", path, DefaultDockerfilePath)
	}
}

func TestGetDockerfilePath_Configured(t *testing.T) {
	customPath := ".maestro/Dockerfile.custom"
	SetConfigForTesting(&Config{
		Container: &ContainerConfig{
			Dockerfile: customPath,
		},
	})
	defer SetConfigForTesting(nil)

	path := GetDockerfilePath()
	if path != customPath {
		t.Errorf("GetDockerfilePath() = %q, want %q", path, customPath)
	}
}

func TestIsValidDockerfilePath_Valid(t *testing.T) {
	tests := []struct {
		name string
		path string
		want bool
	}{
		{
			name: "default path",
			path: ".maestro/Dockerfile",
			want: true,
		},
		{
			name: "custom dockerfile name",
			path: ".maestro/Dockerfile.dev",
			want: true,
		},
		{
			name: "nested path",
			path: ".maestro/docker/Dockerfile.prod",
			want: true,
		},
		{
			name: "cuda variant",
			path: ".maestro/Dockerfile.cuda",
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidDockerfilePath(tt.path); got != tt.want {
				t.Errorf("IsValidDockerfilePath(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestIsValidDockerfilePath_Invalid(t *testing.T) {
	tests := []struct {
		name string
		path string
	}{
		{
			name: "empty path",
			path: "",
		},
		{
			name: "repo root",
			path: "Dockerfile",
		},
		{
			name: "different directory",
			path: "docker/Dockerfile",
		},
		{
			name: "absolute path",
			path: "/etc/Dockerfile",
		},
		{
			name: "dot-dot escape attempt",
			path: ".maestro/../Dockerfile",
		},
		{
			name: "double dot-dot escape",
			path: ".maestro/../../etc/passwd",
		},
		{
			name: "hidden escape",
			path: ".maestro/../.maestro/../Dockerfile",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidDockerfilePath(tt.path); got {
				t.Errorf("IsValidDockerfilePath(%q) = true, want false", tt.path)
			}
		})
	}
}

func TestIsValidDockerfilePathWithRoot(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, tempErr := os.MkdirTemp("", "config-test-*")
	if tempErr != nil {
		t.Fatalf("Failed to create temp dir: %v", tempErr)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	tests := []struct {
		name        string
		projectRoot string
		dockerfile  string
		want        bool
	}{
		{
			name:        "relative path in .maestro",
			projectRoot: tmpDir,
			dockerfile:  ".maestro/Dockerfile",
			want:        true,
		},
		{
			name:        "absolute path in .maestro",
			projectRoot: tmpDir,
			dockerfile:  filepath.Join(tmpDir, ".maestro", "Dockerfile"),
			want:        true,
		},
		{
			name:        "path outside .maestro",
			projectRoot: tmpDir,
			dockerfile:  "Dockerfile",
			want:        false,
		},
		{
			name:        "absolute path outside project",
			projectRoot: tmpDir,
			dockerfile:  "/etc/Dockerfile",
			want:        false,
		},
		{
			name:        "dot-dot escape with root",
			projectRoot: tmpDir,
			dockerfile:  ".maestro/../Dockerfile",
			want:        false,
		},
		{
			name:        "empty dockerfile path",
			projectRoot: tmpDir,
			dockerfile:  "",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsValidDockerfilePathWithRoot(tt.projectRoot, tt.dockerfile); got != tt.want {
				t.Errorf("IsValidDockerfilePathWithRoot(%q, %q) = %v, want %v",
					tt.projectRoot, tt.dockerfile, got, tt.want)
			}
		})
	}
}

func TestSetDockerfilePath_Valid(t *testing.T) {
	// Create a temporary directory for testing
	tmpDir, tempErr := os.MkdirTemp("", "config-test-*")
	if tempErr != nil {
		t.Fatalf("Failed to create temp dir: %v", tempErr)
	}
	defer os.RemoveAll(tmpDir)

	// Create .maestro directory
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if mkdirErr := os.MkdirAll(maestroDir, 0755); mkdirErr != nil {
		t.Fatalf("Failed to create .maestro dir: %v", mkdirErr)
	}

	// Initialize config with the temp directory as project dir
	mu.Lock()
	projectDir = tmpDir
	config = &Config{
		Container: &ContainerConfig{},
	}
	mu.Unlock()
	defer func() {
		mu.Lock()
		config = nil
		projectDir = ""
		mu.Unlock()
	}()

	// Test setting a valid path
	validPath := ".maestro/Dockerfile.custom"
	setErr := SetDockerfilePath(validPath)
	if setErr != nil {
		t.Errorf("SetDockerfilePath(%q) returned error: %v", validPath, setErr)
	}

	// Verify it was set
	if got := GetDockerfilePath(); got != validPath {
		t.Errorf("GetDockerfilePath() = %q, want %q", got, validPath)
	}
}

func TestSetDockerfilePath_Invalid(t *testing.T) {
	// Set up a config for testing
	SetConfigForTesting(&Config{
		Container: &ContainerConfig{},
	})
	defer SetConfigForTesting(nil)

	invalidPaths := []string{
		"Dockerfile",
		"docker/Dockerfile",
		"/etc/Dockerfile",
		".maestro/../Dockerfile",
	}

	for _, path := range invalidPaths {
		t.Run(path, func(t *testing.T) {
			err := SetDockerfilePath(path)
			if err == nil {
				t.Errorf("SetDockerfilePath(%q) should return error, got nil", path)
			}
		})
	}
}
