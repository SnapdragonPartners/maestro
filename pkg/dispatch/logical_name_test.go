package dispatch

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestLogicalNaming(t *testing.T) {
	cfg := &config.Config{
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			CoderModel:     config.ModelClaudeSonnetLatest,
			ArchitectModel: config.ModelOpenAIO3Mini,
		},
	}

	// Test basic config validation
	if cfg.Agents.MaxCoders != 2 {
		t.Errorf("Expected MaxCoders 2, got %d", cfg.Agents.MaxCoders)
	}

	if cfg.Agents.CoderModel != config.ModelClaudeSonnetLatest {
		t.Errorf("Expected CoderModel %s, got %s", config.ModelClaudeSonnetLatest, cfg.Agents.CoderModel)
	}

	if cfg.Agents.ArchitectModel != config.ModelOpenAIO3Mini {
		t.Errorf("Expected ArchitectModel %s, got %s", config.ModelOpenAIO3Mini, cfg.Agents.ArchitectModel)
	}

	// Test model info lookup
	info, exists := config.GetModelInfo(config.ModelClaudeSonnetLatest)
	if !exists {
		t.Error("Expected to find Claude model in KnownModels")
	}

	if info.MaxContextTokens != 200000 {
		t.Errorf("Expected Claude MaxContextTokens 200000, got %d", info.MaxContextTokens)
	}
}
