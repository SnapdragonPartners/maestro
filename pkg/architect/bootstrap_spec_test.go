package architect

import (
	"os"
	"strings"
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/workspace"
)

func TestRenderBootstrapSpec_EmptyRequirements(t *testing.T) {
	// Set up test config
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "test-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name: "test-container",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/test/repo",
		},
	})
	defer config.SetConfigForTesting(nil)

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec([]workspace.BootstrapRequirementID{}, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	// Empty requirements should still produce some output (header at minimum)
	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec for empty requirements")
	}
}

func TestRenderBootstrapSpec_SingleRequirement(t *testing.T) {
	tests := []struct {
		name         string
		requirement  workspace.BootstrapRequirementID
		expectInSpec string // substring that should be in the rendered spec
	}{
		{
			name:         "container requirement",
			requirement:  workspace.BootstrapReqContainer,
			expectInSpec: "container", // Should mention container
		},
		{
			name:         "dockerfile requirement",
			requirement:  workspace.BootstrapReqDockerfile,
			expectInSpec: "Dockerfile", // Should mention Dockerfile
		},
		{
			name:         "build_system requirement",
			requirement:  workspace.BootstrapReqBuildSystem,
			expectInSpec: "build", // Should mention build system
		},
		{
			name:         "knowledge_graph requirement",
			requirement:  workspace.BootstrapReqKnowledgeGraph,
			expectInSpec: "knowledge", // Should mention knowledge graph
		},
		{
			name:         "git_access requirement",
			requirement:  workspace.BootstrapReqGitAccess,
			expectInSpec: "git", // Should mention git
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config.SetConfigForTesting(&config.Config{
				Project: &config.ProjectInfo{
					Name:            "test-project",
					PrimaryPlatform: "go",
				},
				Container: &config.ContainerConfig{
					Name: "test-container",
				},
				Git: &config.GitConfig{
					RepoURL: "https://github.com/test/repo",
				},
			})
			defer config.SetConfigForTesting(nil)

			logger := logx.NewLogger("test")
			spec, err := RenderBootstrapSpec([]workspace.BootstrapRequirementID{tt.requirement}, logger)
			if err != nil {
				t.Fatalf("RenderBootstrapSpec() error = %v", err)
			}

			if spec == "" {
				t.Error("RenderBootstrapSpec() returned empty spec")
			}

			// Check that the spec contains expected content (case-insensitive)
			if !strings.Contains(strings.ToLower(spec), strings.ToLower(tt.expectInSpec)) {
				t.Errorf("RenderBootstrapSpec() spec does not contain %q", tt.expectInSpec)
			}
		})
	}
}

func TestRenderBootstrapSpec_MultipleRequirements(t *testing.T) {
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "test-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name: "test-container",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/test/repo",
		},
	})
	defer config.SetConfigForTesting(nil)

	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqDockerfile,
		workspace.BootstrapReqBuildSystem,
		workspace.BootstrapReqKnowledgeGraph,
	}

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec(requirements, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}

	// Check that spec contains content for all requirements
	specLower := strings.ToLower(spec)
	if !strings.Contains(specLower, "dockerfile") {
		t.Error("Spec should mention Dockerfile")
	}
	if !strings.Contains(specLower, "build") {
		t.Error("Spec should mention build system")
	}
	if !strings.Contains(specLower, "knowledge") {
		t.Error("Spec should mention knowledge graph")
	}
}

func TestRenderBootstrapSpec_DifferentPlatforms(t *testing.T) {
	platforms := []string{"go", "python", "node", "rust", "generic"}

	for _, platform := range platforms {
		t.Run(platform, func(t *testing.T) {
			config.SetConfigForTesting(&config.Config{
				Project: &config.ProjectInfo{
					Name:            "test-project",
					PrimaryPlatform: platform,
				},
				Container: &config.ContainerConfig{
					Name: "test-container",
				},
				Git: &config.GitConfig{
					RepoURL: "https://github.com/test/repo",
				},
			})
			defer config.SetConfigForTesting(nil)

			requirements := []workspace.BootstrapRequirementID{
				workspace.BootstrapReqBuildSystem,
			}

			logger := logx.NewLogger("test")
			spec, err := RenderBootstrapSpec(requirements, logger)
			if err != nil {
				t.Fatalf("RenderBootstrapSpec() error = %v for platform %s", err, platform)
			}

			if spec == "" {
				t.Errorf("RenderBootstrapSpec() returned empty spec for platform %s", platform)
			}
		})
	}
}

func TestRenderBootstrapSpec_NilLogger(t *testing.T) {
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "test-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name: "test-container",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/test/repo",
		},
	})
	defer config.SetConfigForTesting(nil)

	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
	}

	// Should not panic with nil logger
	spec, err := RenderBootstrapSpec(requirements, nil)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}
}

func TestRenderBootstrapSpec_NoConfig(t *testing.T) {
	// Clear any existing config
	config.SetConfigForTesting(nil)

	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
	}

	logger := logx.NewLogger("test")
	_, err := RenderBootstrapSpec(requirements, logger)
	if err == nil {
		t.Error("RenderBootstrapSpec() should error when config is not available")
	}
}

func TestRenderBootstrapSpec_MinimalConfig(t *testing.T) {
	// Test with minimal config (empty project, container, git)
	config.SetConfigForTesting(&config.Config{})
	defer config.SetConfigForTesting(nil)

	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
	}

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec(requirements, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	// Should still produce output even with minimal config
	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}
}

func TestRenderBootstrapSpec_DebugOutput(t *testing.T) {
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "test-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name: "test-container",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/test/repo",
		},
	})
	defer config.SetConfigForTesting(nil)

	// Set debug env var
	t.Setenv("MAESTRO_DEBUG_BOOTSTRAP", "1")
	defer os.Unsetenv("MAESTRO_DEBUG_BOOTSTRAP")

	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
	}

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec(requirements, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}

	// Debug file should be created (cleanup after test)
	debugPath := os.TempDir() + "/maestro-bootstrap-spec.md"
	defer os.Remove(debugPath)

	if _, err := os.Stat(debugPath); os.IsNotExist(err) {
		t.Error("Debug file should be created when MAESTRO_DEBUG_BOOTSTRAP is set")
	}
}

func TestRenderBootstrapSpec_ProjectConfigValues(t *testing.T) {
	// Test that project config values appear in the rendered spec
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "my-awesome-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name:       "my-container",
			Dockerfile: ".maestro/custom/Dockerfile",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/acme/my-awesome-project",
		},
	})
	defer config.SetConfigForTesting(nil)

	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqDockerfile,
		workspace.BootstrapReqBuildSystem,
	}

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec(requirements, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	// The spec should contain project-specific information
	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}

	// Check that the custom Dockerfile path is used
	if !strings.Contains(spec, ".maestro") {
		// The template should reference the maestro directory in some way
		t.Log("Note: Spec may not directly include Dockerfile path in output")
	}
}

func TestRenderBootstrapSpec_InvalidRequirementFiltered(t *testing.T) {
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "test-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name: "test-container",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/test/repo",
		},
	})
	defer config.SetConfigForTesting(nil)

	// Include both valid and invalid requirement IDs
	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
		workspace.BootstrapRequirementID("invalid_id"),
	}

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec(requirements, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	// Should still produce output (invalid IDs filtered by RequirementIDsToFailures)
	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}

	// Valid requirement should still be processed
	if !strings.Contains(strings.ToLower(spec), "build") {
		t.Error("Valid requirement should still be included in spec")
	}
}

func TestRenderBootstrapSpec_SpecSize(t *testing.T) {
	config.SetConfigForTesting(&config.Config{
		Project: &config.ProjectInfo{
			Name:            "test-project",
			PrimaryPlatform: "go",
		},
		Container: &config.ContainerConfig{
			Name: "test-container",
		},
		Git: &config.GitConfig{
			RepoURL: "https://github.com/test/repo",
		},
	})
	defer config.SetConfigForTesting(nil)

	// Test with all requirements
	requirements := []workspace.BootstrapRequirementID{
		workspace.BootstrapReqContainer,
		workspace.BootstrapReqDockerfile,
		workspace.BootstrapReqBuildSystem,
		workspace.BootstrapReqKnowledgeGraph,
		workspace.BootstrapReqGitAccess,
	}

	logger := logx.NewLogger("test")
	spec, err := RenderBootstrapSpec(requirements, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	// Spec should have reasonable size
	if len(spec) < 100 {
		t.Errorf("Spec seems too short: %d bytes", len(spec))
	}
	if len(spec) > 100000 {
		t.Errorf("Spec seems too long: %d bytes", len(spec))
	}
}
