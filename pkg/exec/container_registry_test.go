package exec

import (
	"fmt"
	"testing"
	"time"

	"orchestrator/pkg/logx"
)

func TestContainerRegistry_Register(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	// Test registration
	registry.Register("claude_sonnet4:001", "maestro-story-claude_sonnet4-001", "planning")

	containers := registry.GetActiveContainers()
	if len(containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(containers))
	}

	containerName := "maestro-story-claude_sonnet4-001"
	info, exists := containers[containerName]
	if !exists {
		t.Fatal("Container not found in registry")
	}

	// Agent ID should be sanitized
	if info.AgentID != "claude_sonnet4-001" {
		t.Errorf("Expected sanitized agent ID 'claude_sonnet4-001', got '%s'", info.AgentID)
	}

	if info.Purpose != "planning" {
		t.Errorf("Expected purpose 'planning', got '%s'", info.Purpose)
	}

	if info.ContainerName != containerName {
		t.Errorf("Expected container name '%s', got '%s'", containerName, info.ContainerName)
	}
}

func TestContainerRegistry_Unregister(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	containerName := "test-container"
	registry.Register("test-agent", containerName, "testing")

	// Verify registered
	if registry.GetContainerCount() != 1 {
		t.Fatal("Container should be registered")
	}

	// Unregister
	registry.Unregister(containerName)

	// Verify unregistered
	if registry.GetContainerCount() != 0 {
		t.Fatal("Container should be unregistered")
	}
}

func TestContainerRegistry_GetContainersByAgent(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	// Register containers for different agents
	registry.Register("agent1:v1", "container1", "planning")
	registry.Register("agent1:v1", "container2", "coding")
	registry.Register("agent2:v2", "container3", "planning")

	// Test getting containers for agent1
	agent1Containers := registry.GetContainersByAgent("agent1:v1")
	if len(agent1Containers) != 2 {
		t.Fatalf("Expected 2 containers for agent1, got %d", len(agent1Containers))
	}

	// Test getting containers for agent2
	agent2Containers := registry.GetContainersByAgent("agent2:v2")
	if len(agent2Containers) != 1 {
		t.Fatalf("Expected 1 container for agent2, got %d", len(agent2Containers))
	}

	// Test getting containers for non-existent agent
	agent3Containers := registry.GetContainersByAgent("agent3")
	if len(agent3Containers) != 0 {
		t.Fatalf("Expected 0 containers for non-existent agent, got %d", len(agent3Containers))
	}
}

func TestContainerRegistry_GetActiveAgents(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	// Register containers for different agents
	registry.Register("agent1:v1", "container1", "planning")
	registry.Register("agent1:v1", "container2", "coding")
	registry.Register("agent2:v2", "container3", "planning")

	activeAgents := registry.GetActiveAgents()
	if len(activeAgents) != 2 {
		t.Fatalf("Expected 2 active agents, got %d", len(activeAgents))
	}

	// Check that both agents are present (order doesn't matter)
	agentSet := make(map[string]bool)
	for _, agent := range activeAgents {
		agentSet[agent] = true
	}

	if !agentSet["agent1-v1"] {
		t.Error("Expected agent1-v1 to be active")
	}
	if !agentSet["agent2-v2"] {
		t.Error("Expected agent2-v2 to be active")
	}
}

func TestContainerRegistry_UpdateLastUsed(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	containerName := "test-container"
	registry.Register("test-agent", containerName, "testing")

	// Get initial timestamp
	containers := registry.GetActiveContainers()
	initialTime := containers[containerName].LastUsed

	// Wait a bit and update
	time.Sleep(10 * time.Millisecond)
	registry.UpdateLastUsed(containerName)

	// Verify timestamp was updated
	updatedContainers := registry.GetActiveContainers()
	updatedTime := updatedContainers[containerName].LastUsed

	if !updatedTime.After(initialTime) {
		t.Error("LastUsed timestamp should be updated")
	}
}

func TestContainerRegistry_GetStaleContainers(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	// Register a container
	containerName := "test-container"
	registry.Register("test-agent", containerName, "testing")

	// Initially, no containers should be stale
	stale := registry.GetStaleContainers(1 * time.Hour)
	if len(stale) != 0 {
		t.Fatalf("Expected 0 stale containers, got %d", len(stale))
	}

	// Containers should be stale with a very short threshold
	stale = registry.GetStaleContainers(1 * time.Nanosecond)
	if len(stale) != 1 {
		t.Fatalf("Expected 1 stale container, got %d", len(stale))
	}

	if stale[0].ContainerName != containerName {
		t.Errorf("Expected stale container '%s', got '%s'", containerName, stale[0].ContainerName)
	}
}

func TestContainerRegistry_ConcurrentAccess(t *testing.T) {
	logger := logx.NewLogger("test")
	registry := NewContainerRegistry(logger)

	// Test concurrent registration and unregistration
	done := make(chan bool, 100)

	// Start multiple goroutines doing concurrent operations
	for i := 0; i < 50; i++ {
		go func(id int) {
			containerName := fmt.Sprintf("container-%d", id)
			agentName := fmt.Sprintf("agent-%d", id)

			// Register
			registry.Register(agentName, containerName, "testing")

			// Update last used
			registry.UpdateLastUsed(containerName)

			// Check if it exists
			containers := registry.GetActiveContainers()
			if _, exists := containers[containerName]; !exists {
				t.Errorf("Container %s should exist", containerName)
			}

			// Unregister
			registry.Unregister(containerName)

			done <- true
		}(i)
	}

	// Wait for all operations to complete
	for i := 0; i < 50; i++ {
		<-done
	}

	// Should have no containers left
	if registry.GetContainerCount() != 0 {
		t.Errorf("Expected 0 containers after cleanup, got %d", registry.GetContainerCount())
	}
}
