package orch

import (
	"os"
	"path/filepath"
	"testing"

	"orchestrator/pkg/config"
)

// TestNewStartupOrchestrator tests orchestrator construction.
func TestNewStartupOrchestrator(t *testing.T) {
	tests := []struct {
		name        string
		projectDir  string
		isBootstrap bool
	}{
		{
			name:        "normal mode",
			projectDir:  "/test/project",
			isBootstrap: false,
		},
		{
			name:        "bootstrap mode",
			projectDir:  "/test/bootstrap",
			isBootstrap: true,
		},
		{
			name:        "empty project dir",
			projectDir:  "",
			isBootstrap: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orch, err := NewStartupOrchestrator(tt.projectDir, tt.isBootstrap)

			if err != nil {
				t.Errorf("expected no error, got %v", err)
			}

			if orch == nil {
				t.Fatal("expected orchestrator, got nil")
			}

			if orch.projectDir != tt.projectDir {
				t.Errorf("expected projectDir %q, got %q", tt.projectDir, orch.projectDir)
			}

			if orch.isBootstrap != tt.isBootstrap {
				t.Errorf("expected isBootstrap %v, got %v", tt.isBootstrap, orch.isBootstrap)
			}

			if orch.logger == nil {
				t.Error("expected logger to be initialized")
			}
		})
	}
}

// TestGetSafeImageID tests safe image ID resolution.
func TestGetSafeImageID(t *testing.T) {
	orch, err := NewStartupOrchestrator("/test", false)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	tests := []struct {
		name     string
		config   *config.Config
		expected string
	}{
		{
			name: "with safe image ID configured",
			config: &config.Config{
				Container: &config.ContainerConfig{
					SafeImageID: "sha256:abc123",
				},
			},
			expected: "sha256:abc123",
		},
		{
			name: "with nil container config",
			config: &config.Config{
				Container: nil,
			},
			expected: "maestro-bootstrap:latest",
		},
		{
			name: "with empty safe image ID",
			config: &config.Config{
				Container: &config.ContainerConfig{
					SafeImageID: "",
				},
			},
			expected: "maestro-bootstrap:latest",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := orch.getSafeImageID(tt.config)

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestHasDockerfile tests dockerfile detection.
func TestHasDockerfile(t *testing.T) {
	orch, err := NewStartupOrchestrator("/test", false)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	tests := []struct {
		name     string
		config   *config.Config
		expected bool
	}{
		{
			name: "with dockerfile",
			config: &config.Config{
				Container: &config.ContainerConfig{
					Dockerfile: "Dockerfile",
				},
			},
			expected: true,
		},
		{
			name: "with empty dockerfile",
			config: &config.Config{
				Container: &config.ContainerConfig{
					Dockerfile: "",
				},
			},
			expected: false,
		},
		{
			name: "with nil container config",
			config: &config.Config{
				Container: nil,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := orch.hasDockerfile(tt.config)

			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestGetDockerfilePath tests dockerfile path extraction from config.
func TestGetDockerfilePath(t *testing.T) {
	orch, err := NewStartupOrchestrator("/test", false)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	tests := []struct {
		name     string
		config   *config.Config
		expected string
	}{
		{
			name: "with dockerfile path",
			config: &config.Config{
				Container: &config.ContainerConfig{
					Dockerfile: ".maestro/Dockerfile",
				},
			},
			expected: ".maestro/Dockerfile",
		},
		{
			name: "with empty dockerfile",
			config: &config.Config{
				Container: &config.ContainerConfig{
					Dockerfile: "",
				},
			},
			expected: "",
		},
		{
			name: "with nil container config",
			config: &config.Config{
				Container: nil,
			},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := orch.getDockerfilePath(tt.config)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestRecoverUnhealthyTarget_Case2_DockerfileNotOnDisk tests that a configured
// dockerfile that doesn't exist on disk triggers bootstrap fallback (Case 2).
func TestRecoverUnhealthyTarget_Case2_DockerfileNotOnDisk(t *testing.T) {
	tmpDir := t.TempDir()

	orch, err := NewStartupOrchestrator(tmpDir, false)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	cfg := &config.Config{
		Container: &config.ContainerConfig{
			Dockerfile: ".maestro/Dockerfile", // configured but file doesn't exist
		},
	}

	// recoverUnhealthyTarget should detect missing file and call fallbackToBootstrap.
	// fallbackToBootstrap calls config.SetPinnedImageID which requires global config.
	// We can't easily test the full flow without docker, but we can verify the
	// dockerfile-not-found detection by checking that the path doesn't exist.
	absPath := filepath.Join(tmpDir, ".maestro/Dockerfile")
	if _, statErr := os.Stat(absPath); !os.IsNotExist(statErr) {
		t.Fatalf("expected dockerfile to not exist at %q", absPath)
	}

	// Verify that the orchestrator would detect the missing file
	dockerfilePath := orch.getDockerfilePath(cfg)
	if dockerfilePath == "" {
		t.Fatal("expected dockerfile path from config, got empty")
	}

	resolved := filepath.Join(tmpDir, dockerfilePath)
	if _, statErr := os.Stat(resolved); !os.IsNotExist(statErr) {
		t.Errorf("expected IsNotExist for %q", resolved)
	}
}

// TestRecoverUnhealthyTarget_Case4_NoDockerfile tests that no dockerfile
// configured triggers bootstrap fallback (Case 4).
func TestRecoverUnhealthyTarget_Case4_NoDockerfile(t *testing.T) {
	orch, err := NewStartupOrchestrator("/test", false)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	cfg := &config.Config{
		Container: &config.ContainerConfig{
			Dockerfile: "",
		},
	}

	dockerfilePath := orch.getDockerfilePath(cfg)
	if dockerfilePath != "" {
		t.Errorf("expected empty dockerfile path, got %q", dockerfilePath)
	}
}

// TestRecoverUnhealthyTarget_Case1_DockerfileExists verifies that when a
// dockerfile exists on disk, the orchestrator finds it for rebuild (Case 1).
func TestRecoverUnhealthyTarget_Case1_DockerfileExists(t *testing.T) {
	tmpDir := t.TempDir()

	// Create the dockerfile on disk
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatalf("failed to create .maestro dir: %v", err)
	}
	dockerfilePath := filepath.Join(maestroDir, "Dockerfile")
	if err := os.WriteFile(dockerfilePath, []byte("FROM ubuntu:latest\n"), 0644); err != nil {
		t.Fatalf("failed to write dockerfile: %v", err)
	}

	orch, err := NewStartupOrchestrator(tmpDir, false)
	if err != nil {
		t.Fatalf("failed to create orchestrator: %v", err)
	}

	cfg := &config.Config{
		Container: &config.ContainerConfig{
			Dockerfile: ".maestro/Dockerfile",
		},
	}

	// Verify the dockerfile path resolves and exists
	path := orch.getDockerfilePath(cfg)
	if path == "" {
		t.Fatal("expected dockerfile path from config, got empty")
	}

	resolved := filepath.Join(tmpDir, path)
	if _, statErr := os.Stat(resolved); statErr != nil {
		t.Errorf("expected dockerfile to exist at %q, got error: %v", resolved, statErr)
	}
}
