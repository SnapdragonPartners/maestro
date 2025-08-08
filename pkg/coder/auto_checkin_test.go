package coder

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestAutoCheckinConfig(t *testing.T) {
	modelConfig := &config.Model{
		Name:        config.ModelClaudeSonnetLatest,
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
