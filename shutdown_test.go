package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"orchestrator/pkg/config"
)

// TestGracefulShutdown verifies the acceptance criteria:
// "Integration test kills orchestrator; STATUS.md exists for each agent"
func TestGracefulShutdown(t *testing.T) {
	// Setup test environment
	tmpDir := t.TempDir()

	// Create test config
	configPath := filepath.Join(tmpDir, "config.json")
	testConfig := `{
  "models": {
    "claude_sonnet4": {
      "max_tokens_per_minute": 1000,
      "max_budget_per_day_usd": 25.0,
      "cpm_tokens_in": 0.003,
      "cpm_tokens_out": 0.015,
      "api_key": "test-key",
      "agents": [
        {
          "name": "claude-shutdown",
          "id": "001",
          "type": "coder",
          "workdir": "./work/claude-shutdown"
        }
      ]
    },
    "openai_o3": {
      "max_tokens_per_minute": 500,
      "max_budget_per_day_usd": 10.0,
      "cpm_tokens_in": 0.004,
      "cpm_tokens_out": 0.016,
      "api_key": "test-key",
      "agents": [
        {
          "name": "architect-shutdown",
          "id": "001",
          "type": "architect",
          "workdir": "./work/architect-shutdown"
        }
      ]
    }
  },
  "graceful_shutdown_timeout_sec": 5,
  "event_log_rotation_hours": 24,
  "max_retry_attempts": 3,
  "retry_backoff_multiplier": 2.0
}`

	err := os.WriteFile(configPath, []byte(testConfig), 0644)
	if err != nil {
		t.Fatalf("Failed to create test config: %v", err)
	}

	// Create stories directory and sample story
	storiesDir := filepath.Join(tmpDir, "stories")
	err = os.MkdirAll(storiesDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create stories directory: %v", err)
	}

	sampleStory := `# Test Story
This is a test story for shutdown testing.

- Basic functionality
- Error handling
`
	err = os.WriteFile(filepath.Join(storiesDir, "001.md"), []byte(sampleStory), 0644)
	if err != nil {
		t.Fatalf("Failed to create sample story: %v", err)
	}

	// Build the orchestrator binary for testing
	binary := filepath.Join(tmpDir, "orchestrator")
	buildCmd := exec.Command("go", "build", "-o", binary, ".")
	buildCmd.Dir = "."
	if err := buildCmd.Run(); err != nil {
		t.Fatalf("Failed to build orchestrator: %v", err)
	}

	// Start orchestrator process
	cmd := exec.Command(binary, "-config", configPath)
	cmd.Dir = tmpDir
	cmd.Env = append(os.Environ(), "CLAUDE_API_KEY=test", "O3_API_KEY=test")

	// Start the process
	if err := cmd.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Give orchestrator time to start up
	time.Sleep(2 * time.Second)

	// Send SIGINT to trigger graceful shutdown
	t.Logf("Sending SIGINT to orchestrator process (PID: %d)", cmd.Process.Pid)
	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatalf("Failed to send SIGINT: %v", err)
	}

	// Wait for process to exit with timeout
	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Logf("Orchestrator exited with error: %v", err)
		} else {
			t.Log("Orchestrator exited cleanly")
		}
	case <-time.After(15 * time.Second):
		// Force kill if it doesn't exit gracefully
		cmd.Process.Kill()
		t.Fatal("Orchestrator did not exit within timeout")
	}

	// Verify STATUS.md files were created
	statusDir := filepath.Join(tmpDir, "status")

	// Check if status directory exists
	if _, err := os.Stat(statusDir); os.IsNotExist(err) {
		t.Fatal("Status directory was not created")
	}

	// Expected status files (using the actual config from the test config file)
	expectedFiles := []string{
		"claude_sonnet4:001-STATUS.md",   // claude agent from test config
		"openai_o3:001-STATUS.md",        // architect agent from test config
		"orchestrator-STATUS.md",
	}

	for _, expectedFile := range expectedFiles {
		statusFile := filepath.Join(statusDir, expectedFile)

		// Check file exists
		if _, err := os.Stat(statusFile); os.IsNotExist(err) {
			t.Errorf("Expected status file %s was not created", expectedFile)
			continue
		}

		// Check file has content
		content, err := os.ReadFile(statusFile)
		if err != nil {
			t.Errorf("Failed to read status file %s: %v", expectedFile, err)
			continue
		}

		if len(content) == 0 {
			t.Errorf("Status file %s is empty", expectedFile)
			continue
		}

		t.Logf("✓ Status file %s created with %d bytes", expectedFile, len(content))

		// Verify content structure for agent status files
		contentStr := string(content)
		if expectedFile != "orchestrator-STATUS.md" {
			if !contains(contentStr, "Agent Status Report") {
				t.Errorf("Status file %s missing agent status report header", expectedFile)
			}
			if !contains(contentStr, "Agent Information") {
				t.Errorf("Status file %s missing agent information section", expectedFile)
			}
		} else {
			if !contains(contentStr, "Orchestrator Status Report") {
				t.Errorf("Orchestrator status file missing report header")
			}
			if !contains(contentStr, "Configuration") {
				t.Errorf("Orchestrator status file missing configuration section")
			}
		}
	}

	// Verify logs directory was created
	logsDir := filepath.Join(tmpDir, "logs")
	if _, err := os.Stat(logsDir); os.IsNotExist(err) {
		t.Error("Logs directory was not created")
	} else {
		t.Log("✓ Logs directory created")
	}
}

// TestOrchestratorCreation tests creating an orchestrator instance
func TestOrchestratorCreation(t *testing.T) {
	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude_sonnet4": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 25.0,
				CpmTokensIn:        0.003,
				CpmTokensOut:       0.015,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "claude-test", ID: "001", Type: "coder", WorkDir: "./work/claude-test"},
				},
			},
		},
		GracefulShutdownTimeoutSec: 30,
		MaxRetryAttempts:           3,
		RetryBackoffMultiplier:     2.0,
	}

	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}

	if orchestrator == nil {
		t.Fatal("Expected orchestrator instance")
	}

	if orchestrator.shutdownTime != 30*time.Second {
		t.Errorf("Expected shutdown time 30s, got %v", orchestrator.shutdownTime)
	}

	// Test start and shutdown
	ctx := context.Background()

	err = orchestrator.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Give it a moment to start
	time.Sleep(100 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	err = orchestrator.Shutdown(shutdownCtx)
	if err != nil {
		t.Fatalf("Failed to shutdown orchestrator: %v", err)
	}
}

// TestStatusGeneration tests the status generation functionality
func TestStatusGeneration(t *testing.T) {
	tmpDir := t.TempDir()

	cfg := &config.Config{
		Models: map[string]config.ModelCfg{
			"claude_sonnet4": {
				MaxTokensPerMinute: 1000,
				MaxBudgetPerDayUSD: 25.0,
				CpmTokensIn:        0.003,
				CpmTokensOut:       0.015,
				APIKey:             "test-key",
				Agents: []config.Agent{
					{Name: "claude-status", ID: "001", Type: "coder", WorkDir: "./work/claude-status"},
				},
			},
		},
		GracefulShutdownTimeoutSec: 5,
		MaxRetryAttempts:           3,
		RetryBackoffMultiplier:     2.0,
	}

	// Change to temp directory for this test
	oldDir, _ := os.Getwd()
	defer os.Chdir(oldDir)
	os.Chdir(tmpDir)

	orchestrator, err := NewOrchestrator(cfg)
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}

	ctx := context.Background()
	err = orchestrator.Start(ctx)
	if err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}

	// Collect status
	err = orchestrator.collectAgentStatus()
	if err != nil {
		t.Fatalf("Failed to collect agent status: %v", err)
	}

	// Verify status files were created
	statusFiles := []string{
		"status/claude_sonnet4:001-STATUS.md",
		"status/orchestrator-STATUS.md",
	}

	for _, statusFile := range statusFiles {
		if _, err := os.Stat(statusFile); os.IsNotExist(err) {
			t.Errorf("Expected status file %s was not created", statusFile)
		} else {
			t.Logf("✓ Status file %s created", statusFile)
		}
	}

	// Cleanup
	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	orchestrator.Shutdown(shutdownCtx)
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) &&
		(s == substr || len(substr) == 0 ||
			(len(s) > len(substr) && containsAt(s, substr, 0)))
}

func containsAt(s, substr string, start int) bool {
	if start+len(substr) > len(s) {
		return false
	}

	for i := 0; i < len(substr); i++ {
		if toLower(s[start+i]) != toLower(substr[i]) {
			if start+1 <= len(s)-len(substr) {
				return containsAt(s, substr, start+1)
			}
			return false
		}
	}
	return true
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}
