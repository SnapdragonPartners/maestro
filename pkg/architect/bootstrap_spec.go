// Package architect provides the architect agent implementation.
package architect

import (
	"fmt"
	"os"
	"path/filepath"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	bootstraptpl "orchestrator/pkg/templates/bootstrap"
	"orchestrator/pkg/templates/packs"
	"orchestrator/pkg/workspace"
)

// RenderBootstrapSpec renders the technical bootstrap specification from requirement IDs.
// This is called when architect receives bootstrap_requirements in spec data.
// The architect loads config and language pack to render the full technical specification,
// keeping the PM agent simple and focused on requirements gathering.
func RenderBootstrapSpec(requirements []workspace.BootstrapRequirementID, logger *logx.Logger) (string, error) {
	if logger == nil {
		logger = logx.NewLogger("architect")
	}

	// Log received requirements
	logger.Info("Received bootstrap requirements: %v", requirements)

	cfg, err := config.GetConfig()
	if err != nil {
		return "", fmt.Errorf("failed to get config: %w", err)
	}

	// Determine platform from config
	platform := "generic"
	if cfg.Project != nil && cfg.Project.PrimaryPlatform != "" {
		platform = cfg.Project.PrimaryPlatform
	}

	// Load language pack
	pack, warnings, err := packs.Get(platform)
	if err != nil {
		return "", fmt.Errorf("failed to load pack for platform %s: %w", platform, err)
	}
	for _, w := range warnings {
		logger.Warn("Pack warning: %s", w)
	}

	// Convert requirement IDs to BootstrapFailure structs
	failures := workspace.RequirementIDsToFailures(requirements)

	// Get values from config
	projectName := ""
	if cfg.Project != nil {
		projectName = cfg.Project.Name
	}

	containerName := ""
	dockerfilePath := config.DefaultDockerfilePath
	if cfg.Container != nil {
		containerName = cfg.Container.Name
		if cfg.Container.Dockerfile != "" {
			dockerfilePath = cfg.Container.Dockerfile
		}
	}

	gitRepoURL := ""
	if cfg.Git != nil {
		gitRepoURL = cfg.Git.RepoURL
	}

	// Build template data from config
	data := bootstraptpl.NewTemplateDataWithConfig(
		projectName,
		platform,
		pack.DisplayName,
		containerName,
		gitRepoURL,
		dockerfilePath,
		failures,
	)

	// Set the language pack on the template data
	if _, packErr := data.SetPack(); packErr != nil {
		logger.Warn("Failed to set pack on template data: %v", packErr)
		// Continue without pack - template will render with defaults
	}

	// Render the template
	renderer, err := bootstraptpl.NewRenderer()
	if err != nil {
		return "", fmt.Errorf("failed to create bootstrap renderer: %w", err)
	}

	spec, err := renderer.RenderBootstrapSpecWithConfig(
		projectName,
		platform,
		containerName,
		gitRepoURL,
		dockerfilePath,
		failures,
	)
	if err != nil {
		return "", fmt.Errorf("failed to render bootstrap spec: %w", err)
	}

	// Log rendered spec size
	logger.Info("Rendered bootstrap spec: %d bytes", len(spec))

	// Optional: Write to temp file for debugging
	if os.Getenv("MAESTRO_DEBUG_BOOTSTRAP") != "" {
		debugPath := filepath.Join(os.TempDir(), "maestro-bootstrap-spec.md")
		if writeErr := os.WriteFile(debugPath, []byte(spec), 0644); writeErr != nil {
			logger.Warn("Failed to write debug bootstrap spec: %v", writeErr)
		} else {
			logger.Info("Bootstrap spec written to: %s", debugPath)
		}
	}

	return spec, nil
}
