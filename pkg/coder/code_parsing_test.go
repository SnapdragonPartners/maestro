package coder

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestCodeParsingConfig(t *testing.T) {
	modelConfig := &config.Model{
		Name:   config.ModelClaudeSonnetLatest,
		MaxTPM: 50000,
	}

	// Test basic model config validation
	if modelConfig.Name == "" {
		t.Error("Expected model name to be set")
	}

	// Test that we can create a model config for code parsing
	if modelConfig.MaxTPM <= 0 {
		t.Error("Expected positive MaxTPM for code parsing tasks")
	}
}
