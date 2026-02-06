package demo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStack_Up_EmptyProjectName(t *testing.T) {
	s := &Stack{}

	err := s.Up(context.Background())
	if err == nil {
		t.Error("expected error for empty project name")
	}
}

func TestStack_Down_EmptyProjectName(t *testing.T) {
	s := &Stack{}

	err := s.Down(context.Background())
	if err == nil {
		t.Error("expected error for empty project name")
	}
}

func TestStack_Restart_EmptyProjectName(t *testing.T) {
	s := &Stack{}

	err := s.Restart(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty project name")
	}
}

func TestStack_Logs_EmptyProjectName(t *testing.T) {
	s := &Stack{}

	_, err := s.Logs(context.Background(), "")
	if err == nil {
		t.Error("expected error for empty project name")
	}
}

func TestStack_PS_EmptyProjectName(t *testing.T) {
	s := &Stack{}

	_, err := s.PS(context.Background())
	if err == nil {
		t.Error("expected error for empty project name")
	}
}

func TestStack_Up_Success(t *testing.T) {
	var capturedArgs []string
	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: "/path/to/compose.yml",
		CommandRunner: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	err := s.Up(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify args include project name and compose file
	argsStr := strings.Join(capturedArgs, " ")
	if !strings.Contains(argsStr, "-p test-project") {
		t.Errorf("expected -p test-project in args: %v", capturedArgs)
	}
	if !strings.Contains(argsStr, "-f /path/to/compose.yml") {
		t.Errorf("expected -f /path/to/compose.yml in args: %v", capturedArgs)
	}
	if !strings.Contains(argsStr, "up -d --wait") {
		t.Errorf("expected 'up -d --wait' in args: %v", capturedArgs)
	}
}

func TestStack_Down_Success(t *testing.T) {
	var capturedArgs []string
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	err := s.Down(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	argsStr := strings.Join(capturedArgs, " ")
	if !strings.Contains(argsStr, "down -v") {
		t.Errorf("expected 'down -v' in args: %v", capturedArgs)
	}
}

func TestStack_Down_NoConfigFile(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'no configuration file' >&2; exit 1")
		},
	}

	// Should not return error for non-existent config
	err := s.Down(context.Background())
	if err != nil {
		t.Errorf("expected no error for missing config, got: %v", err)
	}
}

func TestStack_Restart_AllServices(t *testing.T) {
	var capturedArgs []string
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	err := s.Restart(context.Background(), "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	argsStr := strings.Join(capturedArgs, " ")
	if !strings.Contains(argsStr, "restart") {
		t.Errorf("expected 'restart' in args: %v", capturedArgs)
	}
}

func TestStack_Restart_SpecificService(t *testing.T) {
	var capturedArgs []string
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, args ...string) *exec.Cmd {
			capturedArgs = args
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	err := s.Restart(context.Background(), "postgres")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	argsStr := strings.Join(capturedArgs, " ")
	if !strings.Contains(argsStr, "restart postgres") {
		t.Errorf("expected 'restart postgres' in args: %v", capturedArgs)
	}
}

func TestStack_Logs_Success(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'log line 1\nlog line 2'")
		},
	}

	reader, err := s.Logs(context.Background(), "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	buf := make([]byte, 1024)
	n, _ := reader.Read(buf)
	output := string(buf[:n])
	if !strings.Contains(output, "log line 1") {
		t.Errorf("expected log output, got: %s", output)
	}
}

func TestStack_IsRunning_True(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", `echo '{"Service":"app","State":"running","Health":"healthy","Publishers":[]}'`)
		},
	}

	running, err := s.IsRunning(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !running {
		t.Error("expected stack to be running")
	}
}

func TestStack_IsRunning_False(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", `echo '{"Service":"app","State":"exited","Health":"","Publishers":[]}'`)
		},
	}

	running, err := s.IsRunning(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if running {
		t.Error("expected stack to not be running")
	}
}

func TestStack_Exists_True(t *testing.T) {
	// Create a temp file
	tmpDir := t.TempDir()
	composePath := filepath.Join(tmpDir, "compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}"), 0644); err != nil {
		t.Fatal(err)
	}

	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: composePath,
	}

	if !s.Exists() {
		t.Error("expected Exists() to return true")
	}
}

func TestStack_Exists_False(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: "/nonexistent/compose.yml",
	}

	if s.Exists() {
		t.Error("expected Exists() to return false")
	}
}

func TestStack_Exists_EmptyPath(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
	}

	if s.Exists() {
		t.Error("expected Exists() to return false for empty path")
	}
}

func TestStack_ListServices(t *testing.T) {
	tmpDir := t.TempDir()
	composePath := filepath.Join(tmpDir, "compose.yml")

	composeContent := `services:
  app:
    image: myapp
  postgres:
    image: postgres:15
  redis:
    image: redis:7
`
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatal(err)
	}

	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: composePath,
	}

	services, err := s.ListServices()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if len(services) != 3 {
		t.Errorf("expected 3 services, got %d", len(services))
	}

	// Check all services are present
	serviceMap := make(map[string]bool)
	for _, svc := range services {
		serviceMap[svc] = true
	}
	for _, expected := range []string{"app", "postgres", "redis"} {
		if !serviceMap[expected] {
			t.Errorf("missing service: %s", expected)
		}
	}
}

func TestStack_ListServices_EmptyPath(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
	}

	_, err := s.ListServices()
	if err == nil {
		t.Error("expected error for empty compose file path")
	}
}

func TestStack_CountServices(t *testing.T) {
	tmpDir := t.TempDir()
	composePath := filepath.Join(tmpDir, "compose.yml")

	composeContent := `services:
  app:
    image: myapp
  postgres:
    image: postgres:15
`
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatal(err)
	}

	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: composePath,
	}

	count, err := s.CountServices()
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if count != 2 {
		t.Errorf("expected 2 services, got %d", count)
	}
}

func TestComposeFileExists_True(t *testing.T) {
	tmpDir := t.TempDir()
	maestroDir := filepath.Join(tmpDir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatal(err)
	}
	composePath := filepath.Join(maestroDir, "compose.yml")
	if err := os.WriteFile(composePath, []byte("services: {}"), 0644); err != nil {
		t.Fatal(err)
	}

	if !ComposeFileExists(tmpDir) {
		t.Error("expected ComposeFileExists to return true")
	}
}

func TestComposeFileExists_False(t *testing.T) {
	tmpDir := t.TempDir()

	if ComposeFileExists(tmpDir) {
		t.Error("expected ComposeFileExists to return false")
	}
}

func TestComposeFilePath(t *testing.T) {
	path := ComposeFilePath("/workspace/coder-001")
	expected := "/workspace/coder-001/.maestro/compose.yml"
	if path != expected {
		t.Errorf("ComposeFilePath = %q, want %q", path, expected)
	}
}

func TestNewStack(t *testing.T) {
	s := NewStack("my-project", "/path/compose.yml", "my-network")

	if s.ProjectName != "my-project" {
		t.Errorf("ProjectName = %q, want %q", s.ProjectName, "my-project")
	}
	if s.ComposeFile != "/path/compose.yml" {
		t.Errorf("ComposeFile = %q, want %q", s.ComposeFile, "/path/compose.yml")
	}
	if s.Network != "my-network" {
		t.Errorf("Network = %q, want %q", s.Network, "my-network")
	}
}

func TestStack_SanitizedComposeFile_StripsContainerName(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	composePath := filepath.Join(tmpDir, "compose.yml")

	// Write compose file with hardcoded container_name
	composeContent := `services:
  db:
    image: postgres:15-alpine
    container_name: helloworld-db
    environment:
      POSTGRES_USER: test
    ports:
      - "5432:5432"
  redis:
    image: redis:7-alpine
    container_name: helloworld-redis
`
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}

	s := NewStack("test-project", composePath, "test-network")

	// Call sanitizedComposeFile
	sanitizedPath, err := s.sanitizedComposeFile()
	if err != nil {
		t.Fatalf("sanitizedComposeFile failed: %v", err)
	}
	if sanitizedPath == "" {
		t.Fatal("expected sanitized file path, got empty string")
	}
	defer os.Remove(sanitizedPath)

	// Read sanitized file
	sanitizedContent, err := os.ReadFile(sanitizedPath)
	if err != nil {
		t.Fatalf("failed to read sanitized file: %v", err)
	}

	// Verify container_name is removed
	if strings.Contains(string(sanitizedContent), "container_name") {
		t.Errorf("sanitized file still contains container_name:\n%s", sanitizedContent)
	}

	// Verify other content is preserved
	if !strings.Contains(string(sanitizedContent), "postgres:15-alpine") {
		t.Error("sanitized file missing postgres image")
	}
	if !strings.Contains(string(sanitizedContent), "redis:7-alpine") {
		t.Error("sanitized file missing redis image")
	}
	if !strings.Contains(string(sanitizedContent), "POSTGRES_USER") {
		t.Error("sanitized file missing environment variable")
	}
}

func TestStack_SanitizedComposeFile_NoChangesNeeded(t *testing.T) {
	// Create temp directory
	tmpDir := t.TempDir()
	composePath := filepath.Join(tmpDir, "compose.yml")

	// Write compose file WITHOUT container_name
	composeContent := `services:
  db:
    image: postgres:15-alpine
    environment:
      POSTGRES_USER: test
`
	if err := os.WriteFile(composePath, []byte(composeContent), 0644); err != nil {
		t.Fatalf("failed to write compose file: %v", err)
	}

	s := NewStack("test-project", composePath, "test-network")

	// Call sanitizedComposeFile - should return empty string (no changes needed)
	sanitizedPath, err := s.sanitizedComposeFile()
	if err != nil {
		t.Fatalf("sanitizedComposeFile failed: %v", err)
	}
	if sanitizedPath != "" {
		os.Remove(sanitizedPath)
		t.Errorf("expected empty path when no changes needed, got %q", sanitizedPath)
	}
}

func TestStack_SanitizedComposeFile_EmptyComposeFile(t *testing.T) {
	s := NewStack("test-project", "", "test-network")

	// Call sanitizedComposeFile with empty path
	sanitizedPath, err := s.sanitizedComposeFile()
	if err != nil {
		t.Fatalf("sanitizedComposeFile failed: %v", err)
	}
	if sanitizedPath != "" {
		t.Errorf("expected empty path for empty compose file, got %q", sanitizedPath)
	}
}

func TestStack_UpAndAttach_EmptyContainerName(t *testing.T) {
	// With empty container name, UpAndAttach should just call Up() and return
	upCalled := false
	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: "/path/to/compose.yml",
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			upCalled = true
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		},
	}

	err := s.UpAndAttach(context.Background(), "")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if !upCalled {
		t.Error("expected Up() to be called")
	}
}

func TestStack_UpAndAttach_ConnectsContainerToNetwork(t *testing.T) {
	// Track commands to verify both compose up and network connect are called
	var commands []string
	mockRunner := func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		cmdStr := strings.Join(args, " ")
		commands = append(commands, cmdStr)

		// For network inspect (exists check), return success
		if len(args) >= 3 && args[0] == "network" && args[1] == "inspect" {
			// NetworkExists check - return success (network exists)
			if len(args) >= 4 && args[3] == "--format" {
				// IsConnected check - return empty (not connected yet)
				return exec.CommandContext(ctx, "sh", "-c", "echo ''")
			}
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		}

		// For network connect, return success
		if len(args) >= 3 && args[0] == "network" && args[1] == "connect" {
			return exec.CommandContext(ctx, "sh", "-c", "exit 0")
		}

		// For compose up, return success
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	s := &Stack{
		ProjectName:   "test-project",
		ComposeFile:   "/path/to/compose.yml",
		CommandRunner: mockRunner,
		networkManager: &NetworkManager{
			CommandRunner: mockRunner,
		},
	}

	err := s.UpAndAttach(context.Background(), "maestro-story-coder-001")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Verify compose up was called
	foundComposeUp := false
	foundNetworkConnect := false
	for _, cmd := range commands {
		if strings.Contains(cmd, "up -d --wait") {
			foundComposeUp = true
		}
		if strings.Contains(cmd, "network connect test-project_default maestro-story-coder-001") {
			foundNetworkConnect = true
		}
	}

	if !foundComposeUp {
		t.Errorf("expected compose up command, got commands: %v", commands)
	}
	if !foundNetworkConnect {
		t.Errorf("expected network connect command, got commands: %v", commands)
	}
}

func TestStack_UpAndAttach_NetworkNotFound(t *testing.T) {
	mockRunner := func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// For network inspect, return failure (network doesn't exist)
		if len(args) >= 3 && args[0] == "network" && args[1] == "inspect" {
			return exec.CommandContext(ctx, "sh", "-c", "exit 1")
		}
		// For compose up, return success
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	s := &Stack{
		ProjectName:   "test-project",
		ComposeFile:   "/path/to/compose.yml",
		CommandRunner: mockRunner,
		networkManager: &NetworkManager{
			CommandRunner: mockRunner,
		},
	}

	err := s.UpAndAttach(context.Background(), "maestro-story-coder-001")
	if err == nil {
		t.Fatal("expected error for missing network")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
	if !strings.Contains(err.Error(), "test-project_default") {
		t.Errorf("expected network name in error, got: %v", err)
	}
}

func TestStack_UpAndAttach_UpFails(t *testing.T) {
	s := &Stack{
		ProjectName: "test-project",
		ComposeFile: "/path/to/compose.yml",
		CommandRunner: func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'compose error' >&2; exit 1")
		},
	}

	err := s.UpAndAttach(context.Background(), "maestro-story-coder-001")
	if err == nil {
		t.Fatal("expected error when Up() fails")
	}
	if !strings.Contains(err.Error(), "compose up failed") {
		t.Errorf("expected compose up error, got: %v", err)
	}
}
