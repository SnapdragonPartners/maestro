package limiter

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestLimiterExample(t *testing.T) {
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
	if limiter == nil {
		t.Error("Expected limiter to be created")
	}

	// Test basic functionality
	err := limiter.Reserve(config.ModelClaudeSonnetLatest, 100)
	if err != nil {
		t.Errorf("Expected reserve to succeed, got error: %v", err)
	}

	// Test unknown model
	err = limiter.Reserve("unknown-model", 100)
	if err == nil {
		t.Error("Expected error for unknown model")
	}
}
