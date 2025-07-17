package exec

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDockerExec_DIND tests DockerExec using Docker-in-Docker for more comprehensive testing
func TestDockerExec_DIND(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping DIND test in short mode")
	}

	// Check if Docker is available
	exec := NewDockerExec("docker:24-dind")
	if !exec.Available() {
		t.Skip("Docker not available, skipping DIND test")
	}

	// Create a temporary directory for the test
	tempDir, err := os.MkdirTemp("", "docker-dind-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a simple test script
	scriptPath := filepath.Join(tempDir, "test.sh")
	script := `#!/bin/sh
echo "Running in Docker-in-Docker"
docker --version
echo "Exit code: $?"
`
	if err := os.WriteFile(scriptPath, []byte(script), 0755); err != nil {
		t.Fatalf("Failed to write test script: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	opts := ExecOpts{
		WorkDir: tempDir,
		Timeout: 45 * time.Second,
	}

	// Run the test script in DIND
	result, err := exec.Run(ctx, []string{"sh", "./test.sh"}, opts)
	if err != nil {
		t.Fatalf("Failed to run DIND test: %v", err)
	}

	// Verify the result
	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d", result.ExitCode)
	}

	if result.Stdout == "" {
		t.Error("Expected non-empty stdout")
	}

	t.Logf("DIND test output: %s", result.Stdout)
}

// TestDockerExec_WorktreeCompatibility tests Docker bind mount compatibility with git worktrees
func TestDockerExec_WorktreeCompatibility(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping worktree compatibility test in short mode")
	}

	exec := NewDockerExec("golang:1.24-alpine")
	if !exec.Available() {
		t.Skip("Docker not available, skipping worktree compatibility test")
	}

	// Create a temporary directory to simulate a worktree
	tempDir, err := os.MkdirTemp("", "worktree-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create a simple Go project structure
	if err := os.MkdirAll(filepath.Join(tempDir, "pkg", "test"), 0755); err != nil {
		t.Fatalf("Failed to create pkg directory: %v", err)
	}

	// Create go.mod
	goMod := `module test
go 1.24
`
	if err := os.WriteFile(filepath.Join(tempDir, "go.mod"), []byte(goMod), 0644); err != nil {
		t.Fatalf("Failed to write go.mod: %v", err)
	}

	// Create a simple test file
	testFile := `package test

import "testing"

func TestWorktreeCompatibility(t *testing.T) {
	t.Log("Running test in Docker with worktree bind mount")
}
`
	if err := os.WriteFile(filepath.Join(tempDir, "pkg", "test", "test_test.go"), []byte(testFile), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := ExecOpts{
		WorkDir: tempDir,
		Timeout: 25 * time.Second,
	}

	// Run go test to verify worktree compatibility
	result, err := exec.Run(ctx, []string{"go", "test", "./pkg/test/..."}, opts)
	if err != nil {
		t.Fatalf("Failed to run go test in worktree: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Expected exit code 0, got %d. Stdout: %s, Stderr: %s", result.ExitCode, result.Stdout, result.Stderr)
	}

	// Verify that the test ran successfully (look for "ok" which indicates success)
	if !stringContains(result.Stdout, "ok") {
		t.Errorf("Expected 'ok' in output, got: %s", result.Stdout)
	}

	t.Logf("Worktree compatibility test passed: %s", result.Stdout)
}

// TestDockerExec_ArchitectCodeReview tests that architect can review code via file diff
func TestDockerExec_ArchitectCodeReview(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping architect code review test in short mode")
	}

	exec := NewDockerExec("golang:1.24-alpine")
	if !exec.Available() {
		t.Skip("Docker not available, skipping architect code review test")
	}

	// Create a temporary directory to simulate a workspace
	tempDir, err := os.MkdirTemp("", "code-review-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	opts := ExecOpts{
		WorkDir: tempDir,
		Timeout: 25 * time.Second,
	}

	// Create original file
	originalFile := `package main

import "fmt"

func main() {
	fmt.Println("Hello, World!")
}
`
	if err := os.WriteFile(filepath.Join(tempDir, "main.go"), []byte(originalFile), 0644); err != nil {
		t.Fatalf("Failed to write original main.go: %v", err)
	}

	// Create modified file
	modifiedFile := `package main

import "fmt"

func main() {
	fmt.Println("Hello, Docker World!")
	fmt.Println("This is a modification")
}
`
	if err := os.WriteFile(filepath.Join(tempDir, "main_modified.go"), []byte(modifiedFile), 0644); err != nil {
		t.Fatalf("Failed to write modified main.go: %v", err)
	}

	// Test that architect can review code via diff command
	result, err := exec.Run(ctx, []string{"diff", "-u", "main.go", "main_modified.go"}, opts)
	// diff command fails with exit code 1 when files differ, which is expected
	if err != nil && result.ExitCode != 1 {
		t.Fatalf("Failed to run diff: %v", err)
	}

	// diff returns exit code 1 when files differ, which is expected
	if result.ExitCode != 1 {
		t.Errorf("Expected diff exit code 1 (files differ), got %d", result.ExitCode)
	}

	// Verify that the diff contains the expected changes
	if !stringContains(result.Stdout, "Hello, Docker World!") {
		t.Errorf("Expected diff to contain 'Hello, Docker World!', got: %s", result.Stdout)
	}

	if !stringContains(result.Stdout, "This is a modification") {
		t.Errorf("Expected diff to contain 'This is a modification', got: %s", result.Stdout)
	}

	// Test that architect can read files for code review
	result, err = exec.Run(ctx, []string{"cat", "main.go"}, opts)
	if err != nil {
		t.Fatalf("Failed to read file: %v", err)
	}

	if result.ExitCode != 0 {
		t.Errorf("Cat command failed with exit code %d: %s", result.ExitCode, result.Stderr)
	}

	if !stringContains(result.Stdout, "Hello, World!") {
		t.Errorf("Expected to read original file content, got: %s", result.Stdout)
	}

	t.Logf("Architect code review test passed - file diff and reading works correctly")
}

// TestDockerExec_MultiAgentStress tests concurrent Docker executor usage
func TestDockerExec_MultiAgentStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping multi-agent stress test in short mode")
	}

	exec := NewDockerExec("golang:1.24-alpine")
	if !exec.Available() {
		t.Skip("Docker not available, skipping multi-agent stress test")
	}

	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "multi-agent-stress-test")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Number of concurrent "agents"
	numAgents := 5

	// Channel to collect results
	results := make(chan error, numAgents)

	// Launch concurrent executions
	for i := 0; i < numAgents; i++ {
		go func(agentID int) {
			opts := ExecOpts{
				WorkDir: tempDir,
				Timeout: 45 * time.Second,
			}

			// Each agent runs a different command
			cmd := []string{"sh", "-c", fmt.Sprintf("echo 'Agent %d running' && sleep 1 && echo 'Agent %d done'", agentID, agentID)}

			result, err := exec.Run(ctx, cmd, opts)
			if err != nil {
				results <- fmt.Errorf("Agent %d failed: %v", agentID, err)
				return
			}

			if result.ExitCode != 0 {
				results <- fmt.Errorf("Agent %d exited with code %d", agentID, result.ExitCode)
				return
			}

			if !stringContains(result.Stdout, fmt.Sprintf("Agent %d running", agentID)) {
				results <- fmt.Errorf("Agent %d output missing expected text", agentID)
				return
			}

			results <- nil
		}(i)
	}

	// Wait for all agents to complete
	for i := 0; i < numAgents; i++ {
		select {
		case err := <-results:
			if err != nil {
				t.Errorf("Multi-agent stress test failed: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("Multi-agent stress test timed out")
		}
	}

	t.Logf("Multi-agent stress test passed with %d concurrent agents", numAgents)
}

// TestDockerExec_PerformanceBenchmark benchmarks Docker vs Local execution
func TestDockerExec_PerformanceBenchmark(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance benchmark in short mode")
	}

	dockerExec := NewDockerExec("golang:1.24-alpine")
	localExec := NewLocalExec()

	if !dockerExec.Available() {
		t.Skip("Docker not available, skipping performance benchmark")
	}

	// Create a temporary directory
	tempDir, err := os.MkdirTemp("", "performance-benchmark")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()

	opts := ExecOpts{
		WorkDir: tempDir,
		Timeout: 30 * time.Second,
	}

	// Simple command to benchmark
	cmd := []string{"echo", "Hello, World!"}

	// Benchmark Docker execution
	dockerStart := time.Now()
	_, err = dockerExec.Run(ctx, cmd, opts)
	dockerDuration := time.Since(dockerStart)
	if err != nil {
		t.Fatalf("Docker execution failed: %v", err)
	}

	// Benchmark Local execution
	localStart := time.Now()
	_, err = localExec.Run(ctx, cmd, opts)
	localDuration := time.Since(localStart)
	if err != nil {
		t.Fatalf("Local execution failed: %v", err)
	}

	// Log performance comparison
	t.Logf("Performance Benchmark:")
	t.Logf("  Docker execution: %v", dockerDuration)
	t.Logf("  Local execution:  %v", localDuration)
	t.Logf("  Overhead ratio:   %.2fx", float64(dockerDuration)/float64(localDuration))

	// Verify Docker isn't dramatically slower (allow up to 100x overhead for container startup)
	if dockerDuration > 100*localDuration {
		t.Errorf("Docker execution is significantly slower: %v vs %v", dockerDuration, localDuration)
	}
}

// Helper function to check if a string contains a substring
func stringContains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(substr) > 0 && stringContainsHelper(s, substr)))
}

func stringContainsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
