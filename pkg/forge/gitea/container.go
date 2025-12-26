// Package gitea provides Gitea-specific forge implementation for airplane mode.
// This package manages the local Gitea container lifecycle and API client.
package gitea

import (
	"context"
	"fmt"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"orchestrator/pkg/logx"
)

// Container configuration constants.
const (
	// GiteaImage is the pinned Gitea Docker image version.
	// Using version 1.25 for faster startup (smaller image size).
	GiteaImage = "gitea/gitea:1.25"

	// DefaultHTTPPort is the default HTTP port for Gitea.
	DefaultHTTPPort = 3000

	// DefaultSSHPort is the default SSH port for Gitea.
	DefaultSSHPort = 2222

	// ContainerPrefix is the prefix for Gitea container names.
	ContainerPrefix = "maestro-gitea-"

	// VolumePrefix is the prefix for Gitea volume names.
	VolumePrefix = "maestro-gitea-"

	// VolumeSuffix is the suffix for Gitea volume names.
	VolumeSuffix = "-data"

	// HealthCheckTimeout is the timeout for individual health check requests.
	HealthCheckTimeout = 5 * time.Second

	// DefaultReadyTimeout is the default timeout for waiting for Gitea to be ready.
	DefaultReadyTimeout = 60 * time.Second

	// HealthCheckInterval is the interval between health check attempts.
	HealthCheckInterval = 500 * time.Millisecond
)

// ContainerConfig holds configuration for a Gitea container.
type ContainerConfig struct {
	// ProjectName is used to generate unique container and volume names.
	ProjectName string

	// HTTPPort is the host port for Gitea HTTP access.
	HTTPPort int

	// SSHPort is the host port for Gitea SSH access.
	SSHPort int
}

// ContainerInfo holds information about a running Gitea container.
type ContainerInfo struct {
	// Name is the Docker container name.
	Name string

	// VolumeName is the Docker volume name.
	VolumeName string

	// HTTPPort is the host port for HTTP access.
	HTTPPort int

	// SSHPort is the host port for SSH access.
	SSHPort int

	// URL is the base URL for API access.
	URL string
}

// ContainerManager handles Gitea container lifecycle.
type ContainerManager struct {
	logger    *logx.Logger
	dockerCmd string
}

// NewContainerManager creates a new Gitea container manager.
func NewContainerManager() *ContainerManager {
	logger := logx.NewLogger("gitea")

	// Auto-detect Docker command (prefer docker, fall back to podman).
	dockerCmd := "docker"
	if _, err := exec.LookPath("podman"); err == nil {
		if _, err := exec.LookPath("docker"); err != nil {
			dockerCmd = "podman"
		}
	}

	return &ContainerManager{
		logger:    logger,
		dockerCmd: dockerCmd,
	}
}

// ContainerName returns the container name for a project.
func ContainerName(projectName string) string {
	return ContainerPrefix + sanitizeName(projectName)
}

// VolumeName returns the volume name for a project.
func VolumeName(projectName string) string {
	return VolumePrefix + sanitizeName(projectName) + VolumeSuffix
}

// sanitizeName converts a project name to a safe Docker name.
// Docker names must match: [a-zA-Z0-9][a-zA-Z0-9_.-]*
func sanitizeName(name string) string {
	// Replace unsafe characters with hyphens.
	var result strings.Builder
	for i, r := range name {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			result.WriteRune(r)
		case r == '_' || r == '.' || r == '-':
			result.WriteRune(r)
		default:
			// First character must be alphanumeric.
			if i > 0 {
				result.WriteRune('-')
			}
		}
	}

	// Ensure non-empty and starts with alphanumeric.
	s := result.String()
	if s == "" {
		return "project"
	}

	// Ensure first character is alphanumeric.
	if !((s[0] >= 'a' && s[0] <= 'z') || (s[0] >= 'A' && s[0] <= 'Z') || (s[0] >= '0' && s[0] <= '9')) {
		s = "p" + s
	}

	return strings.ToLower(s)
}

// EnsureContainer ensures a Gitea container is running for the project.
// This is idempotent - if a container already exists and is healthy, it returns its info.
func (m *ContainerManager) EnsureContainer(ctx context.Context, cfg ContainerConfig) (*ContainerInfo, error) {
	containerName := ContainerName(cfg.ProjectName)
	volumeName := VolumeName(cfg.ProjectName)

	// Set default ports if not specified.
	httpPort := cfg.HTTPPort
	if httpPort == 0 {
		httpPort = DefaultHTTPPort
	}
	sshPort := cfg.SSHPort
	if sshPort == 0 {
		sshPort = DefaultSSHPort
	}

	info := &ContainerInfo{
		Name:       containerName,
		VolumeName: volumeName,
		HTTPPort:   httpPort,
		SSHPort:    sshPort,
		URL:        fmt.Sprintf("http://localhost:%d", httpPort),
	}

	// Check if container already exists and is running.
	if m.isContainerRunning(ctx, containerName) {
		m.logger.Info("Gitea container %s is already running", containerName)

		// Verify it's healthy.
		if IsHealthy(ctx, info.URL) {
			return info, nil
		}

		// Container exists but not healthy - wait for it.
		m.logger.Info("Gitea container %s exists but not healthy, waiting...", containerName)
		if err := WaitForReady(ctx, info.URL, DefaultReadyTimeout); err != nil {
			return nil, fmt.Errorf("existing container not healthy: %w", err)
		}
		return info, nil
	}

	// Check if container exists but is stopped.
	if m.containerExists(ctx, containerName) {
		m.logger.Info("Gitea container %s exists but stopped, starting...", containerName)
		if err := m.startContainer(ctx, containerName); err != nil {
			return nil, fmt.Errorf("failed to start existing container: %w", err)
		}

		if err := WaitForReady(ctx, info.URL, DefaultReadyTimeout); err != nil {
			return nil, fmt.Errorf("container started but not healthy: %w", err)
		}
		return info, nil
	}

	// Create and start new container.
	m.logger.Info("Creating new Gitea container %s", containerName)

	// Ensure volume exists.
	if err := m.ensureVolume(ctx, volumeName); err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	// Create container.
	if err := m.createContainer(ctx, containerName, volumeName, httpPort, sshPort); err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Wait for container to be ready.
	m.logger.Info("Waiting for Gitea to be ready at %s", info.URL)
	if err := WaitForReady(ctx, info.URL, DefaultReadyTimeout); err != nil {
		return nil, fmt.Errorf("container started but not healthy: %w", err)
	}

	m.logger.Info("Gitea container %s is ready", containerName)
	return info, nil
}

// StopContainer gracefully stops a Gitea container.
func (m *ContainerManager) StopContainer(ctx context.Context, containerName string) error {
	m.logger.Info("Stopping Gitea container %s", containerName)

	// Stop the container with a timeout.
	cmd := exec.CommandContext(ctx, m.dockerCmd, "stop", "-t", "10", containerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Check if container doesn't exist (already stopped/removed).
		if strings.Contains(string(output), "No such container") {
			m.logger.Info("Gitea container %s already stopped/removed", containerName)
			return nil
		}
		return fmt.Errorf("failed to stop container: %w (output: %s)", err, string(output))
	}

	m.logger.Info("Gitea container %s stopped", containerName)
	return nil
}

// RemoveContainer removes a Gitea container and optionally its volume.
func (m *ContainerManager) RemoveContainer(ctx context.Context, containerName string, removeVolume bool) error {
	m.logger.Info("Removing Gitea container %s", containerName)

	// Remove container.
	cmd := exec.CommandContext(ctx, m.dockerCmd, "rm", "-f", containerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		// Check if container doesn't exist.
		if !strings.Contains(string(output), "No such container") {
			return fmt.Errorf("failed to remove container: %w (output: %s)", err, string(output))
		}
	}

	// Remove volume if requested.
	if removeVolume {
		// Derive volume name from container name.
		volumeName := strings.TrimPrefix(containerName, ContainerPrefix)
		volumeName = VolumePrefix + volumeName + VolumeSuffix

		m.logger.Info("Removing Gitea volume %s", volumeName)
		cmd = exec.CommandContext(ctx, m.dockerCmd, "volume", "rm", "-f", volumeName)
		if output, err := cmd.CombinedOutput(); err != nil {
			if !strings.Contains(string(output), "No such volume") {
				m.logger.Warn("Failed to remove volume %s: %v", volumeName, err)
			}
		}
	}

	m.logger.Info("Gitea container %s removed", containerName)
	return nil
}

// isContainerRunning checks if a container is currently running.
func (m *ContainerManager) isContainerRunning(ctx context.Context, containerName string) bool {
	cmd := exec.CommandContext(ctx, m.dockerCmd, "inspect", "-f", "{{.State.Running}}", containerName)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(output)) == "true"
}

// containerExists checks if a container exists (running or stopped).
func (m *ContainerManager) containerExists(ctx context.Context, containerName string) bool {
	cmd := exec.CommandContext(ctx, m.dockerCmd, "inspect", containerName)
	return cmd.Run() == nil
}

// startContainer starts an existing stopped container.
func (m *ContainerManager) startContainer(ctx context.Context, containerName string) error {
	cmd := exec.CommandContext(ctx, m.dockerCmd, "start", containerName)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker start failed: %w (output: %s)", err, string(output))
	}
	return nil
}

// ensureVolume creates a Docker volume if it doesn't exist.
func (m *ContainerManager) ensureVolume(ctx context.Context, volumeName string) error {
	// Check if volume exists.
	checkCmd := exec.CommandContext(ctx, m.dockerCmd, "volume", "inspect", volumeName)
	if checkCmd.Run() == nil {
		m.logger.Debug("Volume %s already exists", volumeName)
		return nil
	}

	// Create volume.
	m.logger.Info("Creating volume %s", volumeName)
	createCmd := exec.CommandContext(ctx, m.dockerCmd, "volume", "create", volumeName)
	if output, err := createCmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker volume create failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// createContainer creates and starts a new Gitea container.
func (m *ContainerManager) createContainer(ctx context.Context, containerName, volumeName string, httpPort, sshPort int) error {
	args := []string{
		"run", "-d",
		"--name", containerName,
		"--restart", "unless-stopped",

		// Volume mount for persistent data.
		"-v", fmt.Sprintf("%s:/data", volumeName),

		// Port mappings.
		"-p", fmt.Sprintf("%d:3000", httpPort),
		"-p", fmt.Sprintf("%d:22", sshPort),

		// Environment variables for Gitea configuration.
		"-e", "USER_UID=1000",
		"-e", "USER_GID=1000",
		"-e", fmt.Sprintf("GITEA__server__ROOT_URL=http://localhost:%d/", httpPort),
		"-e", "GITEA__server__HTTP_PORT=3000",
		"-e", "GITEA__server__DISABLE_SSH=false",
		"-e", "GITEA__server__SSH_PORT=22",
		"-e", fmt.Sprintf("GITEA__server__SSH_LISTEN_PORT=%d", sshPort),

		// Skip install page - auto-configure Gitea.
		"-e", "GITEA__security__INSTALL_LOCK=true",

		// Disable features not needed for local development.
		"-e", "GITEA__service__DISABLE_REGISTRATION=false",
		"-e", "GITEA__service__REQUIRE_SIGNIN_VIEW=false",

		// Database - use SQLite for simplicity.
		"-e", "GITEA__database__DB_TYPE=sqlite3",

		// Reduce logging noise.
		"-e", "GITEA__log__LEVEL=Warn",

		// Image.
		GiteaImage,
	}

	m.logger.Debug("Running: %s %s", m.dockerCmd, strings.Join(args, " "))

	cmd := exec.CommandContext(ctx, m.dockerCmd, args...)
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker run failed: %w (output: %s)", err, string(output))
	}

	return nil
}

// IsHealthy checks if a Gitea instance is healthy via its API.
func IsHealthy(ctx context.Context, baseURL string) bool {
	// Use a short timeout for health checks.
	ctx, cancel := context.WithTimeout(ctx, HealthCheckTimeout)
	defer cancel()

	// Gitea provides a health endpoint.
	healthURL := baseURL + "/api/v1/version"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, http.NoBody)
	if err != nil {
		return false
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()

	// Any 2xx response means Gitea is up.
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// WaitForReady waits for a Gitea instance to become healthy.
func WaitForReady(ctx context.Context, baseURL string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for Gitea to be ready at %s: %w", baseURL, ctx.Err())
		case <-ticker.C:
			if IsHealthy(ctx, baseURL) {
				return nil
			}
		}
	}
}

// GetContainerURL returns the API URL for a project's Gitea container.
func GetContainerURL(httpPort int) string {
	return fmt.Sprintf("http://localhost:%d", httpPort)
}
