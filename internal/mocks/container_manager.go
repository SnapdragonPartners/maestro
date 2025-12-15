package mocks

import (
	"context"
	"sync"
)

// StopContainerCall records the parameters of a StopContainer call.
type StopContainerCall struct {
	ContainerName string
}

// MockContainerManager implements coder.ContainerManager for testing.
// It provides configurable behavior for StopContainer and Shutdown operations.
//
// Note: For integration tests requiring realistic Docker behavior, consider using
// a real ContainerManager. Mocks are best suited for unit tests where Docker
// availability cannot be guaranteed or where speed is a priority.
type MockContainerManager struct {
	// StopContainerFunc is called when StopContainer is invoked.
	StopContainerFunc func(ctx context.Context, containerName string) error

	// ShutdownFunc is called when Shutdown is invoked.
	ShutdownFunc func(ctx context.Context) error

	// StopContainerCalls tracks all calls to StopContainer for verification.
	StopContainerCalls []StopContainerCall

	// ShutdownCalls tracks the number of times Shutdown was called.
	ShutdownCalls int

	// mu protects call tracking
	mu sync.Mutex
}

// NewMockContainerManager creates a new mock container manager with default behavior.
// Default behavior: all operations succeed with no error.
func NewMockContainerManager() *MockContainerManager {
	m := &MockContainerManager{}

	// Default StopContainer behavior: succeed
	m.StopContainerFunc = func(_ context.Context, _ string) error {
		return nil
	}

	// Default Shutdown behavior: succeed
	m.ShutdownFunc = func(_ context.Context) error {
		return nil
	}

	return m
}

// StopContainer implements coder.ContainerManager.
func (m *MockContainerManager) StopContainer(ctx context.Context, containerName string) error {
	m.mu.Lock()
	m.StopContainerCalls = append(m.StopContainerCalls, StopContainerCall{ContainerName: containerName})
	m.mu.Unlock()
	return m.StopContainerFunc(ctx, containerName)
}

// Shutdown implements coder.ContainerManager.
func (m *MockContainerManager) Shutdown(ctx context.Context) error {
	m.mu.Lock()
	m.ShutdownCalls++
	m.mu.Unlock()
	return m.ShutdownFunc(ctx)
}

// --- Configuration methods ---

// OnStopContainer sets a custom handler for StopContainer calls.
func (m *MockContainerManager) OnStopContainer(fn func(ctx context.Context, containerName string) error) {
	m.StopContainerFunc = fn
}

// OnShutdown sets a custom handler for Shutdown calls.
func (m *MockContainerManager) OnShutdown(fn func(ctx context.Context) error) {
	m.ShutdownFunc = fn
}

// --- Error simulation helpers ---

// FailStopContainerWith configures StopContainer to return the specified error.
func (m *MockContainerManager) FailStopContainerWith(err error) {
	m.StopContainerFunc = func(_ context.Context, _ string) error {
		return err
	}
}

// FailShutdownWith configures Shutdown to return the specified error.
func (m *MockContainerManager) FailShutdownWith(err error) {
	m.ShutdownFunc = func(_ context.Context) error {
		return err
	}
}

// FailStopContainerForName configures StopContainer to fail for a specific container name.
// Other containers succeed.
func (m *MockContainerManager) FailStopContainerForName(containerName string, err error) {
	m.StopContainerFunc = func(_ context.Context, name string) error {
		if name == containerName {
			return err
		}
		return nil
	}
}

// --- Verification helpers ---

// Reset clears all recorded calls.
func (m *MockContainerManager) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.StopContainerCalls = nil
	m.ShutdownCalls = 0
}

// GetStopContainerCallCount returns the number of times StopContainer was called.
func (m *MockContainerManager) GetStopContainerCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.StopContainerCalls)
}

// GetShutdownCallCount returns the number of times Shutdown was called.
func (m *MockContainerManager) GetShutdownCallCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ShutdownCalls
}

// WasContainerStopped returns true if StopContainer was called for the specified container.
func (m *MockContainerManager) WasContainerStopped(containerName string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, call := range m.StopContainerCalls {
		if call.ContainerName == containerName {
			return true
		}
	}
	return false
}

// LastStopContainerCall returns the most recent StopContainer call, or nil if none.
func (m *MockContainerManager) LastStopContainerCall() *StopContainerCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.StopContainerCalls) == 0 {
		return nil
	}
	return &m.StopContainerCalls[len(m.StopContainerCalls)-1]
}

// WasShutdownCalled returns true if Shutdown was called at least once.
func (m *MockContainerManager) WasShutdownCalled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ShutdownCalls > 0
}
