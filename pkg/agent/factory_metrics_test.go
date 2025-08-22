package agent

import (
	"testing"

	"orchestrator/pkg/agent/middleware/metrics"
	"orchestrator/pkg/config"
)

func TestMetricsRecorderSelection(t *testing.T) {
	tests := []struct {
		name         string
		description  string
		enabled      bool
		wantInternal bool
	}{
		{
			name:         "enabled_metrics_uses_internal",
			enabled:      true,
			wantInternal: true,
			description:  "Enabled config should use internal metrics recorder",
		},
		{
			name:         "disabled_metrics_uses_noop",
			enabled:      false,
			wantInternal: false,
			description:  "Disabled metrics should use no-op recorder",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := config.Config{
				Agents: &config.AgentConfig{
					Metrics: config.MetricsConfig{
						Enabled: tt.enabled,
					},
				},
			}

			factory, err := NewLLMClientFactory(cfg)
			if err != nil {
				t.Fatalf("NewLLMClientFactory() error = %v", err)
			}

			// Check the type of recorder
			isInternal := isInternalRecorder(factory.metricsRecorder)

			if isInternal != tt.wantInternal {
				t.Errorf("%s: got Internal=%v, want=%v", tt.description, isInternal, tt.wantInternal)
			}
		})
	}
}

func TestDefaultConfigUsesInternal(t *testing.T) {
	// Test that a config with our new defaults creates an internal recorder
	cfg := config.Config{
		Agents: &config.AgentConfig{
			Metrics: config.MetricsConfig{
				Enabled: true, // Our new default
			},
		},
	}

	factory, err := NewLLMClientFactory(cfg)
	if err != nil {
		t.Fatalf("NewLLMClientFactory() error = %v", err)
	}

	if !isInternalRecorder(factory.metricsRecorder) {
		t.Error("Config with enabled=true should create internal recorder, but got no-op")
	}
}

// isInternalRecorder checks if the recorder is an internal recorder
// by attempting a type assertion (simple way to distinguish without exposing internals).
func isInternalRecorder(recorder metrics.Recorder) bool {
	// The internal recorder is *InternalRecorder, noop is *NoopRecorder
	// We can distinguish by the type name
	switch recorder.(type) {
	case *metrics.InternalRecorder:
		return true
	default:
		return false
	}
}
