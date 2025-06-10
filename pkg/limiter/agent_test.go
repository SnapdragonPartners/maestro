package limiter

import (
	"testing"
)

func TestAgentReservation(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test initial agent count
	_, _, agents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 0 {
		t.Errorf("Expected 0 agents initially, got %d", agents)
	}

	// Test successful agent reservation
	err = limiter.ReserveAgent("claude")
	if err != nil {
		t.Fatalf("Failed to reserve agent: %v", err)
	}

	_, _, agents, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 1 {
		t.Errorf("Expected 1 agent after reservation, got %d", agents)
	}

	// Reserve more agents up to limit
	err = limiter.ReserveAgent("claude")
	if err != nil {
		t.Fatalf("Failed to reserve second agent: %v", err)
	}

	err = limiter.ReserveAgent("claude")
	if err != nil {
		t.Fatalf("Failed to reserve third agent: %v", err)
	}

	_, _, agents, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 3 {
		t.Errorf("Expected 3 agents after reservations, got %d", agents)
	}

	// Test agent limit exceeded
	err = limiter.ReserveAgent("claude")
	if err != ErrAgentLimit {
		t.Errorf("Expected agent limit error, got %v", err)
	}
}

func TestAgentRelease(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Reserve some agents first
	err := limiter.ReserveAgent("claude")
	if err != nil {
		t.Fatalf("Failed to reserve agent: %v", err)
	}

	err = limiter.ReserveAgent("claude")
	if err != nil {
		t.Fatalf("Failed to reserve second agent: %v", err)
	}

	_, _, agents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 2 {
		t.Errorf("Expected 2 agents, got %d", agents)
	}

	// Test successful release
	err = limiter.ReleaseAgent("claude")
	if err != nil {
		t.Fatalf("Failed to release agent: %v", err)
	}

	_, _, agents, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 1 {
		t.Errorf("Expected 1 agent after release, got %d", agents)
	}

	// Release remaining agent
	err = limiter.ReleaseAgent("claude")
	if err != nil {
		t.Fatalf("Failed to release remaining agent: %v", err)
	}

	_, _, agents, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 0 {
		t.Errorf("Expected 0 agents after final release, got %d", agents)
	}

	// Test releasing when no agents are reserved
	err = limiter.ReleaseAgent("claude")
	if err == nil {
		t.Error("Expected error when releasing with no agents reserved")
	}
}

func TestAgentLimitsPerModel(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Claude has max 3 agents
	for i := 0; i < 3; i++ {
		err := limiter.ReserveAgent("claude")
		if err != nil {
			t.Fatalf("Failed to reserve claude agent %d: %v", i+1, err)
		}
	}

	// O3 has max 1 agent
	err := limiter.ReserveAgent("o3")
	if err != nil {
		t.Fatalf("Failed to reserve o3 agent: %v", err)
	}

	// Both should now be at limit
	err = limiter.ReserveAgent("claude")
	if err != ErrAgentLimit {
		t.Errorf("Expected claude agent limit error, got %v", err)
	}

	err = limiter.ReserveAgent("o3")
	if err != ErrAgentLimit {
		t.Errorf("Expected o3 agent limit error, got %v", err)
	}

	// Check status
	_, _, claudeAgents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get claude status: %v", err)
	}
	if claudeAgents != 3 {
		t.Errorf("Expected claude to have 3 agents, got %d", claudeAgents)
	}

	_, _, o3Agents, err := limiter.GetStatus("o3")
	if err != nil {
		t.Fatalf("Failed to get o3 status: %v", err)
	}
	if o3Agents != 1 {
		t.Errorf("Expected o3 to have 1 agent, got %d", o3Agents)
	}
}

func TestAgentResetDaily(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Reserve all agents
	for i := 0; i < 3; i++ {
		err := limiter.ReserveAgent("claude")
		if err != nil {
			t.Fatalf("Failed to reserve agent %d: %v", i+1, err)
		}
	}

	_, _, agents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status: %v", err)
	}
	if agents != 3 {
		t.Errorf("Expected 3 agents before reset, got %d", agents)
	}

	// Trigger daily reset
	limiter.ResetDaily()

	_, _, agents, err = limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get status after reset: %v", err)
	}
	if agents != 0 {
		t.Errorf("Expected 0 agents after reset, got %d", agents)
	}
}

func TestConcurrentAgentReservation(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	// Test concurrent agent reservations
	done := make(chan bool, 5)
	successCount := 0
	errorCount := 0

	for i := 0; i < 5; i++ {
		go func() {
			err := limiter.ReserveAgent("claude")
			if err == nil {
				successCount++
			} else if err == ErrAgentLimit {
				errorCount++
			} else {
				t.Errorf("Unexpected error: %v", err)
			}
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < 5; i++ {
		<-done
	}

	// Check final state
	_, _, agents, err := limiter.GetStatus("claude")
	if err != nil {
		t.Fatalf("Failed to get final status: %v", err)
	}

	// Should have exactly 3 agents (the max)
	if agents != 3 {
		t.Errorf("Expected 3 agents reserved, got %d", agents)
	}

	// Should have exactly 3 successes and 2 failures
	if successCount != 3 {
		t.Errorf("Expected 3 successful reservations, got %d", successCount)
	}
	if errorCount != 2 {
		t.Errorf("Expected 2 failed reservations, got %d", errorCount)
	}
}

func TestUnknownModelAgent(t *testing.T) {
	cfg := createTestConfig()
	limiter := NewLimiter(cfg)
	defer limiter.Close()

	err := limiter.ReserveAgent("unknown")
	if err == nil {
		t.Error("Expected error for unknown model")
	}

	err = limiter.ReleaseAgent("unknown")
	if err == nil {
		t.Error("Expected error for unknown model")
	}
}
