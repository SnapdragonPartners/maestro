// Package demo provides demo mode functionality for running and testing applications.
package demo

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
)

// NetworkManager handles Docker network lifecycle operations.
type NetworkManager struct {
	// CommandRunner allows injecting a mock for testing.
	// If nil, uses exec.CommandContext.
	CommandRunner func(ctx context.Context, name string, args ...string) *exec.Cmd
}

// NewNetworkManager creates a new network manager.
func NewNetworkManager() *NetworkManager {
	return &NetworkManager{}
}

// runCommand executes a command, using the injected runner or default exec.
//
//nolint:unparam // name parameter allows future flexibility for non-docker commands
func (m *NetworkManager) runCommand(ctx context.Context, name string, args ...string) *exec.Cmd {
	if m.CommandRunner != nil {
		return m.CommandRunner(ctx, name, args...)
	}
	return exec.CommandContext(ctx, name, args...)
}

// EnsureNetwork creates a Docker network if it doesn't exist.
// Returns nil if network already exists or was successfully created.
func (m *NetworkManager) EnsureNetwork(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("network name cannot be empty")
	}

	// Check if network already exists
	exists, err := m.NetworkExists(ctx, name)
	if err != nil {
		return fmt.Errorf("failed to check network existence: %w", err)
	}
	if exists {
		return nil
	}

	// Create the network
	cmd := m.runCommand(ctx, "docker", "network", "create", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if it was created by another process (race condition)
		exists, checkErr := m.NetworkExists(ctx, name)
		if checkErr == nil && exists {
			return nil
		}
		return fmt.Errorf("failed to create network %q: %w, output: %s", name, err, string(output))
	}

	return nil
}

// RemoveNetwork removes a Docker network.
// Returns nil if network doesn't exist or was successfully removed.
func (m *NetworkManager) RemoveNetwork(ctx context.Context, name string) error {
	if name == "" {
		return fmt.Errorf("network name cannot be empty")
	}

	cmd := m.runCommand(ctx, "docker", "network", "rm", name)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if network doesn't exist (not an error)
		if strings.Contains(string(output), "No such network") ||
			strings.Contains(string(output), "not found") {
			return nil
		}
		return fmt.Errorf("failed to remove network %q: %w, output: %s", name, err, string(output))
	}

	return nil
}

// ConnectContainer connects a container to a network.
func (m *NetworkManager) ConnectContainer(ctx context.Context, network, container string) error {
	if network == "" {
		return fmt.Errorf("network name cannot be empty")
	}
	if container == "" {
		return fmt.Errorf("container name cannot be empty")
	}

	// Check if already connected
	connected, err := m.IsConnected(ctx, network, container)
	if err != nil {
		return fmt.Errorf("failed to check connection status: %w", err)
	}
	if connected {
		return nil
	}

	cmd := m.runCommand(ctx, "docker", "network", "connect", network, container)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if already connected (race condition)
		if strings.Contains(string(output), "already exists") ||
			strings.Contains(string(output), "is already connected") {
			return nil
		}
		return fmt.Errorf("failed to connect container %q to network %q: %w, output: %s",
			container, network, err, string(output))
	}

	return nil
}

// DisconnectContainer disconnects a container from a network.
func (m *NetworkManager) DisconnectContainer(ctx context.Context, network, container string) error {
	if network == "" {
		return fmt.Errorf("network name cannot be empty")
	}
	if container == "" {
		return fmt.Errorf("container name cannot be empty")
	}

	cmd := m.runCommand(ctx, "docker", "network", "disconnect", network, container)
	output, err := cmd.CombinedOutput()
	if err != nil {
		// Check if not connected or doesn't exist (not an error)
		if strings.Contains(string(output), "is not connected") ||
			strings.Contains(string(output), "No such container") ||
			strings.Contains(string(output), "not found") {
			return nil
		}
		return fmt.Errorf("failed to disconnect container %q from network %q: %w, output: %s",
			container, network, err, string(output))
	}

	return nil
}

// NetworkExists checks if a Docker network exists.
func (m *NetworkManager) NetworkExists(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, fmt.Errorf("network name cannot be empty")
	}

	cmd := m.runCommand(ctx, "docker", "network", "inspect", name)
	err := cmd.Run()
	if err != nil {
		// Exit code 1 means network doesn't exist
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect network %q: %w", name, err)
	}

	return true, nil
}

// IsConnected checks if a container is connected to a network.
func (m *NetworkManager) IsConnected(ctx context.Context, network, container string) (bool, error) {
	if network == "" {
		return false, fmt.Errorf("network name cannot be empty")
	}
	if container == "" {
		return false, fmt.Errorf("container name cannot be empty")
	}

	// Use docker network inspect with format to check containers
	cmd := m.runCommand(ctx, "docker", "network", "inspect", network,
		"--format", "{{range .Containers}}{{.Name}} {{end}}")
	output, err := cmd.Output()
	if err != nil {
		// Network doesn't exist
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return false, nil
		}
		return false, fmt.Errorf("failed to inspect network %q: %w", network, err)
	}

	// Check if container name is in the output
	containers := strings.Fields(string(output))
	for _, c := range containers {
		if c == container {
			return true, nil
		}
	}

	return false, nil
}

// ListNetworkContainers returns all containers connected to a network.
func (m *NetworkManager) ListNetworkContainers(ctx context.Context, network string) ([]string, error) {
	if network == "" {
		return nil, fmt.Errorf("network name cannot be empty")
	}

	cmd := m.runCommand(ctx, "docker", "network", "inspect", network,
		"--format", "{{range .Containers}}{{.Name}} {{end}}")
	output, err := cmd.Output()
	if err != nil {
		// Network doesn't exist
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return []string{}, nil
		}
		return nil, fmt.Errorf("failed to inspect network %q: %w", network, err)
	}

	containers := strings.Fields(string(output))
	return containers, nil
}
