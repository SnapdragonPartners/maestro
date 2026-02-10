package factory

import (
	"testing"

	"orchestrator/pkg/config"
)

// TestNewAgentFactory tests factory construction.
func TestNewAgentFactory(t *testing.T) {
	factory := NewAgentFactory(nil, nil, nil, nil, nil)

	if factory == nil {
		t.Fatal("expected factory, got nil")
	}

	if factory.dispatcher != nil {
		t.Error("expected nil dispatcher")
	}

	if factory.persistenceChannel != nil {
		t.Error("expected nil persistenceChannel")
	}

	if factory.chatService != nil {
		t.Error("expected nil chatService")
	}

	if factory.llmFactory != nil {
		t.Error("expected nil llmFactory")
	}
}

// TestGetWorkDirFromConfig tests work directory resolution.
func TestGetWorkDirFromConfig(t *testing.T) {
	// Test with nil config (should fallback gracefully)
	workDir := getWorkDirFromConfig(nil)

	if workDir == "" {
		t.Error("expected work directory to be set")
	}
}

// TestGetWorkDirFromConfigFallback tests fallback behavior.
func TestGetWorkDirFromConfigFallback(t *testing.T) {
	// Create a minimal config
	cfg := &config.Config{}

	workDir := getWorkDirFromConfig(cfg)

	// Should not panic and should return something
	if workDir == "" {
		t.Error("expected work directory to be set even with minimal config")
	}
}
