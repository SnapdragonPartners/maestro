package coder

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestAutoCheckinConfig(t *testing.T) {
	// Test that model info is properly structured
	modelInfo, exists := config.GetModelInfo(config.ModelClaudeSonnetLatest)
	if !exists {
		t.Error("Expected to find model info for Claude Sonnet")
	}

	if modelInfo.MaxContextTokens <= 0 {
		t.Error("Expected MaxContextTokens to be positive")
	}

	if modelInfo.MaxOutputTokens <= 0 {
		t.Error("Expected MaxOutputTokens to be positive")
	}

	if modelInfo.InputCPM <= 0 {
		t.Error("Expected InputCPM to be positive")
	}
}
