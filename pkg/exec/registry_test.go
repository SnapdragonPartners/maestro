package exec

import (
	"context"
	"testing"
	"time"
)

// Mock executor for testing.
type mockExecutor struct {
	name      string
	available bool
}

func (m *mockExecutor) Name() ExecutorType {
	return ExecutorType(m.name)
}

func (m *mockExecutor) Available() bool {
	return m.available
}

func (m *mockExecutor) Run(_ context.Context, _ []string, _ *ExecOpts) (ExecResult, error) {
	return ExecResult{
		ExitCode:     0,
		Stdout:       "mock output",
		Stderr:       "",
		Duration:     time.Millisecond,
		ExecutorUsed: m.name,
	}, nil
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()

	mock := &mockExecutor{name: "test", available: true}

	err := registry.Register(mock)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Test getting the registered executor.
	executor, err := registry.Get("test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(executor.Name()) != "test" {
		t.Errorf("Expected name 'test', got %s", string(executor.Name()))
	}
}

func TestRegistry_Register_NilExecutor(t *testing.T) {
	registry := NewRegistry()

	err := registry.Register(nil)
	if err == nil {
		t.Error("Expected error for nil executor")
	}
}

func TestRegistry_Register_EmptyName(t *testing.T) {
	registry := NewRegistry()

	mock := &mockExecutor{name: "", available: true}

	err := registry.Register(mock)
	if err == nil {
		t.Error("Expected error for empty name")
	}
}

func TestRegistry_Get_NotFound(t *testing.T) {
	registry := NewRegistry()

	_, err := registry.Get("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent executor")
	}
}

func TestRegistry_GetDefault(t *testing.T) {
	registry := NewRegistry()

	// Register local executor first.
	localExec := NewLocalExec()
	registry.Register(localExec)

	// Should have local executor by default.
	executor, err := registry.GetDefault()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(executor.Name()) != "local" {
		t.Errorf("Expected default executor 'local', got %s", string(executor.Name()))
	}
}

func TestRegistry_SetDefault(t *testing.T) {
	registry := NewRegistry()

	mock := &mockExecutor{name: "test", available: true}
	registry.Register(mock)

	err := registry.SetDefault("test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	executor, err := registry.GetDefault()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(executor.Name()) != "test" {
		t.Errorf("Expected default executor 'test', got %s", string(executor.Name()))
	}
}

func TestRegistry_SetDefault_NotFound(t *testing.T) {
	registry := NewRegistry()

	err := registry.SetDefault("nonexistent")
	if err == nil {
		t.Error("Expected error for non-existent executor")
	}
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()

	// Register local executor first.
	localExec := NewLocalExec()
	registry.Register(localExec)

	mock1 := &mockExecutor{name: "test1", available: true}
	mock2 := &mockExecutor{name: "test2", available: true}

	registry.Register(mock1)
	registry.Register(mock2)

	names := registry.List()

	// Should have local, test1, test2.
	if len(names) != 3 {
		t.Errorf("Expected 3 executors, got %d", len(names))
	}

	expectedNames := map[string]bool{
		"local": true,
		"test1": true,
		"test2": true,
	}

	for _, name := range names {
		if !expectedNames[name] {
			t.Errorf("Unexpected executor name: %s", name)
		}
	}
}

func TestRegistry_GetAvailable(t *testing.T) {
	registry := NewRegistry()

	// Register local executor first.
	localExec := NewLocalExec()
	registry.Register(localExec)

	mock1 := &mockExecutor{name: "available", available: true}
	mock2 := &mockExecutor{name: "unavailable", available: false}

	registry.Register(mock1)
	registry.Register(mock2)

	available := registry.GetAvailable()

	// Should have local and available (not unavailable)
	if len(available) != 2 {
		t.Errorf("Expected 2 available executors, got %d", len(available))
	}

	for _, executor := range available {
		if !executor.Available() {
			t.Errorf("Executor %s should be available", string(executor.Name()))
		}
	}
}

func TestRegistry_GetBest(t *testing.T) {
	registry := NewRegistry()

	mock1 := &mockExecutor{name: "docker", available: true}
	mock2 := &mockExecutor{name: "local", available: true}

	registry.Register(mock1)
	registry.Register(mock2)

	// Test with preferences.
	preferences := []string{"docker", "local"}
	executor, err := registry.GetBest(preferences)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(executor.Name()) != "docker" {
		t.Errorf("Expected 'docker' executor, got %s", string(executor.Name()))
	}
}

func TestRegistry_GetBest_Fallback(t *testing.T) {
	registry := NewRegistry()

	mock1 := &mockExecutor{name: "docker", available: false}
	mock2 := &mockExecutor{name: "local", available: true}

	registry.Register(mock1)
	registry.Register(mock2)

	// Test with preferences where first is unavailable.
	preferences := []string{"docker", "local"}
	executor, err := registry.GetBest(preferences)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(executor.Name()) != "local" {
		t.Errorf("Expected 'local' executor, got %s", string(executor.Name()))
	}
}

func TestRegistry_GetBest_NoAvailable(t *testing.T) {
	registry := NewRegistry()

	mock1 := &mockExecutor{name: "test", available: false}
	registry.Register(mock1)

	// Remove local executor to test no available executors.
	registry.executors = map[string]Executor{
		"test": mock1,
	}

	preferences := []string{"test"}
	_, err := registry.GetBest(preferences)
	if err == nil {
		t.Error("Expected error when no executors available")
	}
}

func TestGlobalRegistry(t *testing.T) {
	// Test that global registry functions work.
	mock := &mockExecutor{name: "global_test", available: true}

	err := Register(mock)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	executor, err := Get("global_test")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if string(executor.Name()) != "global_test" {
		t.Errorf("Expected 'global_test', got %s", string(executor.Name()))
	}

	// Test that local executor is registered by default.
	names := List()
	found := false
	for _, name := range names {
		if name == "local" {
			found = true
			break
		}
	}

	if !found {
		t.Error("Expected local executor to be registered by default")
	}
}
