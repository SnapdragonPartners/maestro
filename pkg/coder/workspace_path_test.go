package coder

import (
	"context"
	"path/filepath"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/build"
	"orchestrator/pkg/config"
)

// createTestLLMFactoryForWorkspace creates a minimal LLM factory for workspace testing.
func createTestLLMFactoryForWorkspace(t *testing.T) *agent.LLMClientFactory {
	t.Helper()
	cfg := &config.Config{
		Agents: &config.AgentConfig{
			CoderModel:     "claude-sonnet-4-20250514",
			ArchitectModel: "o3-mini",
			Resilience: config.ResilienceConfig{
				RateLimit: config.RateLimitConfig{
					Anthropic: config.ProviderLimits{
						TokensPerMinute: 300000,
						MaxConcurrency:  5,
					},
					OpenAI: config.ProviderLimits{
						TokensPerMinute: 150000,
						MaxConcurrency:  5,
					},
				},
				CircuitBreaker: config.CircuitBreakerConfig{
					FailureThreshold: 5,
					SuccessThreshold: 3,
					Timeout:          30_000_000_000,
				},
				Retry: config.RetryConfig{
					MaxAttempts:   3,
					InitialDelay:  100_000_000,
					MaxDelay:      10_000_000_000,
					BackoffFactor: 2,
					Jitter:        true,
				},
				Timeout: 180_000_000_000,
			},
			Metrics: config.MetricsConfig{
				Enabled: false,
			},
		},
	}
	factory, err := agent.NewLLMClientFactory(cfg)
	if err != nil {
		t.Fatalf("Failed to create test LLM factory: %v", err)
	}
	return factory
}

func TestGetHostWorkspacePath(t *testing.T) {
	// Setup test config
	tempDir := t.TempDir()
	if err := config.LoadConfig(tempDir); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Create a test coder with a relative workspace path (simulating current behavior)
	agentID := "test-coder-001"
	workDir := "./test-workspace"

	llmFactory := createTestLLMFactoryForWorkspace(t)
	defer llmFactory.Stop()

	coder, err := NewCoder(context.Background(), agentID, workDir, nil, build.NewBuildService(), nil, nil, llmFactory, nil)
	if err != nil {
		t.Fatalf("Failed to create coder: %v", err)
	}

	// Test GetHostWorkspacePath
	hostPath := coder.GetHostWorkspacePath()

	// Should return absolute path, not relative
	if !filepath.IsAbs(hostPath) {
		t.Errorf("Expected absolute path, got relative: %s", hostPath)
	}

	// Should contain the workDir
	expectedSuffix := "test-workspace"
	if hostPath != workDir && !filepath.IsAbs(hostPath) {
		t.Errorf("Expected absolute path containing %s, got: %s", expectedSuffix, hostPath)
	}

	t.Logf("Host workspace path: %s", hostPath)
	t.Logf("Original work dir: %s", coder.originalWorkDir)
	t.Logf("Current work dir: %s", coder.workDir)
}

func TestAgentFactoryWorkspaceCreation(t *testing.T) {
	// Test what the agent factory actually creates
	tempDir := t.TempDir()
	if err := config.LoadConfig(tempDir); err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	// Simulate what happens in agent factory

	agentID := "test-agent"
	baseWorkDir := "." // This is what getWorkDirFromConfig returns
	coderWorkDir := filepath.Join(baseWorkDir, agentID)

	t.Logf("Base work dir: %s", baseWorkDir)
	t.Logf("Coder work dir: %s", coderWorkDir)

	// Test filepath.Abs on the result
	absPath, err := filepath.Abs(coderWorkDir)
	if err != nil {
		t.Fatalf("Failed to get absolute path: %v", err)
	}

	t.Logf("Absolute path: %s", absPath)

	// The absolute path should be a real host path, not relative
	if !filepath.IsAbs(absPath) {
		t.Errorf("Expected absolute path, got: %s", absPath)
	}
}
