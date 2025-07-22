package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"orchestrator/pkg/utils"
)

// TestStory070AcceptanceCriteria tests that LocalExec passes all existing unit tests.
// This is the acceptance criteria for Story 070.
func TestStory070AcceptanceCriteria(t *testing.T) {
	t.Run("LocalExec preserves shell tool behavior", func(t *testing.T) {
		// Create a shell adapter using LocalExec.
		adapter := NewShellCommandAdapter(NewLocalExec())

		// Test 1: Basic command execution.
		result, err := adapter.ExecuteShellCommand(context.Background(), "echo 'Hello World'", "")
		if err != nil {
			t.Fatalf("Basic command failed: %v", err)
		}

		resultMap, err := utils.AssertMapStringAny(result)
		if err != nil {
			t.Fatalf("Result assertion failed: %v", err)
		}
		if resultMap["exit_code"] != 0 {
			t.Errorf("Expected exit code 0, got %v", resultMap["exit_code"])
		}

		stdout, err := utils.GetMapField[string](resultMap, "stdout")
		if err != nil {
			t.Errorf("Failed to get stdout: %v", err)
		} else if !strings.Contains(stdout, "Hello World") {
			t.Errorf("Expected stdout to contain 'Hello World', got %s", stdout)
		}

		// Test 2: Working directory behavior.
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		result, err = adapter.ExecuteShellCommand(context.Background(), "ls test.txt", tempDir)
		if err != nil {
			t.Fatalf("Working directory command failed: %v", err)
		}

		resultMap, err = utils.AssertMapStringAny(result)
		if err != nil {
			t.Fatalf("Result assertion failed: %v", err)
		}
		if resultMap["exit_code"] != 0 {
			t.Errorf("Expected exit code 0, got %v", resultMap["exit_code"])
		}

		stdout, err = utils.GetMapField[string](resultMap, "stdout")
		if err != nil {
			t.Errorf("Failed to get stdout: %v", err)
		} else if !strings.Contains(stdout, "test.txt") {
			t.Errorf("Expected stdout to contain 'test.txt', got %s", stdout)
		}

		// Test 3: Error handling.
		result, err = adapter.ExecuteShellCommand(context.Background(), "false", "")
		if err != nil {
			t.Fatalf("Error handling test failed: %v", err)
		}

		resultMap, err = utils.AssertMapStringAny(result)
		if err != nil {
			t.Fatalf("Result assertion failed: %v", err)
		}
		if resultMap["exit_code"] != 1 {
			t.Errorf("Expected exit code 1, got %v", resultMap["exit_code"])
		}

		// Test 4: stderr capture.
		result, err = adapter.ExecuteShellCommand(context.Background(), "echo 'error message' >&2", "")
		if err != nil {
			t.Fatalf("stderr capture test failed: %v", err)
		}

		resultMap, err = utils.AssertMapStringAny(result)
		if err != nil {
			t.Fatalf("Result assertion failed: %v", err)
		}
		if resultMap["exit_code"] != 0 {
			t.Errorf("Expected exit code 0, got %v", resultMap["exit_code"])
		}

		stderr, err := utils.GetMapField[string](resultMap, "stderr")
		if err != nil {
			t.Errorf("Failed to get stderr: %v", err)
		} else if !strings.Contains(stderr, "error message") {
			t.Errorf("Expected stderr to contain 'error message', got %s", stderr)
		}
	})

	t.Run("Registry provides local executor by default", func(t *testing.T) {
		// Test global registry has local executor.
		names := List()
		found := false
		for _, name := range names {
			if name == "local" {
				found = true
				break
			}
		}

		if !found {
			t.Error("Global registry should have local executor by default")
		}

		// Test we can get the default executor.
		defaultExec, err := GetDefault()
		if err != nil {
			t.Fatalf("Failed to get default executor: %v", err)
		}

		if defaultExec.Name() != "local" {
			t.Errorf("Expected default executor to be 'local', got %s", defaultExec.Name())
		}

		// Test it's available.
		if !defaultExec.Available() {
			t.Error("Default executor should be available")
		}
	})

	t.Run("Executor interface provides consistent API", func(t *testing.T) {
		localExec := NewLocalExec()

		// Test interface methods.
		if localExec.Name() != "local" {
			t.Errorf("Expected name 'local', got %s", localExec.Name())
		}

		if !localExec.Available() {
			t.Error("Local executor should always be available")
		}

		// Test execution with proper options.
		opts := DefaultExecOpts()
		opts.Timeout = 5 * time.Second

		result, err := localExec.Run(context.Background(), []string{"echo", "test"}, &opts)
		if err != nil {
			t.Fatalf("Execution failed: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got %d", result.ExitCode)
		}

		if result.ExecutorUsed != "local" {
			t.Errorf("Expected executor 'local', got %s", result.ExecutorUsed)
		}

		if result.Duration <= 0 {
			t.Error("Expected positive duration")
		}

		if !strings.Contains(result.Stdout, "test") {
			t.Errorf("Expected stdout to contain 'test', got %s", result.Stdout)
		}
	})
}

// TestBackwardCompatibility ensures the new executor system doesn't break existing code.
func TestBackwardCompatibility(t *testing.T) {
	// Test that shell adapter maintains the same interface as the original shell tool.
	adapter := NewShellCommandAdapter(NewLocalExec())

	// These are the exact patterns used by the original shell tool.
	testCases := []struct {
		name    string
		command string
		cwd     string
		wantErr bool
	}{
		{
			name:    "simple echo",
			command: "echo hello",
			cwd:     "",
			wantErr: false,
		},
		{
			name:    "compound command",
			command: "mkdir -p test && echo 'file content' > test/file.txt",
			cwd:     t.TempDir(),
			wantErr: false,
		},
		{
			name:    "failing command",
			command: "exit 1",
			cwd:     "",
			wantErr: false, // Should not return error, just non-zero exit code
		},
		{
			name:    "empty command",
			command: "",
			cwd:     "",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result, err := adapter.ExecuteShellCommand(context.Background(), tc.command, tc.cwd)

			if tc.wantErr && err == nil {
				t.Error("Expected error, got nil")
			}

			if !tc.wantErr && err != nil {
				t.Errorf("Unexpected error: %v", err)
			}

			if !tc.wantErr {
				// Check result format matches original shell tool.
				resultMap, ok := result.(map[string]any)
				if !ok {
					t.Fatalf("Expected result to be map[string]any, got %T", result)
				}

				requiredKeys := []string{"stdout", "stderr", "exit_code", "cwd"}
				for _, key := range requiredKeys {
					if _, exists := resultMap[key]; !exists {
						t.Errorf("Missing required key: %s", key)
					}
				}

				// Check types.
				if _, ok := resultMap["stdout"].(string); !ok {
					t.Error("stdout should be string")
				}
				if _, ok := resultMap["stderr"].(string); !ok {
					t.Error("stderr should be string")
				}
				if _, ok := resultMap["exit_code"].(int); !ok {
					t.Error("exit_code should be int")
				}
				if _, ok := resultMap["cwd"].(string); !ok {
					t.Error("cwd should be string")
				}
			}
		})
	}
}
