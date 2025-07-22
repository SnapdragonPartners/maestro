package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLocalExec_Name(t *testing.T) {
	exec := NewLocalExec()
	if exec.Name() != "local" {
		t.Errorf("Expected name 'local', got %s", exec.Name())
	}
}

func TestLocalExec_Available(t *testing.T) {
	exec := NewLocalExec()
	if !exec.Available() {
		t.Error("LocalExec should always be available")
	}
}

func TestLocalExec_Run_Success(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	// Test simple command.
	opts := DefaultExecOpts()
	result, err := exec.Run(ctx, []string{"echo", "hello world"}, &opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if strings.TrimSpace(result.Stdout) != "hello world" {
		t.Errorf("Expected stdout 'hello world', got %s", result.Stdout)
	}

	if result.ExecutorUsed != "local" {
		t.Errorf("Expected executor 'local', got %s", result.ExecutorUsed)
	}

	if result.Duration <= 0 {
		t.Error("Expected positive duration")
	}
}

func TestLocalExec_Run_Failure(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	// Test command that fails.
	opts := DefaultExecOpts()
	result, err := exec.Run(ctx, []string{"false"}, &opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.ExitCode != 1 {
		t.Errorf("Expected exit code 1, got %d", result.ExitCode)
	}
}

func TestLocalExec_Run_EmptyCommand(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	opts := DefaultExecOpts()
	_, err := exec.Run(ctx, []string{}, &opts)
	if err == nil {
		t.Error("Expected error for empty command")
	}
}

func TestLocalExec_Run_WorkingDirectory(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	// Create a temporary directory.
	tempDir := t.TempDir()

	// Create a test file in the temp directory.
	testFile := filepath.Join(tempDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create test file: %v", err)
	}

	// Test command with working directory.
	opts := DefaultExecOpts()
	opts.WorkDir = tempDir

	result, err := exec.Run(ctx, []string{"ls", "test.txt"}, &opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Stdout, "test.txt") {
		t.Errorf("Expected stdout to contain 'test.txt', got %s", result.Stdout)
	}
}

func TestLocalExec_Run_NonExistentWorkingDirectory(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	opts := DefaultExecOpts()
	opts.WorkDir = "/nonexistent/directory"

	_, err := exec.Run(ctx, []string{"echo", "test"}, &opts)
	if err == nil {
		t.Error("Expected error for non-existent working directory")
	}

	if !strings.Contains(err.Error(), "working directory does not exist") {
		t.Errorf("Expected working directory error, got: %v", err)
	}
}

func TestLocalExec_Run_Environment(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	opts := DefaultExecOpts()
	opts.Env = []string{"TEST_VAR=hello world"}

	result, err := exec.Run(ctx, []string{"sh", "-c", "echo $TEST_VAR"}, &opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if strings.TrimSpace(result.Stdout) != "hello world" {
		t.Errorf("Expected stdout 'hello world', got %s", result.Stdout)
	}
}

func TestLocalExec_Run_Timeout(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	opts := DefaultExecOpts()
	opts.Timeout = 100 * time.Millisecond

	// Command that sleeps longer than timeout.
	result, err := exec.Run(ctx, []string{"sleep", "1"}, &opts)

	// For timeout, we expect either:
	// 1. An error is returned (context deadline exceeded)
	// 2. The command is killed and returns with exit code -1.
	// The exact behavior depends on timing and OS.

	timeoutOccurred := false

	if err != nil {
		// Check if it's a timeout-related error.
		if strings.Contains(err.Error(), "context deadline exceeded") ||
			strings.Contains(err.Error(), "signal: killed") {
			timeoutOccurred = true
		} else {
			t.Errorf("Expected timeout-related error, got: %v", err)
		}
	}

	// If no error, the command should have been killed (exit code -1)
	if !timeoutOccurred && result.ExitCode != -1 {
		t.Errorf("Expected timeout to occur (error or exit code -1), got exit code %d", result.ExitCode)
	}

	// Duration should be roughly the timeout duration.
	if result.Duration > 2*opts.Timeout {
		t.Errorf("Expected duration to be around %v, got %v", opts.Timeout, result.Duration)
	}
}

func TestLocalExec_Run_Stderr(t *testing.T) {
	exec := NewLocalExec()
	ctx := context.Background()

	// Command that writes to stderr.
	opts := DefaultExecOpts()
	result, err := exec.Run(ctx, []string{"sh", "-c", "echo 'error message' >&2"}, &opts)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if !strings.Contains(result.Stderr, "error message") {
		t.Errorf("Expected stderr to contain 'error message', got %s", result.Stderr)
	}
}

func TestDefaultExecOpts(t *testing.T) {
	opts := DefaultExecOpts()

	if opts.Timeout != 5*time.Minute {
		t.Errorf("Expected timeout 5m, got %v", opts.Timeout)
	}

	if opts.ReadOnly {
		t.Error("Expected ReadOnly to be false by default")
	}

	if opts.NetworkDisabled {
		t.Error("Expected NetworkDisabled to be false by default")
	}

	if opts.ResourceLimits == nil {
		t.Error("Expected ResourceLimits to be set")
	} else {
		if opts.ResourceLimits.CPUs != "2" {
			t.Errorf("Expected CPUs '2', got %s", opts.ResourceLimits.CPUs)
		}
		if opts.ResourceLimits.Memory != "2g" {
			t.Errorf("Expected Memory '2g', got %s", opts.ResourceLimits.Memory)
		}
		if opts.ResourceLimits.PIDs != 1024 {
			t.Errorf("Expected PIDs 1024, got %d", opts.ResourceLimits.PIDs)
		}
	}
}
