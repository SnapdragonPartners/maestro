package coder

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestCodeParsingConfig(t *testing.T) {
	// Test basic model info validation
	modelInfo, exists := config.GetModelInfo(config.ModelClaudeSonnetLatest)
	if !exists {
		t.Error("Expected to find model info for code parsing")
	}

	// Test that we can get model info for code parsing tasks
	if modelInfo.MaxContextTokens <= 0 {
		t.Error("Expected positive MaxContextTokens for code parsing tasks")
	}

	if modelInfo.MaxOutputTokens <= 0 {
		t.Error("Expected positive MaxOutputTokens for code parsing tasks")
	}
}
