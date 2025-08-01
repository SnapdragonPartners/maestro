package dispatch

import (
	"testing"

	"orchestrator/pkg/config"
)

func TestLogicalNaming(t *testing.T) {
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
				{
					Name:           "o3-mini",
					MaxTPM:         10000,
					DailyBudget:    100.0,
					MaxConnections: 2,
					CPM:            15.0,
				},
			},
		},
		Agents: &config.AgentConfig{
			MaxCoders:      2,
			CoderModel:     "claude-3-5-sonnet-20241022",
			ArchitectModel: "o3-mini",
		},
	}

	// Test basic config validation
	if len(cfg.Orchestrator.Models) != 2 {
		t.Errorf("Expected 2 models, got %d", len(cfg.Orchestrator.Models))
	}

	if cfg.Agents.MaxCoders != 2 {
		t.Errorf("Expected MaxCoders 2, got %d", cfg.Agents.MaxCoders)
	}

	// Test model lookup by name
	var claudeModel *config.Model
	for i, model := range cfg.Orchestrator.Models {
		if model.Name == "claude-3-5-sonnet-20241022" {
			claudeModel = &cfg.Orchestrator.Models[i]
			break
		}
	}

	if claudeModel == nil {
		t.Error("Expected to find Claude model")
	}

	if claudeModel != nil && claudeModel.MaxTPM != 50000 {
		t.Errorf("Expected Claude MaxTPM 50000, got %d", claudeModel.MaxTPM)
	}
}
