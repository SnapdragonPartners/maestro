package exec

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

// ExecutorManager handles executor initialization and selection.
type ExecutorManager struct {
	config   *config.ExecutorConfig
	logger   *logx.Logger
	registry *Registry
}

// NewExecutorManager creates a new executor manager.
func NewExecutorManager(config *config.ExecutorConfig) *ExecutorManager {
	logger := logx.NewLogger("executor-manager")

	return &ExecutorManager{
		config:   config,
		logger:   logger,
		registry: NewRegistry(),
	}
}

// Initialize sets up the executor registry based on configuration.
func (m *ExecutorManager) Initialize(ctx context.Context) error {
	m.logger.Info("Initializing executor manager with type: %s", m.config.Type)

	// Register local executor (always available)
	localExec := NewLocalExec()
	if err := m.registry.Register(localExec); err != nil {
		return fmt.Errorf("failed to register local executor: %w", err)
	}

	// Register Docker executor if available.
	dockerExec := NewLongRunningDockerExec(m.config.Docker.Image, "")
	if err := m.registry.Register(dockerExec); err != nil {
		return fmt.Errorf("failed to register docker executor: %w", err)
	}

	// Determine default executor based on configuration.
	defaultExec, err := m.selectDefaultExecutor(ctx)
	if err != nil {
		return fmt.Errorf("failed to select default executor: %w", err)
	}

	if err := m.registry.SetDefault(defaultExec); err != nil {
		return fmt.Errorf("failed to set default executor: %w", err)
	}

	m.logger.Info("Executor manager initialized with default: %s", defaultExec)
	return nil
}

// selectDefaultExecutor determines which executor to use based on configuration.
func (m *ExecutorManager) selectDefaultExecutor(ctx context.Context) (string, error) {
	switch m.config.Type {
	case "local":
		m.logger.Warn("Using local executor - commands will run without sandboxing!")
		return "local", nil

	case "docker":
		// Force Docker - fail if not available.
		if !m.isDockerAvailable(ctx) {
			return "", fmt.Errorf("docker executor requested but Docker daemon is not available")
		}
		if !m.isDockerImageAvailable(ctx) {
			return "", fmt.Errorf("docker executor requested but image '%s' is not available", m.config.Docker.Image)
		}
		return "docker", nil

	case "auto":
		// Auto-select Docker only - fail if not available.
		if !m.isDockerAvailable(ctx) {
			return "", fmt.Errorf("docker daemon is not available (required for auto mode). Use 'local' explicitly if you want unsandboxed execution")
		}
		if !m.isDockerImageAvailable(ctx) {
			return "", fmt.Errorf("docker image '%s' is not available (required for auto mode). Use 'local' explicitly if you want unsandboxed execution", m.config.Docker.Image)
		}

		m.logger.Info("Auto-selected Docker executor (available and image exists)")
		return "docker", nil

	default:
		return "", fmt.Errorf("unknown executor type: %s", m.config.Type)
	}
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

	if _, err := exec.LookPath(dockerCmd); err != nil {
		m.logger.Debug("Docker command not found: %v", err)
		return false
	}

	// Check if daemon is running.
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, dockerCmd, "info")
	if err := cmd.Run(); err != nil {
		m.logger.Debug("Docker daemon not available: %v", err)
		return false
	}

	return true
}

// isDockerImageAvailable checks if the configured Docker image is available.
func (m *ExecutorManager) isDockerImageAvailable(ctx context.Context) bool {
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	// Check if image exists locally.
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	cmd := exec.CommandContext(timeoutCtx, dockerCmd, "images", "-q", m.config.Docker.Image)
	output, err := cmd.Output()
	if err != nil {
		m.logger.Debug("Failed to check Docker image: %v", err)
		return false
	}

	if strings.TrimSpace(string(output)) != "" {
		m.logger.Debug("Docker image %s found locally", m.config.Docker.Image)
		return true
	}

	// Try to pull image if auto-pull is enabled.
	if m.config.Docker.AutoPull {
		m.logger.Info("Attempting to pull Docker image: %s", m.config.Docker.Image)

		pullCtx, cancel := context.WithTimeout(ctx, time.Duration(m.config.Docker.PullTimeout)*time.Second)
		defer cancel()

		cmd = exec.CommandContext(pullCtx, dockerCmd, "pull", m.config.Docker.Image)
		if err := cmd.Run(); err != nil {
			m.logger.Warn("Failed to pull Docker image %s: %v", m.config.Docker.Image, err)
			return false
		}

		m.logger.Info("Successfully pulled Docker image: %s", m.config.Docker.Image)
		return true
	}

	m.logger.Debug("Docker image %s not found and auto-pull disabled", m.config.Docker.Image)
	return false
}

// GetExecutor returns the best available executor.
func (m *ExecutorManager) GetExecutor(preferences []string) (Executor, error) {
	return m.registry.GetBest(preferences)
}

// GetDefaultExecutor returns the default executor.
func (m *ExecutorManager) GetDefaultExecutor() (Executor, error) {
	return m.registry.GetDefault()
}

// GetRegistry returns the executor registry.
func (m *ExecutorManager) GetRegistry() *Registry {
	return m.registry
}

// GetStatus returns the status of all executors.
func (m *ExecutorManager) GetStatus() map[string]bool {
	status := make(map[string]bool)

	for _, name := range m.registry.List() {
		if executor, err := m.registry.Get(name); err == nil {
			status[name] = executor.Available()
		}
	}

	return status
}

// GetStartupInfo returns information about executor configuration for startup banner.
func (m *ExecutorManager) GetStartupInfo() string {
	status := m.GetStatus()

	var parts []string

	// Show configured type.
	parts = append(parts, fmt.Sprintf("Type: %s", m.config.Type))

	// Show Docker status.
	if dockerAvailable, ok := status["docker"]; ok {
		if dockerAvailable {
			parts = append(parts, fmt.Sprintf("Docker: available (%s)", m.config.Docker.Image))
		} else {
			parts = append(parts, "Docker: unavailable")
		}
	}

	// Show local status.
	if localAvailable, ok := status["local"]; ok {
		if localAvailable {
			parts = append(parts, "Local: available")
		} else {
			parts = append(parts, "Local: unavailable")
		}
	}

	// Show default executor.
	if defaultExec, err := m.GetDefaultExecutor(); err == nil {
		parts = append(parts, fmt.Sprintf("Default: %s", defaultExec.Name()))
	}

	return strings.Join(parts, ", ")
}
