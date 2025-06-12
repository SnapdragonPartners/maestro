package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"orchestrator/pkg/state"
)

// isTestMode returns true if the code is running in a test environment
func isTestMode() bool {
	if strings.HasSuffix(os.Args[0], ".test") {
		return true
	}
	if os.Getenv("GO_TEST") == "1" {
		return true
	}
	return false
}

// resetModeForTest resets the system mode for testing
func resetModeForTest() {
	SystemMode = 0 // Reset to uninitialized
}

// TestHelper provides utilities for testing agent components
type TestHelper struct {
	t       *testing.T
	tempDir string
	store   *state.Store
	cleanup []func()
}

// NewTestHelper creates a new test helper
func NewTestHelper(t *testing.T) *TestHelper {
	t.Helper()
	
	tempDir := t.TempDir()
	store, err := state.NewStore(tempDir)
	if err != nil {
		t.Fatalf("Failed to create test state store: %v", err)
	}
	
	// Reset mode for clean test environment
	resetModeForTest()
	InitMode(ModeMock)
	
	return &TestHelper{
		t:       t,
		tempDir: tempDir,
		store:   store,
		cleanup: make([]func(), 0),
	}
}

// CreateTestDriver creates a driver with test configuration
func (h *TestHelper) CreateTestDriver(id string, initialState State) *BaseDriver {
	h.t.Helper()
	
	ctx := &AgentContext{
		Context: context.Background(),
		Logger:  log.New(os.Stdout, "", log.LstdFlags),
		WorkDir: h.tempDir,
		Store:   h.store,
	}
	
	cfg := &AgentConfig{
		ID:      id,
		Type:    "test",
		Context: *ctx,
		LLMConfig: &LLMConfig{
			MaxContextTokens: 8192,
			MaxOutputTokens:  4096,
			CompactIfOver:   1000,
		},
	}
	
	driver, err := NewBaseDriver(cfg, initialState)
	if err != nil {
		h.t.Fatalf("Failed to create test driver: %v", err)
	}
	
	if err := driver.Initialize(context.Background()); err != nil {
		h.t.Fatalf("Failed to initialize test driver: %v", err)
	}
	
	return driver
}

// CreateMockLLMClient creates a mock client with predefined responses
func (h *TestHelper) CreateMockLLMClient(responses []string, errors []error) LLMClient {
	h.t.Helper()
	
	compResponses := make([]CompletionResponse, len(responses))
	for i, resp := range responses {
		compResponses[i] = CompletionResponse{
			Content: resp,
		}
	}
	
	return NewMockLLMClient(compResponses, errors)
}

// CreateStateMachine creates a state machine for testing
func (h *TestHelper) CreateStateMachine(id string, initialState State) *BaseStateMachine {
	h.t.Helper()
	
	sm := NewBaseStateMachine(id, initialState, h.store)
	if err := sm.Initialize(context.Background()); err != nil {
		h.t.Fatalf("Failed to initialize test state machine: %v", err)
	}
	
	return sm
}

// AssertState verifies that a state machine is in the expected state
func (h *TestHelper) AssertState(sm StateMachine, expected State) {
	h.t.Helper()
	
	actual := sm.GetCurrentState()
	if actual != expected {
		h.t.Errorf("Expected state %v, got %v", expected, actual)
	}
}

// AssertStateTransition verifies a state transition works as expected
func (h *TestHelper) AssertStateTransition(sm StateMachine, to State, metadata map[string]interface{}) {
	h.t.Helper()
	
	from := sm.GetCurrentState()
	err := sm.TransitionTo(context.Background(), to, metadata)
	if err != nil {
		h.t.Errorf("Failed to transition from %v to %v: %v", from, to, err)
		return
	}
	
	h.AssertState(sm, to)
}

// AssertInvalidTransition verifies that an invalid transition is rejected
func (h *TestHelper) AssertInvalidTransition(sm StateMachine, to State) {
	h.t.Helper()
	
	from := sm.GetCurrentState()
	err := sm.TransitionTo(context.Background(), to, nil)
	if err == nil {
		h.t.Errorf("Expected error for invalid transition from %v to %v", from, to)
		return
	}
	
	// State should remain unchanged
	h.AssertState(sm, from)
}

// MockFailingClient creates a client that fails after a certain number of calls
type MockFailingClient struct {
	callCount    int
	failAfter    int
	failureCount int
	responses    []CompletionResponse
}

// NewMockFailingClient creates a client that fails after failAfter successful calls
func NewMockFailingClient(failAfter int, responses []CompletionResponse) *MockFailingClient {
	return &MockFailingClient{
		failAfter: failAfter,
		responses: responses,
	}
}

// Complete implements LLMClient interface with controlled failures
func (m *MockFailingClient) Complete(ctx context.Context, req CompletionRequest) (CompletionResponse, error) {
	m.callCount++
	
	if m.callCount > m.failAfter {
		m.failureCount++
		return CompletionResponse{}, NewTransientError(fmt.Errorf("simulated failure #%d", m.failureCount))
	}
	
	if len(m.responses) > 0 {
		idx := (m.callCount - 1) % len(m.responses)
		return m.responses[idx], nil
	}
	
	return CompletionResponse{Content: fmt.Sprintf("Mock response #%d", m.callCount)}, nil
}

// Stream implements LLMClient interface
func (m *MockFailingClient) Stream(ctx context.Context, req CompletionRequest) (<-chan StreamChunk, error) {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	
	ch := make(chan StreamChunk, 1)
	go func() {
		defer close(ch)
		ch <- StreamChunk{Content: resp.Content}
		ch <- StreamChunk{Done: true}
	}()
	
	return ch, nil
}

// GetCallCount returns the number of calls made
func (m *MockFailingClient) GetCallCount() int {
	return m.callCount
}

// GetFailureCount returns the number of failures
func (m *MockFailingClient) GetFailureCount() int {
	return m.failureCount
}

// Reset resets the client state
func (m *MockFailingClient) Reset() {
	m.callCount = 0
	m.failureCount = 0
}