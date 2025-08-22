package persistence

import (
	"testing"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

func TestMetricsConfigHelpers(t *testing.T) {
	// Test the helper functions for extracting config values
	tests := []struct { //nolint:govet
		name               string
		cfg                config.Config
		expectedConfigured bool
	}{
		{
			name: "enabled_metrics",
			cfg: config.Config{
				Agents: &config.AgentConfig{
					Metrics: config.MetricsConfig{
						Enabled: true,
					},
				},
			},
			expectedConfigured: true,
		},
		{
			name: "disabled_metrics",
			cfg: config.Config{
				Agents: &config.AgentConfig{
					Metrics: config.MetricsConfig{
						Enabled: false,
					},
				},
			},
			expectedConfigured: false,
		},
		{
			name:               "nil_agents",
			cfg:                config.Config{Agents: nil},
			expectedConfigured: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test isMetricsConfigured function
			configured := isMetricsConfigured(tt.cfg)
			if configured != tt.expectedConfigured {
				t.Errorf("isMetricsConfigured() = %v, want %v", configured, tt.expectedConfigured)
			}

			// Test logging (this will show the internal metrics flow)
			logger := logx.NewLogger("test-metrics")
			logger.Info("Testing metrics config: enabled=%v", configured)
		})
	}
}
