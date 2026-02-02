package demo

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"orchestrator/internal/state"
	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
)

func newTestService(t *testing.T) (*Service, string) {
	t.Helper()

	logger := logx.NewLogger("test")
	cfg := &config.Config{}
	registry := state.NewComposeRegistry()

	svc := NewService(cfg, logger, registry)

	// Create temp workspace
	tmpDir := t.TempDir()

	return svc, tmpDir
}

//nolint:unparam // return value useful for potential future tests
func createComposeFile(t *testing.T, dir string) string {
	t.Helper()

	maestroDir := filepath.Join(dir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatal(err)
	}

	composePath := filepath.Join(maestroDir, "compose.yml")
	// Include maestro.app label so tests use compose-only mode (not hybrid)
	content := `services:
  demo:
    image: nginx
    ports:
      - "8081:80"
    labels:
      maestro.app: "true"
`
	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	return composePath
}

// createComposeFileWithoutApp creates a compose file with only support services (db),
// which triggers hybrid mode (compose for deps, app runs separately).
//
//nolint:unparam // return value useful for potential future tests
func createComposeFileWithoutApp(t *testing.T, dir string) string {
	t.Helper()

	maestroDir := filepath.Join(dir, ".maestro")
	if err := os.MkdirAll(maestroDir, 0755); err != nil {
		t.Fatal(err)
	}

	composePath := filepath.Join(maestroDir, "compose.yml")
	content := `services:
  db:
    image: postgres:15-alpine
    ports:
      - "5432:5432"
`
	if err := os.WriteFile(composePath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	return composePath
}

func TestNewService(t *testing.T) {
	svc, _ := newTestService(t)

	if svc == nil {
		t.Fatal("expected non-nil service")
	}
	if svc.port != DefaultDemoPort {
		t.Errorf("port = %d, want %d", svc.port, DefaultDemoPort)
	}
}

func TestService_SetWorkspacePath(t *testing.T) {
	svc, tmpDir := newTestService(t)

	svc.SetWorkspacePath(tmpDir)

	if svc.workspacePath != tmpDir {
		t.Errorf("workspacePath = %q, want %q", svc.workspacePath, tmpDir)
	}
}

func TestService_Start_NoWorkspacePath(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.Start(context.Background())
	if err == nil {
		t.Error("expected error for missing workspace path")
	}
}

func TestService_Start_NoComposeFile(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)

	// Mock network creation to succeed
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	err := svc.Start(context.Background())
	if err == nil {
		t.Error("expected error for missing compose file")
	}
}

func TestService_Start_Success(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands to succeed
	svc.commandRunner = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// For git rev-parse, return a fake SHA
		if len(args) >= 2 && args[len(args)-1] == "HEAD" {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'abc123'")
		}
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	err := svc.Start(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !svc.IsRunning() {
		t.Error("expected service to be running")
	}

	// Check registry was updated
	stack := svc.composeRegistry.Get(DemoProjectName)
	if stack == nil {
		t.Error("expected stack to be registered")
	}
}

func TestService_Start_AlreadyRunning(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands to succeed
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start first time
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("first start failed: %v", err)
	}

	// Try to start again
	err := svc.Start(context.Background())
	if err == nil {
		t.Error("expected error for already running")
	}
}

func TestService_Stop_NotRunning(t *testing.T) {
	svc, _ := newTestService(t)

	// Should not error when not running
	err := svc.Stop(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_Stop_Success(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Stop
	err := svc.Stop(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if svc.IsRunning() {
		t.Error("expected service to not be running")
	}

	// Check registry was cleared
	stack := svc.composeRegistry.Get(DemoProjectName)
	if stack != nil {
		t.Error("expected stack to be unregistered")
	}
}

func TestService_Restart_NotRunning(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.Restart(context.Background())
	if err == nil {
		t.Error("expected error for not running")
	}
}

func TestService_Restart_Success(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Restart
	err := svc.Restart(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if !svc.IsRunning() {
		t.Error("expected service to still be running")
	}
}

func TestService_Status_NotRunning(t *testing.T) {
	svc, _ := newTestService(t)

	status := svc.Status(context.Background())

	if status.Running {
		t.Error("expected Running = false")
	}
	if status.URL != "" {
		t.Error("expected empty URL when not running")
	}
}

func TestService_Status_Running(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands
	svc.commandRunner = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		// For git rev-parse
		if len(args) >= 2 && args[len(args)-1] == "HEAD" {
			return exec.CommandContext(ctx, "sh", "-c", "echo 'abc123'")
		}
		// For docker compose ps
		if len(args) > 0 && args[len(args)-1] == "json" {
			return exec.CommandContext(ctx, "sh", "-c", `echo '{"Service":"demo","State":"running","Health":"healthy","Publishers":[]}'`)
		}
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	status := svc.Status(context.Background())

	if !status.Running {
		t.Error("expected Running = true")
	}
	if status.URL == "" {
		t.Error("expected non-empty URL")
	}
	if status.Port != DefaultDemoPort {
		t.Errorf("Port = %d, want %d", status.Port, DefaultDemoPort)
	}
	if status.StartedAt == nil {
		t.Error("expected non-nil StartedAt")
	}
}

func TestService_Status_Outdated(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	callCount := 0
	// Mock commands - return different SHA on second call
	svc.commandRunner = func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		if len(args) >= 2 && args[len(args)-1] == "HEAD" {
			callCount++
			if callCount == 1 {
				return exec.CommandContext(ctx, "sh", "-c", "echo 'abc123'")
			}
			return exec.CommandContext(ctx, "sh", "-c", "echo 'def456'")
		}
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start (captures first SHA)
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Get status (gets second SHA)
	status := svc.Status(context.Background())

	if !status.Outdated {
		t.Error("expected Outdated = true")
	}
	if status.BuiltFromSHA != "abc123" {
		t.Errorf("BuiltFromSHA = %q, want %q", status.BuiltFromSHA, "abc123")
	}
	if status.CurrentSHA != "def456" {
		t.Errorf("CurrentSHA = %q, want %q", status.CurrentSHA, "def456")
	}
}

func TestService_ConnectPM_NotRunning(t *testing.T) {
	svc, _ := newTestService(t)

	err := svc.ConnectPM(context.Background(), "pm-container")
	if err == nil {
		t.Error("expected error when demo not running")
	}
}

func TestService_ConnectPM_Success(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Connect PM
	err := svc.ConnectPM(context.Background(), "pm-container")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_DisconnectPM(t *testing.T) {
	svc, _ := newTestService(t)

	// Mock network manager
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Should not error even when not connected
	err := svc.DisconnectPM(context.Background(), "pm-container")
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestService_GetLogs_NoContainer(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)

	// Mock docker logs to simulate no container running
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'Error: No such container' >&2; exit 1")
	}

	// Without compose file, GetLogs falls back to docker logs.
	// With no running container, docker logs will fail.
	_, err := svc.GetLogs(context.Background())
	if err == nil {
		t.Error("expected error when no container is running")
	}
}

func TestService_GetLogs_Success(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock log output
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'log line 1\nlog line 2'")
	}

	logs, err := svc.GetLogs(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if logs == "" {
		t.Error("expected non-empty logs")
	}
}

func TestService_Cleanup(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)
	createComposeFile(t, tmpDir)

	// Mock all commands
	svc.commandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "echo 'ok'")
	}
	svc.networkManager.CommandRunner = func(ctx context.Context, _ string, _ ...string) *exec.Cmd {
		return exec.CommandContext(ctx, "sh", "-c", "exit 0")
	}

	// Start
	if err := svc.Start(context.Background()); err != nil {
		t.Fatalf("start failed: %v", err)
	}

	// Cleanup
	err := svc.Cleanup(context.Background())
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if svc.IsRunning() {
		t.Error("expected service to be stopped after cleanup")
	}
}

// TestService_HybridComposeMode_DetectsMode tests that hybrid mode is correctly
// detected when compose file has no maestro.app label.
// Note: This only tests mode detection, not the full start flow (which requires Docker).
func TestService_HybridComposeMode_DetectsMode(t *testing.T) {
	svc, tmpDir := newTestService(t)
	svc.SetWorkspacePath(tmpDir)

	// Create compose file WITHOUT maestro.app label
	createComposeFileWithoutApp(t, tmpDir)
	composePath := filepath.Join(tmpDir, ".maestro", "compose.yml")

	// Should detect NO app service (triggers hybrid mode)
	hasApp, _ := svc.checkComposeAppService(composePath)
	if hasApp {
		t.Error("expected no app service in compose file without maestro.app label")
	}

	// Now test with app label
	createComposeFile(t, tmpDir) // This one HAS maestro.app label
	hasApp, port := svc.checkComposeAppService(composePath)
	if !hasApp {
		t.Error("expected app service with maestro.app label")
	}
	if port != 8081 {
		t.Errorf("expected port 8081, got %d", port)
	}
}

// TestService_CheckComposeAppService tests the maestro.app label detection.
func TestService_CheckComposeAppService(t *testing.T) {
	svc, tmpDir := newTestService(t)

	// Create compose file WITH maestro.app label
	createComposeFile(t, tmpDir)
	composePath := filepath.Join(tmpDir, ".maestro", "compose.yml")

	hasApp, port := svc.checkComposeAppService(composePath)
	if !hasApp {
		t.Error("expected to detect app service with maestro.app label")
	}
	if port != 8081 {
		t.Errorf("expected port 8081, got %d", port)
	}
}

// TestService_CheckComposeAppService_NoLabel tests detection with no label.
func TestService_CheckComposeAppService_NoLabel(t *testing.T) {
	svc, tmpDir := newTestService(t)

	// Create compose file WITHOUT maestro.app label
	createComposeFileWithoutApp(t, tmpDir)
	composePath := filepath.Join(tmpDir, ".maestro", "compose.yml")

	hasApp, _ := svc.checkComposeAppService(composePath)
	if hasApp {
		t.Error("expected no app service without maestro.app label")
	}
}
