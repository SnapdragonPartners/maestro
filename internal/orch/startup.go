// Package orch provides startup orchestration functionality.
package orch

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
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

	// Check if safe container is healthy
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
	}

	o.logger.Info("✅ Safe container %s is healthy", safeImageID)
	return nil
}

// validateTargetContainer validates the target container and offers interactive recovery.
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

	// Cases D/E: Offer interactive rebuild if dockerfile is available
	if o.hasDockerfile(&cfg) {
		return o.offerInteractiveRebuild(ctx, &cfg, pinnedImageID)
	}

	// No dockerfile available
	return fmt.Errorf("target image %s unavailable and no dockerfile configured - provide image or run with --bootstrap", pinnedImageID)
}

// offerInteractiveRebuild offers to rebuild the container from dockerfile.
func (o *StartupOrchestrator) offerInteractiveRebuild(ctx context.Context, cfg *config.Config, currentImageID string) error {
	// Check if image exists but is unhealthy vs missing entirely
	imageExists := true
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", currentImageID)
	if cmd.Run() != nil {
		imageExists = false
	}

	var prompt string
	if imageExists {
		prompt = fmt.Sprintf("Target image %s exists but is not healthy. Rebuild from dockerfile? (y/N): ", currentImageID)
	} else {
		prompt = fmt.Sprintf("Target image %s does not exist. Build from dockerfile? (y/N): ", currentImageID)
	}

	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	response, err := reader.ReadString('\n')
	if err != nil {
		return fmt.Errorf("failed to read user input: %w", err)
	}

	response = strings.TrimSpace(strings.ToLower(response))
	if response != "y" && response != "yes" {
		return fmt.Errorf("user declined rebuild - provide valid image or run with --bootstrap")
	}

	// Proceed with rebuild
	o.logger.Info("🔨 Building container from dockerfile...")
	if err := o.buildContainerFromConfig(ctx, cfg); err != nil {
		return fmt.Errorf("container build failed: %w", err)
	}

	return nil
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

	// Generate a new image name with timestamp
	imageName := fmt.Sprintf("%s:latest", cfg.Container.Name)

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
