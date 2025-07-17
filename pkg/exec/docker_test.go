package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDockerExec_Name(t *testing.T) {
	docker := NewDockerExec("alpine:latest")
	if docker.Name() != "docker" {
		t.Errorf("Expected name 'docker', got '%s'", docker.Name())
	}
}

func TestDockerExec_Available(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	// This test depends on Docker being available
	// We can't easily mock this, so we'll just check the method exists
	available := docker.Available()
	t.Logf("Docker available: %v", available)

	// The test should not fail regardless of Docker availability
	// as this is an environment-dependent check
}

func TestDockerExec_buildDockerArgs(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	testCases := []struct {
		name          string
		containerName string
		cmd           []string
		opts          ExecOpts
		expectedParts []string
	}{
		{
			name:          "Basic command",
			containerName: "test-container",
			cmd:           []string{"echo", "hello"},
			opts:          DefaultExecOpts(),
			expectedParts: []string{"run", "--rm", "--name", "test-container", "alpine:latest", "echo", "hello"},
		},
		{
			name:          "With working directory",
			containerName: "test-container",
			cmd:           []string{"ls", "-la"},
			opts: ExecOpts{
				WorkDir: "/tmp",
				Timeout: 30 * time.Second,
			},
			expectedParts: []string{"--volume", "/tmp:/workspace:rw", "--workdir", "/workspace"},
		},
		{
			name:          "Read-only filesystem",
			containerName: "test-container",
			cmd:           []string{"cat", "file.txt"},
			opts: ExecOpts{
				WorkDir:  "/tmp",
				ReadOnly: true,
			},
			expectedParts: []string{"--volume", "/tmp:/workspace:ro"},
		},
		{
			name:          "Network disabled",
			containerName: "test-container",
			cmd:           []string{"ping", "google.com"},
			opts: ExecOpts{
				NetworkDisabled: true,
			},
			expectedParts: []string{"--network", "none"},
		},
		{
			name:          "Resource limits",
			containerName: "test-container",
			cmd:           []string{"stress", "--cpu", "1"},
			opts: ExecOpts{
				ResourceLimits: &ResourceLimits{
					CPUs:   "2",
					Memory: "1g",
					PIDs:   100,
				},
			},
			expectedParts: []string{"--cpus", "2", "--memory", "1g", "--pids-limit", "100"},
		},
		{
			name:          "Custom user",
			containerName: "test-container",
			cmd:           []string{"whoami"},
			opts: ExecOpts{
				User: "1000:1000",
			},
			expectedParts: []string{"--user", "1000:1000"},
		},
		{
			name:          "Environment variables",
			containerName: "test-container",
			cmd:           []string{"env"},
			opts: ExecOpts{
				Env: []string{"TEST_VAR=value1", "ANOTHER_VAR=value2"},
			},
			expectedParts: []string{"--env", "TEST_VAR=value1", "--env", "ANOTHER_VAR=value2"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			args, err := docker.buildDockerArgs(tc.containerName, tc.cmd, tc.opts)
			if err != nil {
				t.Fatalf("buildDockerArgs failed: %v", err)
			}

			argsStr := strings.Join(args, " ")
			for _, expected := range tc.expectedParts {
				if !strings.Contains(argsStr, expected) {
					t.Errorf("Expected args to contain '%s', got: %s", expected, argsStr)
				}
			}
		})
	}
}

func TestDockerExec_normalizePath(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Unix path",
			input:    "/home/user/project",
			expected: "/home/user/project",
		},
		{
			name:     "Relative path",
			input:    "./project",
			expected: "./project",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := docker.normalizePath(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %s, got %s", tc.expected, result)
			}
		})
	}
}

func TestDockerExec_generateContainerName(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	name1 := docker.generateContainerName()
	// Add a small delay to ensure different timestamps
	time.Sleep(1 * time.Millisecond)
	name2 := docker.generateContainerName()

	if name1 == name2 {
		t.Errorf("Expected unique container names, got: %s and %s", name1, name2)
	}

	if !strings.HasPrefix(name1, "maestro-exec-") {
		t.Errorf("Expected container name to start with 'maestro-exec-', got: %s", name1)
	}
}

// Integration tests - these require Docker to be available
func TestDockerExec_Run_Integration(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	// Skip if Docker is not available
	if !docker.Available() {
		t.Skip("Docker not available, skipping integration test")
	}

	t.Run("Simple command", func(t *testing.T) {
		ctx := context.Background()
		result, err := docker.Run(ctx, []string{"echo", "hello world"}, DefaultExecOpts())

		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got: %d", result.ExitCode)
		}

		if !strings.Contains(result.Stdout, "hello world") {
			t.Errorf("Expected stdout to contain 'hello world', got: %s", result.Stdout)
		}

		if result.ExecutorUsed != "docker" {
			t.Errorf("Expected executor 'docker', got: %s", result.ExecutorUsed)
		}
	})

	t.Run("Command with working directory", func(t *testing.T) {
		// Create temporary directory
		tempDir := t.TempDir()
		testFile := filepath.Join(tempDir, "test.txt")

		err := os.WriteFile(testFile, []byte("test content"), 0644)
		if err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		ctx := context.Background()
		opts := ExecOpts{
			WorkDir: tempDir,
			Timeout: 30 * time.Second,
		}

		result, err := docker.Run(ctx, []string{"cat", "test.txt"}, opts)

		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got: %d", result.ExitCode)
		}

		if !strings.Contains(result.Stdout, "test content") {
			t.Errorf("Expected stdout to contain 'test content', got: %s", result.Stdout)
		}
	})

	t.Run("Command with timeout", func(t *testing.T) {
		ctx := context.Background()
		opts := ExecOpts{
			Timeout: 1 * time.Second,
		}

		result, err := docker.Run(ctx, []string{"sleep", "5"}, opts)

		if err == nil {
			t.Error("Expected timeout error")
		}

		if result.Duration < 900*time.Millisecond || result.Duration > 2*time.Second {
			t.Errorf("Expected duration around 1 second, got: %v", result.Duration)
		}
	})

	t.Run("Command with environment variables", func(t *testing.T) {
		ctx := context.Background()
		opts := ExecOpts{
			Env: []string{"TEST_VAR=test_value"},
		}

		result, err := docker.Run(ctx, []string{"sh", "-c", "echo $TEST_VAR"}, opts)

		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got: %d", result.ExitCode)
		}

		if !strings.Contains(result.Stdout, "test_value") {
			t.Errorf("Expected stdout to contain 'test_value', got: %s", result.Stdout)
		}
	})

	t.Run("Command that fails", func(t *testing.T) {
		ctx := context.Background()
		result, err := docker.Run(ctx, []string{"sh", "-c", "exit 42"}, DefaultExecOpts())

		if err == nil {
			t.Error("Expected error for failing command")
		}

		if result.ExitCode != 42 {
			t.Errorf("Expected exit code 42, got: %d", result.ExitCode)
		}
	})

	t.Run("Network disabled", func(t *testing.T) {
		ctx := context.Background()
		opts := ExecOpts{
			NetworkDisabled: true,
			Timeout:         10 * time.Second,
		}

		// This should fail because network is disabled
		result, err := docker.Run(ctx, []string{"ping", "-c", "1", "8.8.8.8"}, opts)

		if err == nil {
			t.Error("Expected error when network is disabled")
		}

		if result.ExitCode == 0 {
			t.Error("Expected non-zero exit code when network is disabled")
		}
	})
}

func TestDockerExec_Run_ErrorCases(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	t.Run("Empty command", func(t *testing.T) {
		ctx := context.Background()
		_, err := docker.Run(ctx, []string{}, DefaultExecOpts())

		if err == nil {
			t.Error("Expected error for empty command")
		}

		if !strings.Contains(err.Error(), "command cannot be empty") {
			t.Errorf("Expected 'command cannot be empty' error, got: %v", err)
		}
	})

	t.Run("Invalid working directory", func(t *testing.T) {
		ctx := context.Background()
		opts := ExecOpts{
			WorkDir: "/nonexistent/directory",
		}

		_, err := docker.Run(ctx, []string{"echo", "test"}, opts)

		if err == nil {
			t.Error("Expected error for invalid working directory")
		}
	})
}

func TestDockerExec_Shutdown(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := docker.Shutdown(ctx)
	if err != nil {
		t.Errorf("Expected no error during shutdown, got: %v", err)
	}
}

func TestDockerExec_GetSetImage(t *testing.T) {
	docker := NewDockerExec("alpine:latest")

	if docker.GetImage() != "alpine:latest" {
		t.Errorf("Expected image 'alpine:latest', got: %s", docker.GetImage())
	}

	docker.SetImage("ubuntu:20.04")
	if docker.GetImage() != "ubuntu:20.04" {
		t.Errorf("Expected image 'ubuntu:20.04', got: %s", docker.GetImage())
	}
}

// Test with Docker unavailable scenarios
func TestDockerExec_DockerUnavailable(t *testing.T) {
	// Create a DockerExec with a non-existent docker command
	docker := &DockerExec{
		dockerCmd: "nonexistent-docker-command",
		image:     "alpine:latest",
	}

	if docker.Available() {
		t.Error("Expected Docker to be unavailable with non-existent command")
	}
}

// Test Story 072 acceptance criteria
func TestStory072AcceptanceCriteria(t *testing.T) {
	docker := NewDockerExec("golang:1.24-alpine")

	// Skip if Docker is not available
	if !docker.Available() {
		t.Skip("Docker not available, skipping Story 072 acceptance test")
	}

	t.Run("Runs go test in container with worktree bind mount", func(t *testing.T) {
		ctx := context.Background()

		// Get current working directory (should be maestro root)
		cwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("Failed to get current working directory: %v", err)
		}

		// Find maestro root by looking for go.mod
		maestroRoot := cwd
		for {
			if _, err := os.Stat(filepath.Join(maestroRoot, "go.mod")); err == nil {
				break
			}
			parent := filepath.Dir(maestroRoot)
			if parent == maestroRoot {
				t.Skip("Could not find maestro root with go.mod")
				return
			}
			maestroRoot = parent
		}

		// Test with read-only bind mount and writable /tmp tmpfs
		opts := ExecOpts{
			WorkDir:  maestroRoot,
			ReadOnly: true,
			Timeout:  60 * time.Second,
		}

		// Run a simple go test to verify container execution
		result, err := docker.Run(ctx, []string{"go", "test", "./pkg/exec", "-run", "TestLocalExec_Name"}, opts)

		if err != nil {
			t.Fatalf("Docker execution failed: %v\nStdout: %s\nStderr: %s", err, result.Stdout, result.Stderr)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got %d", result.ExitCode)
		}

		if result.ExecutorUsed != "docker" {
			t.Errorf("Expected executor 'docker', got '%s'", result.ExecutorUsed)
		}

		// Verify test actually ran and passed
		if !strings.Contains(result.Stdout, "ok") && !strings.Contains(result.Stdout, "PASS") {
			t.Errorf("Expected test to pass, stdout: %s", result.Stdout)
		}
	})

	t.Run("Honors ExecOpts.Timeout", func(t *testing.T) {
		ctx := context.Background()

		opts := ExecOpts{
			Timeout: 2 * time.Second,
		}

		start := time.Now()
		result, err := docker.Run(ctx, []string{"sleep", "10"}, opts)
		duration := time.Since(start)

		if err == nil {
			t.Error("Expected timeout error")
		}

		// Should timeout around 2 seconds, allow more variance for Docker overhead
		if duration < 1500*time.Millisecond || duration > 15*time.Second {
			t.Errorf("Expected timeout around 2s (with Docker overhead), got %v", duration)
		}

		if result.ExecutorUsed != "docker" {
			t.Errorf("Expected executor 'docker', got '%s'", result.ExecutorUsed)
		}
	})

	t.Run("Returns ExitCode and logs", func(t *testing.T) {
		ctx := context.Background()

		// Test successful command
		result, err := docker.Run(ctx, []string{"echo", "test output"}, DefaultExecOpts())

		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		if result.ExitCode != 0 {
			t.Errorf("Expected exit code 0, got %d", result.ExitCode)
		}

		if !strings.Contains(result.Stdout, "test output") {
			t.Errorf("Expected stdout to contain 'test output', got: %s", result.Stdout)
		}

		// Test failing command
		result, err = docker.Run(ctx, []string{"sh", "-c", "echo 'error message' >&2; exit 42"}, DefaultExecOpts())

		if err == nil {
			t.Error("Expected error for failing command")
		}

		if result.ExitCode != 42 {
			t.Errorf("Expected exit code 42, got %d", result.ExitCode)
		}

		if !strings.Contains(result.Stderr, "error message") {
			t.Errorf("Expected stderr to contain 'error message', got: %s", result.Stderr)
		}
	})

	t.Run("Fallback to LocalExec when Docker unavailable", func(t *testing.T) {
		// Create a new registry for this test
		testRegistry := NewRegistry()

		// Register LocalExec
		err := testRegistry.Register(NewLocalExec())
		if err != nil {
			t.Fatalf("Failed to register LocalExec: %v", err)
		}

		// Create a DockerExec with non-existent docker command
		brokenDocker := &DockerExec{
			dockerCmd: "nonexistent-docker-command",
			image:     "alpine:latest",
		}

		if brokenDocker.Available() {
			t.Error("Expected broken docker to be unavailable")
		}

		// Register the broken docker
		err = testRegistry.Register(brokenDocker)
		if err != nil {
			t.Fatalf("Failed to register broken docker: %v", err)
		}

		// Test that registry can fall back to LocalExec
		preferences := []string{"docker", "local"}
		bestExec, err := testRegistry.GetBest(preferences)

		if err != nil {
			t.Fatalf("Expected to get fallback executor: %v", err)
		}

		if bestExec.Name() != "local" {
			t.Errorf("Expected fallback to 'local', got '%s'", bestExec.Name())
		}
	})
}

// Benchmark test
func BenchmarkDockerExec_Run(b *testing.B) {
	docker := NewDockerExec("alpine:latest")

	if !docker.Available() {
		b.Skip("Docker not available, skipping benchmark")
	}

	ctx := context.Background()
	opts := DefaultExecOpts()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := docker.Run(ctx, []string{"echo", "benchmark"}, opts)
		if err != nil {
			b.Fatalf("Benchmark failed: %v", err)
		}
	}
}
