package dispatch

import (
	"context"
	"fmt"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/config"
	"orchestrator/pkg/eventlog"
	"orchestrator/pkg/limiter"
	"orchestrator/pkg/proto"
)

// MockDriverAgent implements both Agent and Driver interfaces for testing.
type MockDriverAgent struct {
	id        string
	agentType agent.Type
}

func NewMockDriverAgent(id string, agentType agent.Type) *MockDriverAgent {
	return &MockDriverAgent{
		id:        id,
		agentType: agentType,
	}
}

func (a *MockDriverAgent) GetID() string {
	return a.id
}

func (a *MockDriverAgent) ProcessMessage(_ context.Context, _ *proto.AgentMsg) (*proto.AgentMsg, error) {
	return nil, fmt.Errorf("mock agent - no processing implemented")
}

func (a *MockDriverAgent) Shutdown(_ context.Context) error {
	return nil
}

func (a *MockDriverAgent) GetAgentType() agent.Type {
	return a.agentType
}

func (a *MockDriverAgent) GetCurrentState() proto.State {
	return proto.StateWaiting
}

func TestLogicalNameResolution(t *testing.T) {
	// Create test config with realistic agent types.
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

	// Attach mock agents to simulate real system.
	architectAgent := NewMockDriverAgent("openai_o3:001", agent.TypeArchitect)
	coderAgent := NewMockDriverAgent("claude_sonnet4:001", agent.TypeCoder)

	dispatcher.Attach(architectAgent)
	dispatcher.Attach(coderAgent)

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
