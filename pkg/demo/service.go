package demo

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"orchestrator/internal/state"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

const (
	// DemoNetworkName is the name of the demo network.
	DemoNetworkName = "demo-network"
	// DemoProjectName is the compose project name for demo.
	DemoProjectName = "demo"
	// DemoContainerName is the name of the demo container when running without compose.
	DemoContainerName = "maestro-demo"
	// DefaultDemoPort is the default port for the demo app.
	DefaultDemoPort = 8081
	// containerStatusRunning is the Docker status for running containers.
	containerStatusRunning = "running"
)

// Status represents the current state of the demo.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type Status struct {
	Running      bool            `json:"running"`
	Port         int             `json:"port,omitempty"`
	URL          string          `json:"url,omitempty"`
	BuiltFromSHA string          `json:"built_from_sha,omitempty"`
	CurrentSHA   string          `json:"current_sha,omitempty"`
	Outdated     bool            `json:"outdated"`
	Services     []ServiceStatus `json:"services,omitempty"`
	StartedAt    *time.Time      `json:"started_at,omitempty"`
	Error        string          `json:"error,omitempty"`

	// Port detection info
	ContainerPort    int               `json:"container_port,omitempty"`    // Container port being mapped
	DetectedPorts    []config.PortInfo `json:"detected_ports,omitempty"`    // All detected listening ports
	UnreachablePorts []config.PortInfo `json:"unreachable_ports,omitempty"` // Ports bound to loopback
	DiagnosticError  string            `json:"diagnostic_error,omitempty"`  // Port detection error message
	DiagnosticType   string            `json:"diagnostic_type,omitempty"`   // Error type for UI rendering
}

// ServiceStatus represents the status of a compose service.
type ServiceStatus struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Healthy bool   `json:"healthy"`
}

// Service manages the demo lifecycle.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type Service struct {
	mu sync.RWMutex

	config          *config.Config
	logger          *logx.Logger
	networkManager  *NetworkManager
	composeRegistry *state.ComposeRegistry

	// State
	running        bool
	port           int
	builtFromSHA   string
	startedAt      time.Time
	workspacePath  string            // Path to the workspace with compose file
	projectDir     string            // Project directory for config saving
	useCompose     bool              // Whether demo is using compose or container-only mode
	containerID    string            // Container ID when running without compose
	lastDiagnostic *DiagnosticResult // Last port detection diagnostic

	// For testing
	commandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewService creates a new demo service.
func NewService(
	cfg *config.Config,
	logger *logx.Logger,
	composeRegistry *state.ComposeRegistry,
) *Service {
	return &Service{
		config:          cfg,
		logger:          logger,
		networkManager:  NewNetworkManager(),
		composeRegistry: composeRegistry,
		port:            DefaultDemoPort,
	}
}

// SetWorkspacePath sets the workspace path for the demo.
// This should be called before starting the demo.
// The path is converted to absolute to ensure Docker volume mounts work correctly.
func (s *Service) SetWorkspacePath(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Convert to absolute path - Docker requires absolute paths for volume mounts
	absPath, err := filepath.Abs(path)
	if err != nil {
		s.logger.Warn("Failed to get absolute path for %q, using as-is: %v", path, err)
		s.workspacePath = path
		return
	}
	s.workspacePath = absPath
}

// SetProjectDir sets the project directory for config persistence.
func (s *Service) SetProjectDir(dir string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.projectDir = dir
}

// saveDetectedPorts saves the detected port information to config.
func (s *Service) saveDetectedPorts(ports []config.PortInfo, selectedPort, hostPort int) {
	if s.config.Demo == nil {
		s.config.Demo = &config.DemoConfig{}
	}

	s.config.Demo.DetectedPorts = ports
	s.config.Demo.SelectedContainerPort = selectedPort
	s.config.Demo.LastAssignedHostPort = hostPort

	// Save to disk if project dir is set
	if s.projectDir != "" {
		if err := config.SaveConfig(s.config, s.projectDir); err != nil {
			s.logger.Warn("⚠️ Failed to save detected ports to config: %v", err)
		} else {
			s.logger.Info("   Saved detected port %d to config", selectedPort)
		}
	}
}

// Start starts the demo.
func (s *Service) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		return fmt.Errorf("demo is already running")
	}

	if s.workspacePath == "" {
		return fmt.Errorf("workspace path not set")
	}

	s.logger.Info("🚀 Starting demo...")

	// Always create demo network
	if err := s.networkManager.EnsureNetwork(ctx, DemoNetworkName); err != nil {
		return fmt.Errorf("failed to create demo network: %w", err)
	}

	// Check if compose file exists
	composePath := ComposeFilePath(s.workspacePath)
	if ComposeFileExists(s.workspacePath) {
		// Start with compose
		if err := s.startWithCompose(ctx, composePath); err != nil {
			return err
		}
	} else {
		// Start container only (no services)
		if err := s.startContainerOnly(ctx); err != nil {
			return err
		}
	}

	// Get current git SHA
	sha, err := s.getCurrentSHA(ctx)
	if err != nil {
		s.logger.Warn("⚠️ Could not get current SHA: %v", err)
	}

	s.running = true
	s.builtFromSHA = sha
	s.startedAt = time.Now()

	s.logger.Info("✅ Demo started on port %d", s.port)
	return nil
}

// startWithCompose starts the demo using docker compose.
func (s *Service) startWithCompose(ctx context.Context, composePath string) error {
	stack := NewStack(DemoProjectName, composePath, DemoNetworkName)
	if s.commandRunner != nil {
		stack.CommandRunner = s.commandRunner
	}

	if err := stack.Up(ctx); err != nil {
		return fmt.Errorf("failed to start compose stack: %w", err)
	}

	// Register stack for cleanup
	s.composeRegistry.Register(&state.ComposeStack{
		ProjectName: DemoProjectName,
		ComposeFile: composePath,
		Network:     DemoNetworkName,
		StartedAt:   time.Now(),
	})

	s.useCompose = true
	return nil
}

// startContainerOnly starts a single container without compose.
// This runs the app directly in the dev container using build + run commands.
// Uses discovery mode to detect listening ports and restart with proper port mapping.
func (s *Service) startContainerOnly(ctx context.Context) error {
	// Get the image to use (pinned or safe fallback)
	imageID := s.getImageID()
	if imageID == "" {
		return fmt.Errorf("no container image available - bootstrap must complete first")
	}

	// Get build and run commands from config
	buildCmd := "make build"
	runCmd := "make run"
	if s.config.Build != nil {
		if s.config.Build.Build != "" {
			buildCmd = s.config.Build.Build
		}
		if s.config.Build.Run != "" {
			runCmd = s.config.Build.Run
		}
	}

	// Check for demo run command override
	if s.config.Demo != nil && s.config.Demo.RunCmdOverride != "" {
		runCmd = s.config.Demo.RunCmdOverride
	}

	s.logger.Info("🐳 Starting demo container (no compose)")
	s.logger.Info("   Image: %s", imageID)
	s.logger.Info("   Build: %s", buildCmd)
	s.logger.Info("   Run: %s", runCmd)

	// Check if we have a cached port from previous detection
	cachedPort := 0
	if s.config.Demo != nil && s.config.Demo.SelectedContainerPort > 0 {
		cachedPort = s.config.Demo.SelectedContainerPort
		s.logger.Info("   Using cached port: %d", cachedPort)
	}

	// Start the container
	if err := s.runContainer(ctx, imageID, buildCmd, runCmd, cachedPort); err != nil {
		return err
	}

	// If we used cached port, verify it's working with TCP probe
	if cachedPort > 0 {
		if err := s.verifyPortWithProbe(ctx); err != nil {
			s.logger.Info("   Cached port failed verification, running discovery...")
			// Fall back to discovery mode
			s.removeExistingContainer(ctx)
			if err := s.runContainer(ctx, imageID, buildCmd, runCmd, 0); err != nil {
				return err
			}
		} else {
			return nil // Cached port works
		}
	}

	// Discovery mode: wait for listeners
	s.logger.Info("   Running port discovery...")
	diagnostic := s.runPortDiscovery(ctx, imageID, buildCmd, runCmd)

	// Store diagnostic for status reporting
	s.lastDiagnostic = diagnostic

	if !diagnostic.Success {
		return fmt.Errorf("demo port detection: %s", diagnostic.Error)
	}

	s.port = diagnostic.HostPort
	s.logger.Info("   ✅ Port detected: container:%d → host:%d", diagnostic.ContainerPort, diagnostic.HostPort)

	return nil
}

// runContainer starts the demo container with optional port mapping.
// If containerPort is 0, starts without port mapping (discovery mode).
func (s *Service) runContainer(ctx context.Context, imageID, buildCmd, runCmd string, containerPort int) error {
	// Remove any existing demo container
	s.removeExistingContainer(ctx)

	// Build the command
	combinedCmd := fmt.Sprintf("%s && %s", buildCmd, runCmd)

	args := []string{
		"run", "-d",
		"--name", DemoContainerName,
		"--network", DemoNetworkName,
		"--workdir", "/workspace",
		"--volume", fmt.Sprintf("%s:/workspace", s.workspacePath),
		"--cpus", "2",
		"--memory", "2g",
	}

	// Add port mapping if specified
	if containerPort > 0 {
		// Map to localhost with Docker-assigned host port
		args = append(args, "-p", fmt.Sprintf("127.0.0.1::%d", containerPort))
	}

	args = append(args, imageID, "sh", "-c", combinedCmd)

	var cmd *exec.Cmd
	if s.commandRunner != nil {
		cmd = s.commandRunner(ctx, "docker", args...)
	} else {
		cmd = exec.CommandContext(ctx, "docker", args...)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("failed to start demo container: %w (output: %s)", err, string(output))
	}

	// Store container ID
	s.containerID = strings.TrimSpace(string(output))
	s.useCompose = false

	if len(s.containerID) >= 12 {
		s.logger.Info("   Container ID: %s", s.containerID[:12])
	}

	// If we mapped a port, get the assigned host port
	if containerPort > 0 {
		if err := s.updatePublishedPort(ctx); err != nil {
			s.logger.Warn("⚠️ Could not determine published port: %v", err)
		}
	}

	return nil
}

// runPortDiscovery detects listening ports and restarts with proper mapping.
func (s *Service) runPortDiscovery(ctx context.Context, imageID, buildCmd, runCmd string) *DiagnosticResult {
	// Create port detector for the container
	portDetector := NewPortDetector(DemoContainerName)
	if s.commandRunner != nil {
		portDetector.SetCommandRunner(s.commandRunner)
	}

	// Wait for listeners (30 second timeout, poll every second)
	ports, err := portDetector.WaitForListeners(ctx, 30*time.Second, 1*time.Second)
	if err != nil {
		// Check if container crashed
		containerStatus := s.getContainerStatus(ctx)
		if containerStatus.Status != containerStatusRunning {
			logs, _ := s.GetLogs(ctx)
			return &DiagnosticResult{
				ErrorType: DiagnosticContainerExited,
				Error:     fmt.Sprintf("Container exited (%s). Last logs:\n%s", containerStatus.Status, truncateLogs(logs, 10)),
			}
		}
		return &DiagnosticResult{
			ErrorType: DiagnosticNoListeners,
			Error:     "No TCP listeners detected after 30 seconds",
		}
	}

	// Get exposed ports from image (for priority selection)
	exposedPorts, _ := GetExposedPorts(ctx, imageID)

	// Select the main port
	selectedPort := SelectMainPort(s.config.Demo, ports, exposedPorts)

	// Build initial diagnostic
	diagnostic := BuildDiagnostic(ports, selectedPort, 0, nil)

	if selectedPort == 0 {
		return &diagnostic
	}

	// Stop current container and restart with port mapping
	s.logger.Info("   Detected port %d, restarting with port mapping...", selectedPort)
	s.removeExistingContainer(ctx)

	if err := s.runContainer(ctx, imageID, buildCmd, runCmd, selectedPort); err != nil {
		diagnostic.ErrorType = DiagnosticProbeFailure
		diagnostic.Error = fmt.Sprintf("Failed to restart with port mapping: %v", err)
		return &diagnostic
	}

	// Wait for container to start and app to come up
	time.Sleep(2 * time.Second)

	// Get the host port
	hostPort := s.port // Updated by runContainer -> updatePublishedPort

	// TCP probe to verify
	addr := fmt.Sprintf("127.0.0.1:%d", hostPort)
	probeErr := TCPProbe(ctx, addr, 5*time.Second)

	diagnostic = BuildDiagnostic(ports, selectedPort, hostPort, probeErr)

	// Save detected ports if successful
	if diagnostic.Success {
		s.saveDetectedPorts(ports, selectedPort, hostPort)
	}

	return &diagnostic
}

// verifyPortWithProbe checks if the current port is working.
func (s *Service) verifyPortWithProbe(ctx context.Context) error {
	// Wait a bit for the app to start
	time.Sleep(2 * time.Second)

	// Get the actual host port
	if err := s.updatePublishedPort(ctx); err != nil {
		return err
	}

	// TCP probe
	addr := fmt.Sprintf("127.0.0.1:%d", s.port)
	return TCPProbe(ctx, addr, 5*time.Second)
}

// truncateLogs returns the last n lines of logs.
func truncateLogs(logs string, n int) string {
	lines := strings.Split(logs, "\n")
	if len(lines) <= n {
		return logs
	}
	return strings.Join(lines[len(lines)-n:], "\n")
}

// getImageID returns the container image to use for demo.
// Prefers pinned image, falls back to safe image.
// Uses live config (not snapshot) to get current pinned image after rebuilds.
func (s *Service) getImageID() string {
	// Use live config to get current pinned image ID
	// The pinned image can change at runtime when coder finishes a build
	cfg, err := config.GetConfig()
	if err != nil || cfg.Container == nil {
		// Fall back to snapshot if live config unavailable
		if s.config.Container == nil {
			return ""
		}
		if s.config.Container.PinnedImageID != "" {
			return s.config.Container.PinnedImageID
		}
		return s.config.Container.SafeImageID
	}
	if cfg.Container.PinnedImageID != "" {
		return cfg.Container.PinnedImageID
	}
	return cfg.Container.SafeImageID
}

// removeExistingContainer removes any existing demo container.
func (s *Service) removeExistingContainer(ctx context.Context) {
	var cmd *exec.Cmd
	if s.commandRunner != nil {
		cmd = s.commandRunner(ctx, "docker", "rm", "-f", DemoContainerName)
	} else {
		cmd = exec.CommandContext(ctx, "docker", "rm", "-f", DemoContainerName)
	}
	// Ignore error - container may not exist
	_ = cmd.Run()
}

// updatePublishedPort queries Docker to find the actual published port.
func (s *Service) updatePublishedPort(ctx context.Context) error {
	// Use docker port to find the published port
	var cmd *exec.Cmd
	if s.commandRunner != nil {
		cmd = s.commandRunner(ctx, "docker", "port", DemoContainerName)
	} else {
		cmd = exec.CommandContext(ctx, "docker", "port", DemoContainerName)
	}

	output, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker port failed: %w", err)
	}

	// Parse output like "8080/tcp -> 0.0.0.0:32768"
	// Take the first port mapping we find
	lines := strings.Split(string(output), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Parse "PORT/PROTO -> HOST:HOSTPORT"
		parts := strings.Split(line, "->")
		if len(parts) != 2 {
			continue
		}
		hostPart := strings.TrimSpace(parts[1])
		// Extract port from "0.0.0.0:32768" or "[::]:32768"
		colonIdx := strings.LastIndex(hostPart, ":")
		if colonIdx == -1 {
			continue
		}
		portStr := hostPart[colonIdx+1:]
		var port int
		if _, err := fmt.Sscanf(portStr, "%d", &port); err == nil {
			s.port = port
			s.logger.Info("   Published port: %d", port)
			return nil
		}
	}

	// No port found - use default
	s.port = DefaultDemoPort
	return nil
}

// Stop stops the demo.
func (s *Service) Stop(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return nil // Already stopped
	}

	s.logger.Info("🛑 Stopping demo...")

	if s.useCompose {
		// Stop compose stack
		composePath := ComposeFilePath(s.workspacePath)
		stack := NewStack(DemoProjectName, composePath, DemoNetworkName)
		if s.commandRunner != nil {
			stack.CommandRunner = s.commandRunner
		}

		if err := stack.Down(ctx); err != nil {
			s.logger.Warn("⚠️ Error stopping compose stack: %v", err)
		}

		// Unregister from compose registry
		s.composeRegistry.Unregister(DemoProjectName)
	} else if s.containerID != "" {
		// Stop container-only demo
		if err := s.stopContainer(ctx); err != nil {
			s.logger.Warn("⚠️ Error stopping demo container: %v", err)
		}
		s.containerID = ""
	}

	// Remove network
	if err := s.networkManager.RemoveNetwork(ctx, DemoNetworkName); err != nil {
		s.logger.Warn("⚠️ Error removing demo network: %v", err)
	}

	s.running = false
	s.useCompose = false
	s.logger.Info("✅ Demo stopped")
	return nil
}

// stopContainer stops and removes the demo container.
func (s *Service) stopContainer(ctx context.Context) error {
	var cmd *exec.Cmd
	if s.commandRunner != nil {
		cmd = s.commandRunner(ctx, "docker", "rm", "-f", DemoContainerName)
	} else {
		cmd = exec.CommandContext(ctx, "docker", "rm", "-f", DemoContainerName)
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}
	return nil
}

// Restart restarts the demo container without rebuilding.
func (s *Service) Restart(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.running {
		return fmt.Errorf("demo is not running")
	}

	s.logger.Info("🔄 Restarting demo...")

	if s.useCompose {
		composePath := ComposeFilePath(s.workspacePath)
		stack := NewStack(DemoProjectName, composePath, DemoNetworkName)
		if s.commandRunner != nil {
			stack.CommandRunner = s.commandRunner
		}

		// Restart only the main demo service (not databases)
		if err := stack.Restart(ctx, "demo"); err != nil {
			return fmt.Errorf("failed to restart demo: %w", err)
		}
	} else {
		// For container-only mode, restart the container
		var cmd *exec.Cmd
		if s.commandRunner != nil {
			cmd = s.commandRunner(ctx, "docker", "restart", DemoContainerName)
		} else {
			cmd = exec.CommandContext(ctx, "docker", "restart", DemoContainerName)
		}
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to restart demo container: %w", err)
		}
	}

	s.logger.Info("✅ Demo restarted")
	return nil
}

// RebuildOptions configures rebuild behavior.
type RebuildOptions struct {
	// SkipDetection uses cached port instead of re-running discovery.
	// Default (false) re-runs discovery since code changes may affect ports.
	SkipDetection bool
}

// Rebuild rebuilds and restarts the entire demo stack.
// By default, re-runs port discovery. Use RebuildOptions.SkipDetection
// to use the cached port from previous detection.
func (s *Service) Rebuild(ctx context.Context) error {
	return s.RebuildWithOptions(ctx, RebuildOptions{})
}

// RebuildWithOptions rebuilds with configurable options.
func (s *Service) RebuildWithOptions(ctx context.Context, opts RebuildOptions) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("🔨 Rebuilding demo...")

	// If not skipping detection, clear cached port to force re-discovery
	if !opts.SkipDetection {
		if s.config.Demo != nil {
			s.config.Demo.SelectedContainerPort = 0
			s.config.Demo.DetectedPorts = nil
			s.logger.Info("   Clearing cached port for re-discovery")
		}
	} else {
		s.logger.Info("   Skipping port detection (using cached port)")
	}

	// Stop if running
	if s.running {
		s.mu.Unlock()
		if err := s.Stop(ctx); err != nil {
			s.mu.Lock()
			return fmt.Errorf("failed to stop for rebuild: %w", err)
		}
		s.mu.Lock()
	}

	// Rebuild by starting again
	s.mu.Unlock()
	err := s.Start(ctx)
	s.mu.Lock()

	if err != nil {
		return fmt.Errorf("failed to start after rebuild: %w", err)
	}

	s.logger.Info("✅ Demo rebuilt")
	return nil
}

// Status returns the current demo status.
func (s *Service) Status(ctx context.Context) *Status {
	s.mu.RLock()
	defer s.mu.RUnlock()

	status := &Status{
		Running:      s.running,
		Port:         s.port,
		BuiltFromSHA: s.builtFromSHA,
	}

	// Include port detection info from config
	if s.config.Demo != nil {
		status.ContainerPort = s.config.Demo.SelectedContainerPort
		status.DetectedPorts = s.config.Demo.DetectedPorts
	}

	// Include last diagnostic
	if s.lastDiagnostic != nil {
		status.DiagnosticError = s.lastDiagnostic.Error
		status.DiagnosticType = string(s.lastDiagnostic.ErrorType)
		status.UnreachablePorts = s.lastDiagnostic.UnreachablePorts
	}

	if s.running {
		status.URL = fmt.Sprintf("http://localhost:%d", s.port)
		startedAt := s.startedAt
		status.StartedAt = &startedAt

		// Get current SHA to check if outdated
		currentSHA, err := s.getCurrentSHA(ctx)
		if err == nil {
			status.CurrentSHA = currentSHA
			status.Outdated = s.builtFromSHA != "" && currentSHA != "" && s.builtFromSHA != currentSHA
		}

		// Get service statuses
		if s.useCompose {
			composePath := ComposeFilePath(s.workspacePath)
			stack := NewStack(DemoProjectName, composePath, DemoNetworkName)
			if s.commandRunner != nil {
				stack.CommandRunner = s.commandRunner
			}

			services, err := stack.PS(ctx)
			if err == nil {
				for i := range services {
					status.Services = append(status.Services, ServiceStatus{
						Name:    services[i].Name,
						Status:  services[i].Status,
						Healthy: services[i].Health == "healthy",
					})
				}
			}
		} else {
			// Container-only mode: show the main container
			containerStatus := s.getContainerStatus(ctx)
			status.Services = []ServiceStatus{containerStatus}
		}
	}

	return status
}

// getContainerStatus returns the status of the demo container.
func (s *Service) getContainerStatus(ctx context.Context) ServiceStatus {
	status := ServiceStatus{
		Name:    "demo",
		Status:  "unknown",
		Healthy: false,
	}

	var cmd *exec.Cmd
	if s.commandRunner != nil {
		cmd = s.commandRunner(ctx, "docker", "inspect", "--format", "{{.State.Status}}", DemoContainerName)
	} else {
		cmd = exec.CommandContext(ctx, "docker", "inspect", "--format", "{{.State.Status}}", DemoContainerName)
	}

	output, err := cmd.Output()
	if err == nil {
		status.Status = strings.TrimSpace(string(output))
		status.Healthy = status.Status == containerStatusRunning
	}

	return status
}

// IsRunning returns whether the demo is currently running.
func (s *Service) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetLogs returns recent logs from the demo.
func (s *Service) GetLogs(ctx context.Context) (string, error) {
	s.mu.RLock()
	workspacePath := s.workspacePath
	commandRunner := s.commandRunner
	useCompose := s.useCompose
	s.mu.RUnlock()

	if useCompose {
		composePath := ComposeFilePath(workspacePath)
		stack := NewStack(DemoProjectName, composePath, DemoNetworkName)
		if commandRunner != nil {
			stack.CommandRunner = commandRunner
		}

		reader, err := stack.Logs(ctx, "")
		if err != nil {
			return "", fmt.Errorf("failed to get logs: %w", err)
		}

		buf := make([]byte, 64*1024) // 64KB buffer
		n, _ := reader.Read(buf)
		return string(buf[:n]), nil
	}

	// Container-only mode: use docker logs
	var cmd *exec.Cmd
	if commandRunner != nil {
		cmd = commandRunner(ctx, "docker", "logs", "--tail", "100", DemoContainerName)
	} else {
		cmd = exec.CommandContext(ctx, "docker", "logs", "--tail", "100", DemoContainerName)
	}

	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get logs: %w", err)
	}

	return string(output), nil
}

// getCurrentSHA returns the current git HEAD SHA.
func (s *Service) getCurrentSHA(ctx context.Context) (string, error) {
	if s.workspacePath == "" {
		return "", fmt.Errorf("workspace path not set")
	}

	var cmd *exec.Cmd
	if s.commandRunner != nil {
		cmd = s.commandRunner(ctx, "git", "-C", s.workspacePath, "rev-parse", "HEAD")
	} else {
		cmd = exec.CommandContext(ctx, "git", "-C", s.workspacePath, "rev-parse", "HEAD")
	}

	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse failed: %w", err)
	}

	return string(output[:len(output)-1]), nil // Remove trailing newline
}

// Cleanup stops all demo resources. Called during shutdown.
func (s *Service) Cleanup(ctx context.Context) error {
	return s.Stop(ctx)
}

// ConnectPM connects the PM container to the demo network.
// This allows PM to probe the demo services.
func (s *Service) ConnectPM(ctx context.Context, pmContainerName string) error {
	s.mu.RLock()
	running := s.running
	s.mu.RUnlock()

	if !running {
		return fmt.Errorf("demo is not running")
	}

	return s.networkManager.ConnectContainer(ctx, DemoNetworkName, pmContainerName)
}

// DisconnectPM disconnects the PM container from the demo network.
func (s *Service) DisconnectPM(ctx context.Context, pmContainerName string) error {
	return s.networkManager.DisconnectContainer(ctx, DemoNetworkName, pmContainerName)
}
