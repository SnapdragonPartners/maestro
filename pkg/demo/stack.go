package demo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Stack represents a Docker Compose stack.
//
//nolint:govet // fieldalignment: Logical grouping preferred for readability
type Stack struct {
	ProjectName string // e.g., "coder-001", "demo"
	ComposeFile string // Path to compose file
	Network     string // Network name

	// CommandRunner allows injecting a mock for testing.
	// If nil, uses exec.CommandContext.
	CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewStack creates a new stack manager.
func NewStack(projectName, composeFile, network string) *Stack {
	return &Stack{
		ProjectName: projectName,
		ComposeFile: composeFile,
		Network:     network,
	}
}

// runCommand executes a command, using the injected runner or default exec.
//
//nolint:unparam // name parameter allows future flexibility for non-docker commands
func (s *Stack) runCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if s.CommandRunner != nil {
		return s.CommandRunner(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

// baseArgs returns the base docker compose arguments.
func (s *Stack) baseArgs() []string {
	args := []string{"compose", "-p", s.ProjectName}
	if s.ComposeFile != "" {
		args = append(args, "-f", s.ComposeFile)
	}
	return args
}

// Up starts the compose stack.
// This is idempotent - compose handles diffing internally and only recreates changed services.
// Before starting, it creates a sanitized copy of the compose file with container_name removed
// to prevent collisions between different compose projects using the same file.
func (s *Stack) Up(ctx context.Context) error {
	if s.ProjectName == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	// Create sanitized compose file (strips container_name to prevent collisions)
	composeFile := s.ComposeFile
	if s.ComposeFile != "" {
		sanitized, sanitizeErr := s.sanitizedComposeFile()
		if sanitizeErr == nil && sanitized != "" {
			composeFile = sanitized
			defer func() { _ = os.Remove(sanitized) }() // Clean up temp file after compose up
		}
		// If sanitization fails, fall back to original file
	}

	args := []string{"compose", "-p", s.ProjectName}
	if composeFile != "" {
		args = append(args, "-f", composeFile)
	}
	args = append(args, "up", "-d", "--wait", "--remove-orphans")

	cmd := s.runCommand(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose up failed: %w, output: %s", err, string(output))
	}

	return nil
}

// Down stops and removes the compose stack.
// The -v flag removes volumes to ensure clean state.
// The --remove-orphans flag removes containers for services not defined in the compose file.
func (s *Stack) Down(ctx context.Context) error {
	if s.ProjectName == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	args := s.baseArgs()
	args = append(args, "down", "-v", "--remove-orphans", "--timeout", "30")

	cmd := s.runCommand(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if stack doesn't exist (not an error)
		if strings.Contains(string(output), "no configuration file") ||
			strings.Contains(string(output), "No such") {
			return nil
		}
		return fmt.Errorf("docker compose down failed: %w, output: %s", err, string(output))
	}

	return nil
}

// Restart restarts a specific service or all services.
// If service is empty, restarts all services.
func (s *Stack) Restart(ctx context.Context, service string) error {
	if s.ProjectName == "" {
		return fmt.Errorf("project name cannot be empty")
	}

	args := s.baseArgs()
	args = append(args, "restart")
	if service != "" {
		args = append(args, service)
	}

	cmd := s.runCommand(ctx, "docker", args...)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker compose restart failed: %w, output: %s", err, string(output))
	}

	return nil
}

// Logs returns a reader for service logs.
// If service is empty, returns logs for all services.
func (s *Stack) Logs(ctx context.Context, service string) (io.Reader, error) {
	if s.ProjectName == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	args := s.baseArgs()
	args = append(args, "logs", "--no-color", "--tail", "100")
	if service != "" {
		args = append(args, service)
	}

	cmd := s.runCommand(ctx, "docker", args...)
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker compose logs failed: %w", err)
	}

	return bytes.NewReader(output), nil
}

// PS returns the status of services in the stack.
func (s *Stack) PS(ctx context.Context) ([]ServiceInfo, error) {
	if s.ProjectName == "" {
		return nil, fmt.Errorf("project name cannot be empty")
	}

	args := s.baseArgs()
	args = append(args, "ps", "--format", "json")

	cmd := s.runCommand(ctx, "docker", args...)
	output, err := cmd.Output()
	if err != nil {
		// No services running is not an error
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []ServiceInfo{}, nil
		}
		return nil, fmt.Errorf("docker compose ps failed: %w", err)
	}

	// Parse JSON output - each line is a separate JSON object
	lines := strings.Split(string(output), "\n")
	services := make([]ServiceInfo, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var info composeServiceJSON
		if err := json.Unmarshal([]byte(line), &info); err != nil {
			continue // Skip malformed lines
		}

		services = append(services, ServiceInfo{
			Name:    info.Service,
			Status:  info.State,
			Health:  info.Health,
			Ports:   info.Publishers,
			Running: info.State == "running",
		})
	}

	return services, nil
}

// ServiceInfo contains information about a compose service.
type ServiceInfo struct {
	Name    string     `json:"name"`
	Status  string     `json:"status"`
	Health  string     `json:"health"`
	Ports   []PortInfo `json:"ports"`
	Running bool       `json:"running"`
}

// PortInfo contains port mapping information.
//
//nolint:govet // fieldalignment: Matches Docker API response structure
type PortInfo struct {
	URL           string `json:"URL,omitempty"`
	TargetPort    int    `json:"TargetPort"`
	PublishedPort int    `json:"PublishedPort"`
	Protocol      string `json:"Protocol"`
}

// composeServiceJSON matches docker compose ps --format json output.
type composeServiceJSON struct {
	Service    string     `json:"Service"`
	State      string     `json:"State"`
	Health     string     `json:"Health"`
	Publishers []PortInfo `json:"Publishers"`
}

// IsRunning checks if the stack has any running services.
func (s *Stack) IsRunning(ctx context.Context) (bool, error) {
	services, err := s.PS(ctx)
	if err != nil {
		return false, err
	}

	for i := range services {
		if services[i].Running {
			return true, nil
		}
	}
	return false, nil
}

// Exists checks if the compose file exists.
func (s *Stack) Exists() bool {
	if s.ComposeFile == "" {
		return false
	}
	_, err := os.Stat(s.ComposeFile)
	return err == nil
}

// ListServices returns the service names defined in the compose file.
func (s *Stack) ListServices() ([]string, error) {
	if s.ComposeFile == "" {
		return nil, fmt.Errorf("compose file not specified")
	}

	data, err := os.ReadFile(s.ComposeFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read compose file: %w", err)
	}

	var compose composeFile
	if err := yaml.Unmarshal(data, &compose); err != nil {
		return nil, fmt.Errorf("failed to parse compose file: %w", err)
	}

	services := make([]string, 0, len(compose.Services))
	for name := range compose.Services {
		services = append(services, name)
	}
	return services, nil
}

// CountServices returns the number of services defined in the compose file.
func (s *Stack) CountServices() (int, error) {
	services, err := s.ListServices()
	if err != nil {
		return 0, err
	}
	return len(services), nil
}

// composeFile represents the structure of a docker-compose.yml.
type composeFile struct {
	Services map[string]interface{} `yaml:"services"`
}

// sanitizedComposeFile creates a sanitized copy of the compose file with container_name removed.
// Returns the path to the temp file, or empty string if no sanitization was needed.
// The caller is responsible for removing the temp file when done.
func (s *Stack) sanitizedComposeFile() (string, error) {
	if s.ComposeFile == "" {
		return "", nil
	}

	data, err := os.ReadFile(s.ComposeFile)
	if err != nil {
		return "", fmt.Errorf("failed to read compose file: %w", err)
	}

	// Parse as generic map to preserve structure
	var compose map[string]any
	if unmarshalErr := yaml.Unmarshal(data, &compose); unmarshalErr != nil {
		return "", fmt.Errorf("failed to parse compose file: %w", unmarshalErr)
	}

	services, ok := compose["services"].(map[string]any)
	if !ok {
		return "", nil // No services to modify
	}

	// Strip container_name from all services
	modified := false
	for _, svcRaw := range services {
		svc, ok := svcRaw.(map[string]any)
		if !ok {
			continue
		}

		if _, exists := svc["container_name"]; exists {
			delete(svc, "container_name")
			modified = true
		}
	}

	if !modified {
		return "", nil // No changes needed, use original file
	}

	// Write sanitized version to temp file
	newData, err := yaml.Marshal(compose)
	if err != nil {
		return "", fmt.Errorf("failed to marshal compose file: %w", err)
	}

	tmpFile := filepath.Join(os.TempDir(),
		fmt.Sprintf("%s-sanitized-%d.yml", s.ProjectName, time.Now().UnixNano()))

	if err := os.WriteFile(tmpFile, newData, 0600); err != nil {
		return "", fmt.Errorf("failed to write sanitized compose file: %w", err)
	}

	return tmpFile, nil
}

// ComposeFileExists checks if a compose file exists at the given workspace path.
func ComposeFileExists(workspacePath string) bool {
	composePath := filepath.Join(workspacePath, ".maestro", "compose.yml")
	_, err := os.Stat(composePath)
	return err == nil
}

// ComposeFilePath returns the path to the compose file in a workspace.
func ComposeFilePath(workspacePath string) string {
	return filepath.Join(workspacePath, ".maestro", "compose.yml")
}
