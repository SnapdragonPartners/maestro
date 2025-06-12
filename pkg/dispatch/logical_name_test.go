package dispatch

import (
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
)

func TestLogicalNameResolution(t *testing.T) {
	// Create test config with realistic agent types
	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude_sonnet4": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 10.0,
				CpmTokensIn:        0.003,
				CpmTokensOut:       0.015,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "test-coder", ID: "001", Type: "coder", WorkDir: "./work/coder"},
				},
			},
			"openai_o3": {
				MaxTokensPerMinute: 500,
				MaxBudgetPerDayUSD: 5.0,
				CpmTokensIn:        0.004,
				CpmTokensOut:       0.016,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "test-architect", ID: "001", Type: "architect", WorkDir: "./work/architect"},
				},
			},
		},
	}

	rateLimiter := limiter.NewLimiter(cfg)
	eventLog, err := eventlog.NewWriter("logs", 24)
	if err != nil {
		t.Fatalf("Failed to create event log: %v", err)
	}
	defer eventLog.Close()

	dispatcher, err := NewDispatcher(cfg, rateLimiter, eventLog)
	if err != nil {
		t.Fatalf("Failed to create dispatcher: %v", err)
	}

	// Register mock agents to simulate real system
	architectAgent := &MockAgent{id: "openai_o3:001"}
	coderAgent := &MockAgent{id: "claude_sonnet4:001"}

	err = dispatcher.RegisterAgent(architectAgent)
	if err != nil {
		t.Fatalf("Failed to register architect agent: %v", err)
	}

	err = dispatcher.RegisterAgent(coderAgent)
	if err != nil {
		t.Fatalf("Failed to register coder agent: %v", err)
	}

	testCases := []struct {
		input    string
		expected string
		desc     string
	}{
		{"architect", "openai_o3:001", "architect should resolve to openai_o3:001"},
		{"coder", "claude_sonnet4:001", "coder should resolve to claude_sonnet4:001"},
		{"claude", "claude", "claude should NOT be resolved (not a logical name)"},
		{"nonexistent", "nonexistent", "unknown names should pass through unchanged"},
		{"openai_o3:001", "openai_o3:001", "actual agent IDs should pass through unchanged"},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			result := dispatcher.resolveAgentName(tc.input)
			if result != tc.expected {
				t.Errorf("resolveAgentName(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}
