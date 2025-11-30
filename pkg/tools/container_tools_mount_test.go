package tools

import (
	"strings"
	"testing"

	"orchestrator/pkg/proto"
)

func TestContainerTestTool_GetWorkspaceMount(t *testing.T) {
	// Create tool directly without executor dependency for unit testing
	tool := &ContainerTestTool{}

	mount := tool.getWorkspaceMount()

	// Should return a mount string in format "host_path:/workspace:permissions"
	if mount == "" {
		t.Error("Expected workspace mount to be non-empty")
	}

	parts := strings.Split(mount, ":")
	if len(parts) != 3 {
		t.Errorf("Expected mount format 'host_path:/workspace:permissions', got '%s'", mount)
	}

	if parts[1] != "/workspace" {
		t.Errorf("Expected container path to be '/workspace', got '%s'", parts[1])
	}

	// Should be either "ro" or "rw"
	permissions := parts[2]
	if permissions != "ro" && permissions != "rw" {
		t.Errorf("Expected permissions to be 'ro' or 'rw', got '%s'", permissions)
	}
}

func TestContainerTestTool_GetWorkspacePermissions(t *testing.T) {
	tool := &ContainerTestTool{}

	permissions := tool.getWorkspacePermissions()

	// Should return either "ro" or "rw"
	if permissions != "ro" && permissions != "rw" {
		t.Errorf("Expected permissions to be 'ro' or 'rw', got '%s'", permissions)
	}

	// With current implementation, should default to "ro" (safer)
	if permissions != "ro" {
		t.Errorf("Expected default permissions to be 'ro' (read-only), got '%s'", permissions)
	}
}

func TestContainerTestTool_GetCurrentAgentState(t *testing.T) {
	tool := &ContainerTestTool{}

	state := tool.getCurrentAgentState()

	// With current placeholder implementation, should return empty state
	if state != "" {
		t.Errorf("Expected empty state from placeholder implementation, got '%s'", state)
	}
}

func TestContainerTestTool_BuildDockerArgs(t *testing.T) {
	tool := &ContainerTestTool{}

	args := map[string]any{
		"container_name": "test-container",
	}

	dockerArgs := tool.buildDockerArgs(args, "test-container", "echo hello", false)

	dockerCmd := strings.Join(dockerArgs, " ")

	// Verify automatic workspace mount is added
	if !strings.Contains(dockerCmd, "-v") {
		t.Error("Expected workspace mount (-v) to be automatically added")
	}

	// Verify tmpfs for /tmp is added
	if !strings.Contains(dockerCmd, "--tmpfs /tmp:") {
		t.Error("Expected tmpfs mount for /tmp to be automatically added")
	}

	// Verify default working directory is /workspace
	if !strings.Contains(dockerCmd, "-w /workspace") {
		t.Error("Expected working directory to default to /workspace")
	}

	// Verify container name and command are present
	if !strings.Contains(dockerCmd, "test-container") {
		t.Error("Expected container name to be present in docker command")
	}

	if !strings.Contains(dockerCmd, "echo hello") {
		t.Error("Expected command to be present in docker command")
	}
}

func TestContainerTestTool_WithAgentState(t *testing.T) {
	// Test with mock agent in CODING state
	mockAgent := &mockAgent{state: proto.State("CODING")}
	tool := NewContainerTestTool(nil, mockAgent, "/test/workdir")

	permissions := tool.getWorkspacePermissions()
	if permissions != "rw" {
		t.Errorf("Expected 'rw' permissions for CODING state, got '%s'", permissions)
	}

	// Test with mock agent in PLANNING state
	mockAgent.state = proto.State("PLANNING")
	permissions = tool.getWorkspacePermissions()
	if permissions != "ro" {
		t.Errorf("Expected 'ro' permissions for PLANNING state, got '%s'", permissions)
	}

	// Test with mock agent in TESTING state
	mockAgent.state = proto.State("TESTING")
	permissions = tool.getWorkspacePermissions()
	if permissions != "ro" {
		t.Errorf("Expected 'ro' permissions for TESTING state, got '%s'", permissions)
	}

	// Test with mock agent in unknown state
	mockAgent.state = proto.State("UNKNOWN")
	permissions = tool.getWorkspacePermissions()
	if permissions != "ro" {
		t.Errorf("Expected 'ro' permissions for unknown state, got '%s'", permissions)
	}
}

// mockAgent implements Agent interface for testing.
type mockAgent struct {
	state proto.State
}

func (m *mockAgent) GetCurrentState() proto.State {
	return m.state
}

func (m *mockAgent) GetHostWorkspacePath() string {
	return "/tmp/test-workspace"
}

func (m *mockAgent) CompleteTodo(_ int) bool {
	return true
}

func (m *mockAgent) UpdateTodo(_ int, _ string) bool {
	return true
}

func (m *mockAgent) UpdateTodoInState() {
	// No-op for mock
}

func (m *mockAgent) GetIncompleteTodoCount() int {
	return 0 // No incomplete todos in mock
}
