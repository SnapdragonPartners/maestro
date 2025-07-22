package exec

import (
	"context"
	"sync"
	"time"

	"orchestrator/pkg/logx"
	"orchestrator/pkg/utils"
)

// Global container registry instance - initialized by orchestrator at startup.
var globalContainerRegistry *ContainerRegistry //nolint:gochecknoglobals
var globalContainerRegistryMu sync.RWMutex     //nolint:gochecknoglobals

// SetGlobalRegistry initializes the global container registry (called by orchestrator).
func SetGlobalRegistry(registry *ContainerRegistry) {
	globalContainerRegistryMu.Lock()
	defer globalContainerRegistryMu.Unlock()
	globalContainerRegistry = registry
}

// GetGlobalRegistry returns the global container registry.
func GetGlobalRegistry() *ContainerRegistry {
	globalContainerRegistryMu.RLock()
	defer globalContainerRegistryMu.RUnlock()
	return globalContainerRegistry
}

// RegistryContainerInfo holds information about a registered container.
type RegistryContainerInfo struct {
	StartTime     time.Time // When container was started
	LastUsed      time.Time // Last activity timestamp
	AgentID       string    // Agent that owns this container
	ContainerName string    // Docker container name
	Purpose       string    // "planning", "coding", "testing", etc.
}

// ContainerRegistry manages all active containers for graceful shutdown and resource cleanup.
type ContainerRegistry struct {
	containers map[string]*RegistryContainerInfo // containerName -> info
	logger     *logx.Logger
	shutdown   chan struct{}
	done       chan struct{}
	mu         sync.RWMutex
}

// NewContainerRegistry creates a new container registry.
func NewContainerRegistry(logger *logx.Logger) *ContainerRegistry {
	return &ContainerRegistry{
		containers: make(map[string]*RegistryContainerInfo),
		logger:     logger,
		shutdown:   make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Register adds a container to the registry for tracking.
func (r *ContainerRegistry) Register(agentID, containerName, purpose string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Sanitize the agent ID for consistent tracking.
	sanitizedAgentID := utils.SanitizeIdentifier(agentID)

	info := &RegistryContainerInfo{
		AgentID:       sanitizedAgentID,
		ContainerName: containerName,
		Purpose:       purpose,
		StartTime:     time.Now(),
		LastUsed:      time.Now(),
	}

	r.containers[containerName] = info
	r.logger.Info("📦 Container registered: %s (agent: %s, purpose: %s)", containerName, sanitizedAgentID, purpose)
}

// Unregister removes a container from the registry.
func (r *ContainerRegistry) Unregister(containerName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if info, exists := r.containers[containerName]; exists {
		delete(r.containers, containerName)
		r.logger.Info("📦 Container unregistered: %s (agent: %s, purpose: %s)", containerName, info.AgentID, info.Purpose)
	}
}

// UpdateLastUsed updates the last used timestamp for a container.
func (r *ContainerRegistry) UpdateLastUsed(containerName string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if info, exists := r.containers[containerName]; exists {
		info.LastUsed = time.Now()
	}
}

// GetActiveContainers returns a copy of all active container info.
func (r *ContainerRegistry) GetActiveContainers() map[string]RegistryContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make(map[string]RegistryContainerInfo)
	for name, info := range r.containers {
		result[name] = *info // Copy the struct
	}
	return result
}

// GetContainersByAgent returns all containers for a specific agent.
func (r *ContainerRegistry) GetContainersByAgent(agentID string) []RegistryContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	sanitizedAgentID := utils.SanitizeIdentifier(agentID)
	var containers []RegistryContainerInfo

	for _, info := range r.containers {
		if info.AgentID == sanitizedAgentID {
			containers = append(containers, *info) // Copy the struct
		}
	}
	return containers
}

// GetContainerCount returns the total number of registered containers.
func (r *ContainerRegistry) GetContainerCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.containers)
}

// GetActiveAgents returns a list of all agents with active containers.
func (r *ContainerRegistry) GetActiveAgents() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agentSet := make(map[string]bool)
	for _, info := range r.containers {
		agentSet[info.AgentID] = true
	}

	agents := make([]string, 0, len(agentSet))
	for agent := range agentSet {
		agents = append(agents, agent)
	}
	return agents
}

// GetStaleContainers returns containers that haven't been used for the specified duration.
func (r *ContainerRegistry) GetStaleContainers(staleDuration time.Duration) []RegistryContainerInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	cutoff := time.Now().Add(-staleDuration)
	var stale []RegistryContainerInfo

	for _, info := range r.containers {
		if info.LastUsed.Before(cutoff) {
			stale = append(stale, *info) // Copy the struct
		}
	}
	return stale
}

// StopAllContainers gracefully stops all registered containers.
// This should be called during application shutdown.
func (r *ContainerRegistry) StopAllContainers(ctx context.Context, executor Executor) error {
	r.mu.Lock()
	containers := make(map[string]*RegistryContainerInfo)
	for name, info := range r.containers {
		containers[name] = info
	}
	r.mu.Unlock()

	if len(containers) == 0 {
		r.logger.Info("📦 No containers to stop")
		return nil
	}

	r.logger.Info("📦 Stopping %d registered containers for graceful shutdown", len(containers))

	var errors []error
	for containerName, info := range containers {
		r.logger.Info("📦 Stopping container %s (agent: %s, purpose: %s)", containerName, info.AgentID, info.Purpose)

		// Try to stop the container gracefully.
		if longRunning, ok := executor.(*LongRunningDockerExec); ok {
			if err := longRunning.StopContainer(ctx, containerName); err != nil {
				r.logger.Error("Failed to stop container %s: %v", containerName, err)
				errors = append(errors, err)
			} else {
				r.Unregister(containerName)
			}
		}
	}

	if len(errors) > 0 {
		r.logger.Warn("📦 Some containers failed to stop gracefully: %d errors", len(errors))
		return errors[0] // Return first error
	}

	r.logger.Info("📦 All registered containers stopped successfully")
	return nil
}

// StartCleanupRoutine starts a background routine that periodically cleans up stale containers.
func (r *ContainerRegistry) StartCleanupRoutine(ctx context.Context, executor Executor, cleanupInterval, staleThreshold time.Duration) {
	go func() {
		defer close(r.done)

		ticker := time.NewTicker(cleanupInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				r.logger.Info("📦 Container cleanup routine stopping due to context cancellation")
				return
			case <-r.shutdown:
				r.logger.Info("📦 Container cleanup routine stopping due to shutdown signal")
				return
			case <-ticker.C:
				r.cleanupStaleContainers(ctx, executor, staleThreshold)
			}
		}
	}()
}

// cleanupStaleContainers removes containers that haven't been used recently.
func (r *ContainerRegistry) cleanupStaleContainers(ctx context.Context, executor Executor, staleThreshold time.Duration) {
	staleContainers := r.GetStaleContainers(staleThreshold)
	if len(staleContainers) == 0 {
		return
	}

	r.logger.Info("📦 Found %d stale containers, cleaning up", len(staleContainers))

	for _, container := range staleContainers {
		r.logger.Info("📦 Cleaning up stale container %s (agent: %s, idle for %v)",
			container.ContainerName, container.AgentID, time.Since(container.LastUsed))

		if longRunning, ok := executor.(*LongRunningDockerExec); ok {
			if err := longRunning.StopContainer(ctx, container.ContainerName); err != nil {
				r.logger.Error("Failed to cleanup stale container %s: %v", container.ContainerName, err)
			} else {
				r.Unregister(container.ContainerName)
			}
		}
	}
}

// Shutdown signals the cleanup routine to stop and waits for it to finish.
func (r *ContainerRegistry) Shutdown() {
	close(r.shutdown)
	<-r.done
}
