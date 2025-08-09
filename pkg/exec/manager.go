package exec

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// ExecutorManager handles executor initialization and selection.
// Deprecated: Simplified since we only support Docker execution now.
type ExecutorManager struct {
	logger   *logx.Logger
	registry *Registry
}

// NewExecutorManager creates a new executor manager.
// Deprecated: Config parameter ignored since we only support Docker now.
func NewExecutorManager(_ interface{}) *ExecutorManager {
	logger := logx.NewLogger("executor-manager")

	return &ExecutorManager{
		logger:   logger,
		registry: NewRegistry(),
	}
}

// Initialize sets up the executor registry.
// Deprecated: Simplified since we only support Docker execution now.
func (m *ExecutorManager) Initialize(_ context.Context) error {
	m.logger.Info("Initializing executor manager (Docker only)")

	// Register local executor (always available)
	localExec := NewLocalExec()
	if err := m.registry.Register(localExec); err != nil {
		return fmt.Errorf("failed to register local executor: %w", err)
	}

	// Register Docker executor with default image
	dockerExec := NewLongRunningDockerExec(config.DefaultGoDockerImage, "")
	if err := m.registry.Register(dockerExec); err != nil {
		return fmt.Errorf("failed to register docker executor: %w", err)
	}

	// Use Docker as default
	if err := m.registry.SetDefault("docker"); err != nil {
		return fmt.Errorf("failed to set default executor: %w", err)
	}

	m.logger.Info("Executor manager initialized with default: docker")
	return nil
}

// isDockerAvailable checks if Docker daemon is available.
func (m *ExecutorManager) isDockerAvailable(ctx context.Context) bool {
	// Check if docker command exists.
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	// Test Docker daemon connectivity with timeout.
	testCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(testCtx, dockerCmd, "version", "--format", "{{.Server.Version}}")
	err := cmd.Run()
	return err == nil
}

// isDockerImageAvailable checks if the default Docker image is available.
func (m *ExecutorManager) isDockerImageAvailable(ctx context.Context) bool {
	cmd := exec.CommandContext(ctx, "docker", "image", "inspect", config.DefaultGoDockerImage)
	if err := cmd.Run(); err == nil {
		return true
	}

	// Image not available locally - try to pull it.
	m.logger.Info("Docker image not available locally, attempting to pull: %s", config.DefaultGoDockerImage)
	pullCtx, cancel := context.WithTimeout(ctx, 300*time.Second) // 5 minute timeout
	defer cancel()

	pullCmd := exec.CommandContext(pullCtx, "docker", "pull", config.DefaultGoDockerImage)
	if err := pullCmd.Run(); err == nil {
		m.logger.Info("Successfully pulled Docker image: %s", config.DefaultGoDockerImage)
		return true
	}
	m.logger.Warn("Failed to pull Docker image: %s", config.DefaultGoDockerImage)

	return false
}
