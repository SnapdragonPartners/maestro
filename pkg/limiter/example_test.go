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
					Name:           "claude-3-5-sonnet-20241022",
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
	err := limiter.Reserve("claude-3-5-sonnet-20241022", 100)
	if err != nil {
		t.Errorf("Expected reserve to succeed, got error: %v", err)
	}

	// Test unknown model
	err = limiter.Reserve("unknown-model", 100)
	if err == nil {
		t.Error("Expected error for unknown model")
	}
}
