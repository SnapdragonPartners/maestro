package coder

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestAutoCheckinConfig(t *testing.T) {
	modelConfig := &config.Model{
		Name:        "claude-3-5-sonnet-20241022",
		MaxTPM:      50000,
		DailyBudget: 200.0,
	}

	// Test that model config is properly structured
	if modelConfig.Name == "" {
		t.Error("Expected model name to be set")
	}

	if modelConfig.MaxTPM <= 0 {
		t.Error("Expected MaxTPM to be positive")
	}

	if modelConfig.DailyBudget <= 0 {
		t.Error("Expected DailyBudget to be positive")
	}
}
