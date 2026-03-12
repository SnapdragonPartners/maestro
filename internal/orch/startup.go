// Package orch provides startup orchestration functionality.
package orch

import (
	"context"
	"fmt"
	"strings"

	"orchestrator/internal/utils"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// StartupOrchestrator provides startup orchestration with container validation.
type StartupOrchestrator struct {
	logger      *logx.Logger
	projectDir  string
	isBootstrap bool
}

// NewStartupOrchestrator creates a new startup orchestrator.
func NewStartupOrchestrator(projectDir string, isBootstrap bool) (*StartupOrchestrator, error) {
	return &StartupOrchestrator{
		projectDir:  projectDir,
		isBootstrap: isBootstrap,
		logger:      logx.NewLogger("startup-orch"),
	}, nil
}

// OnStart performs startup orchestration with container validation.
func (o *StartupOrchestrator) OnStart(ctx context.Context) error {
	o.logger.Info("🚀 Starting orchestrator validation")

	// Always ensure safe container is healthy
	if err := o.ensureSafeContainerHealthy(ctx); err != nil {
		return fmt.Errorf("safe container validation failed: %w", err)
	}

	if o.isBootstrap {
		o.logger.Info("✅ Bootstrap mode: Safe container validated")
		return nil
	}

	// Normal mode: also validate target container
	if err := o.validateTargetContainer(ctx); err != nil {
		return fmt.Errorf("target container validation failed: %w", err)
	}

	o.logger.Info("✅ Orchestrator validation complete")
	return nil
}

// ensureSafeContainerHealthy ensures the safe/bootstrap container exists and is healthy.
// If the image is missing, it builds it automatically from the embedded Dockerfile and
// pre-compiled MCP proxy binary.
func (o *StartupOrchestrator) ensureSafeContainerHealthy(ctx context.Context) error {
	// Get safe container configuration
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	// Get safe container image ID (this should be a constant or configured value)
	safeImageID := o.getSafeImageID(&cfg)
	if safeImageID == "" {
		return fmt.Errorf("safe container image ID not configured")
	}

	// Check if safe container is healthy and up to date
	if err := utils.IsImageHealthy(ctx, safeImageID); err != nil {
		o.logger.Warn("⚠️ Safe container %s is not available: %v", safeImageID, err)
		o.logger.Info("🔨 Building bootstrap container from embedded Dockerfile...")

		if buildErr := utils.BuildBootstrapImage(ctx); buildErr != nil {
			return fmt.Errorf("failed to build bootstrap container: %w", buildErr)
		}

		// Verify the newly built image is healthy using the tag we just built
		if verifyErr := utils.IsImageHealthy(ctx, config.BootstrapContainerTag); verifyErr != nil {
			return fmt.Errorf("newly built bootstrap container is not healthy: %w", verifyErr)
		}
	} else if utils.IsBootstrapStale(ctx) {
		o.logger.Info("🔨 Bootstrap container is stale, rebuilding...")
		if buildErr := utils.BuildBootstrapImage(ctx); buildErr != nil {
			return fmt.Errorf("failed to rebuild bootstrap container: %w", buildErr)
		}
	}

	o.logger.Info("✅ Safe container %s is healthy", safeImageID)
	return nil
}

// validateTargetContainer validates the target container and recovers automatically.
//
// Recovery cases when the target container is unhealthy:
//
//	Case 1: Dockerfile configured → auto-rebuild from repository clone
//	Case 2: No dockerfile configured → fall back to bootstrap
//	Case 3: Rebuild attempted but failed → fall back to bootstrap
func (o *StartupOrchestrator) validateTargetContainer(ctx context.Context) error {
	cfg, err := config.GetConfig()
	if err != nil {
		return fmt.Errorf("failed to get config: %w", err)
	}

	pinnedImageID := config.GetPinnedImageID()
	safeImageID := o.getSafeImageID(&cfg)

	// Case A: No pinned image ID - resolve container name to SHA256 and save it
	// This happens after bootstrap when no target container has been built yet
	if pinnedImageID == "" {
		o.logger.Info("ℹ️  No pinned image ID configured, resolving container name: %s", cfg.Container.Name)

		// Resolve the container name to its SHA256
		imageID, err := utils.GetImageID(ctx, cfg.Container.Name)
		if err != nil {
			return fmt.Errorf("failed to resolve container name '%s' to image ID: %w", cfg.Container.Name, err)
		}

		// Validate that the image is healthy
		if err := utils.IsImageHealthy(ctx, imageID); err != nil {
			return fmt.Errorf("container '%s' (image %s) is not healthy: %w", cfg.Container.Name, imageID, err)
		}

		// Save the pinned image ID for future startups
		if err := config.SetPinnedImageID(imageID); err != nil {
			return fmt.Errorf("failed to save pinned image ID: %w", err)
		}

		o.logger.Info("✅ Resolved and pinned container: %s → %s", cfg.Container.Name, imageID)
		return nil
	}

	// Case B: Pinned image is the safe image - this means we're in bootstrap mode
	if pinnedImageID == safeImageID {
		o.logger.Info("✅ Using safe/bootstrap container: %s", safeImageID)
		return nil
	}

	// Case C: We have a target image pinned - validate it's healthy
	if err := utils.IsImageHealthy(ctx, pinnedImageID); err == nil {
		o.logger.Info("✅ Target container %s is healthy", pinnedImageID)
		return nil
	}

	o.logger.Warn("⚠️  Target container %s is not healthy", pinnedImageID)

	// Target is unhealthy — attempt recovery based on dockerfile availability
	return o.recoverUnhealthyTarget(ctx, &cfg, safeImageID)
}

// recoveryCase identifies the recovery action for an unhealthy target container.
type recoveryCase int

const (
	// recoveryCaseRebuild means a Dockerfile is configured and a rebuild should be attempted.
	recoveryCaseRebuild recoveryCase = 1
	// recoveryCaseNoDockerfile means no Dockerfile is configured at all.
	recoveryCaseNoDockerfile recoveryCase = 2
)

// detectRecoveryCase determines which recovery path to take based on config.
//
// The Dockerfile lives in the git repository, not in the project root directory.
// We cannot check for it on the local filesystem — the build process clones the
// repo and uses the Dockerfile from there. If the Dockerfile has been deleted from
// the repo, the build will fail and we fall back to bootstrap mode (Case 3 in logs).
//
// This is a pure detection function with no side effects, making it unit-testable.
func (o *StartupOrchestrator) detectRecoveryCase(cfg *config.Config) (recoveryCase, string) {
	dockerfilePath := o.getDockerfilePath(cfg)

	if dockerfilePath == "" {
		return recoveryCaseNoDockerfile, ""
	}

	return recoveryCaseRebuild, dockerfilePath
}

// recoverUnhealthyTarget handles recovery when the target container is unhealthy.
// It attempts to rebuild from dockerfile if available, or falls back to bootstrap mode.
func (o *StartupOrchestrator) recoverUnhealthyTarget(ctx context.Context, cfg *config.Config, safeImageID string) error {
	rc, dockerfilePath := o.detectRecoveryCase(cfg)

	switch rc {
	case recoveryCaseNoDockerfile:
		o.logger.Warn("⚠️  No dockerfile configured — falling back to bootstrap mode")
		return o.fallbackToBootstrap(safeImageID)

	case recoveryCaseRebuild:
		o.logger.Info("🔨 Dockerfile configured at %q — rebuilding target container from repository", dockerfilePath)
		if err := o.buildContainerFromConfig(ctx, cfg); err != nil {
			o.logger.Warn("⚠️  Rebuild from dockerfile failed: %v", err)
			o.logger.Warn("⚠️  Falling back to bootstrap mode — PM will handle Dockerfile remediation")
			return o.fallbackToBootstrap(safeImageID)
		}
		o.logger.Info("✅ Target container rebuilt successfully from dockerfile")
		return nil

	default:
		o.logger.Warn("⚠️  Unknown recovery case %d — falling back to bootstrap mode", rc)
		return o.fallbackToBootstrap(safeImageID)
	}
}

// fallbackToBootstrap resets the pinned image to the safe container, allowing the
// system to continue in bootstrap mode. The PM's bootstrap detection will pick up
// any missing components (including Dockerfile) and create stories to address them.
func (o *StartupOrchestrator) fallbackToBootstrap(safeImageID string) error {
	o.logger.Info("🔄 Resetting pinned image to safe container: %s", safeImageID)
	if err := config.SetPinnedImageID(safeImageID); err != nil {
		return fmt.Errorf("failed to reset pinned image to safe container: %w", err)
	}
	o.logger.Info("✅ Falling back to safe container — PM will handle recovery via bootstrap detection")
	return nil
}

// getDockerfilePath returns the dockerfile path from config, or empty string if not configured.
func (o *StartupOrchestrator) getDockerfilePath(cfg *config.Config) string {
	if cfg.Container != nil && cfg.Container.Dockerfile != "" {
		return cfg.Container.Dockerfile
	}
	return ""
}

// buildContainerFromConfig builds a container using the configured dockerfile.
func (o *StartupOrchestrator) buildContainerFromConfig(ctx context.Context, cfg *config.Config) error {
	if cfg.Container == nil || cfg.Container.Dockerfile == "" {
		return fmt.Errorf("no dockerfile configured")
	}

	// Get repository URL for cloning
	repoURL := cfg.Git.RepoURL
	if repoURL == "" {
		return fmt.Errorf("no repository URL configured")
	}

	// Create temporary clone
	tempDir, cleanup, err := utils.CreateTempRepoClone(ctx, repoURL, "")
	if err != nil {
		return fmt.Errorf("failed to create temp repo clone: %w", err)
	}
	defer cleanup()

	// Use the configured container name, adding :latest tag only if no tag is present
	imageName := cfg.Container.Name
	if !strings.Contains(imageName, ":") {
		imageName += ":latest"
	}

	// Build container
	dockerfilePath := cfg.Container.Dockerfile
	if buildErr := utils.BuildContainerFromDockerfile(ctx, dockerfilePath, imageName, tempDir); buildErr != nil {
		return fmt.Errorf("failed to build container: %w", buildErr)
	}

	// Get the new image ID
	newImageID, err := utils.GetImageID(ctx, imageName)
	if err != nil {
		return fmt.Errorf("failed to get new image ID: %w", err)
	}

	// Update pinned image ID in config
	if err := config.SetPinnedImageID(newImageID); err != nil {
		return fmt.Errorf("failed to update pinned image ID: %w", err)
	}

	o.logger.Info("✅ Container built successfully: %s", newImageID)
	o.logger.Info("✅ Updated pinned image ID in configuration")

	return nil
}

// getSafeImageID returns the safe container image ID from configuration.
func (o *StartupOrchestrator) getSafeImageID(cfg *config.Config) string {
	// This should return a configured safe image ID
	// For now, use a placeholder - this needs to be properly configured
	if cfg.Container != nil && cfg.Container.SafeImageID != "" {
		return cfg.Container.SafeImageID
	}
	return "maestro-bootstrap:latest" // Default safe image
}

// hasDockerfile checks if a dockerfile is configured.
func (o *StartupOrchestrator) hasDockerfile(cfg *config.Config) bool {
	return cfg.Container != nil && cfg.Container.Dockerfile != ""
}
