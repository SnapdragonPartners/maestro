package architect

import (
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/workspace"
)

// TestRenderBootstrapSpec_WrapperDelegates validates the architect wrapper delegates to specrender.
func TestRenderBootstrapSpec_WrapperDelegates(t *testing.T) {
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
	spec, err := RenderBootstrapSpec([]workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
	}, logger)
	if err != nil {
		t.Fatalf("RenderBootstrapSpec() error = %v", err)
	}

	if spec == "" {
		t.Error("RenderBootstrapSpec() returned empty spec")
	}
}

// TestRenderBootstrapSpec_WrapperNoConfig validates error propagation.
func TestRenderBootstrapSpec_WrapperNoConfig(t *testing.T) {
	config.SetConfigForTesting(nil)

	logger := logx.NewLogger("test")
	_, err := RenderBootstrapSpec([]workspace.BootstrapRequirementID{
		workspace.BootstrapReqBuildSystem,
	}, logger)
	if err == nil {
		t.Error("RenderBootstrapSpec() should error when config is not available")
	}
}
