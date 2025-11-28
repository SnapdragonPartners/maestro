package orch

import (
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
