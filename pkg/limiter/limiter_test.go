package limiter

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestLimiter(t *testing.T) {
	cfg := &config.Config{
		Orchestrator: &config.OrchestratorConfig{
			Models: []config.Model{
				{
					Name:           config.ModelClaudeSonnetLatest,
					MaxTPM:         50000,
					DailyBudget:    200.0,
					MaxConnections: 4,
					CPM:            3.0,
				},
			},
		},
	}

	limiter := NewLimiter(cfg)

	// Test token reservation
	err := limiter.Reserve(config.ModelClaudeSonnetLatest, 100)
	if err != nil {
		t.Errorf("Expected reserve to succeed, got error: %v", err)
	}

	// Test budget reservation
	err = limiter.ReserveBudget(config.ModelClaudeSonnetLatest, 1.0)
	if err != nil {
		t.Errorf("Expected budget reserve to succeed, got error: %v", err)
	}

	// Test agent reservation
	err = limiter.ReserveAgent(config.ModelClaudeSonnetLatest)
	if err != nil {
		t.Errorf("Expected agent reserve to succeed, got error: %v", err)
	}

	// Test agent release
	err = limiter.ReleaseAgent(config.ModelClaudeSonnetLatest)
	if err != nil {
		t.Errorf("Expected agent release to succeed, got error: %v", err)
	}

	// Test status
	tokens, budget, agents, err := limiter.GetStatus(config.ModelClaudeSonnetLatest)
	if err != nil {
		t.Errorf("Expected status to succeed, got error: %v", err)
	}

	t.Logf("Status: tokens=%d, budget=%.2f, agents=%d", tokens, budget, agents)
}
