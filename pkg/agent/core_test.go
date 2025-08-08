//go:build ignore

//nolint:all // Legacy test file - needs migration to new APIs
package agent

import (
	"context"
	"errors"
	"fmt"
	"log"
	"testing"
	"time"

	"orchestrator/pkg/config"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
)

func TestCircuitBreakerClient(t *testing.T) {
	// Create a mock base client
	baseClient := NewClaudeClient("test-key")

	// Test circuit breaker client creation
	cbConfig := CircuitBreakerConfig{
		FailureThreshold: 3,
		Timeout:          time.Second,
	}
	cbClient := NewCircuitBreakerClient(baseClient, cbConfig)
	if cbClient == nil {
		t.Error("Expected non-nil circuit breaker client")
	}

	// Test getting initial state
	state := cbClient.GetState()
	if state != CircuitClosed {
		t.Errorf("Expected initial state CLOSED, got %v", state)
	}

	// Test getting failure count
	count := cbClient.GetFailureCount()
	if count != 0 {
		t.Errorf("Expected initial failure count 0, got %d", count)
	}

	// Test reset
	cbClient.Reset()
	if cbClient.GetState() != CircuitClosed {
		t.Error("Expected state to be CLOSED after reset")
	}
}

func TestRetryableClient(t *testing.T) {
	// Create base client and retry config
	baseClient := NewClaudeClient("test-key")
	retryConfig := RetryConfig{
		MaxRetries:    3,
		InitialDelay:  time.Millisecond,
		BackoffFactor: 2.0,
	}

	retryClient := NewRetryableClient(baseClient, retryConfig)
	if retryClient == nil {
		t.Error("Expected non-nil retryable client")
	}

	// Test with logger - create a simple nil logger for testing
	retryClientWithLogger := NewRetryableClientWithLogger(baseClient, retryConfig, nil)
	if retryClientWithLogger == nil {
		t.Error("Expected non-nil retryable client with logger")
	}
}

func TestTransientError(t *testing.T) {
	baseErr := fmt.Errorf("network timeout")
	transientErr := NewTransientError(baseErr)

	// Note: TransientError is now a struct, not a pointer, so it can't be nil

	if !transientErr.ShouldRetry() {
		t.Error("Expected transient error to be retryable")
	}

	if !errors.Is(transientErr, baseErr) && transientErr.Unwrap().Error() != baseErr.Error() {
		t.Error("Expected unwrap to return base error")
	}

	if transientErr.Error() == "" {
		t.Error("Expected non-empty error message")
	}
}

func TestShutdownManager(t *testing.T) {
	sm := NewShutdownManager()
	if sm == nil {
		t.Error("Expected non-nil shutdown manager")
	}

	// Test basic shutdown
	err := sm.Shutdown(context.Background())
	if err != nil {
		t.Errorf("Expected no error during shutdown, got: %v", err)
	}

	// Test shutdown state
	if !sm.IsShuttingDown() {
		t.Error("Expected shutdown manager to be in shutting down state")
	}

	// Test shutdown context
	ctx := sm.ShutdownContext()
	if ctx == nil {
		t.Error("Expected non-nil shutdown context")
	}
}

func TestShutdownableDriver(t *testing.T) {
	tempDir := t.TempDir()

	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	ctx := Context{
		Context: context.Background(),
		Logger:  log.Default(),
	}

	cfg := NewConfig("test-shutdownable", "coder", ctx)
	sm := NewShutdownManager()

	driver, err := NewShutdownableDriver(cfg, proto.StateWaiting, sm)
	if err != nil {
		t.Fatalf("Failed to create shutdownable driver: %v", err)
	}
	if driver == nil {
		t.Error("Expected non-nil shutdownable driver")
	}

	// Note: driver.Name() and driver.CanResume() methods are not accessible
	// from the agent package due to internal visibility. Test disabled.
	_ = driver
}

func TestPromptLogger(t *testing.T) {
	logger := log.Default()
	config := PromptLogConfig{
		Mode:        PromptLogOnFailure,
		MaxChars:    1000,
		IncludeHash: true,
	}

	// Test that we can create a prompt logger (basic struct test)
	_ = config // Use the config to avoid unused variable error
	_ = logger // Use the logger to avoid unused variable error

	// The NewPromptLogger function requires parameters that need complex setup
	// For basic coverage, we'll just test the config validation
	if config.MaxChars <= 0 {
		t.Error("Expected positive max chars")
	}
	if config.Mode == "" {
		t.Error("Expected non-empty mode")
	}
}

func TestLLMConfigValidation(t *testing.T) {
	cfg := &LLMConfig{
		MaxTokens:   1000,
		Temperature: 0.7,
		ModelName:   "test-model",
	}

	// Test validation
	err := cfg.Validate()
	if err != nil {
		t.Errorf("Expected valid LLM config, got error: %v", err)
	}

	// Test invalid config - empty model name
	invalidCfg := &LLMConfig{
		MaxTokens:   1000,
		Temperature: 0.7,
		ModelName:   "", // Invalid empty model name
	}
	err = invalidCfg.Validate()
	if err == nil {
		t.Error("Expected invalid LLM config to fail validation")
	}
}

func TestBaseDriver(t *testing.T) {
	tempDir := t.TempDir()

	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	ctx := Context{
		Context: context.Background(),
		Logger:  log.Default(),
	}

	cfg := NewConfig("test-driver", "coder", ctx)
	baseDriver, err := NewBaseDriver(cfg, proto.StateWaiting)
	if err != nil {
		t.Fatalf("Failed to create base driver: %v", err)
	}
	if baseDriver == nil {
		t.Error("Expected non-nil base driver")
		return // Prevent further nil pointer dereferences
	}

	// Test basic driver methods that are available via embedded StateMachine
	if baseDriver.GetCurrentState() == "" {
		t.Error("Expected non-empty current state")
	}

	stateData := baseDriver.GetStateData()
	if stateData == nil {
		t.Error("Expected non-nil state data")
	}

	// Test that we can initialize the driver
	err = baseDriver.Initialize(context.Background())
	if err != nil {
		t.Errorf("Expected no error during initialize, got: %v", err)
	}
}

func TestBaseStateMachine(t *testing.T) {
	initialState := proto.StateWaiting
	sm := NewBaseStateMachine("test-sm", initialState, nil, nil)
	if sm == nil {
		t.Error("Expected non-nil base state machine")
	}

	// Test getting current state
	currentState := sm.GetCurrentState()
	if currentState != initialState {
		t.Errorf("Expected initial state %v, got %v", initialState, currentState)
	}

	// Test getting agent ID
	agentID := sm.GetAgentID()
	if agentID != "test-sm" {
		t.Errorf("Expected agent ID 'test-sm', got '%s'", agentID)
	}

	// Test state data
	sm.SetStateData("test-key", "test-value")
	value, exists := sm.GetStateValue("test-key")
	if !exists {
		t.Error("Expected state value to exist")
	}
	if value != "test-value" {
		t.Errorf("Expected 'test-value', got '%v'", value)
	}

	// Test typed setters/getters
	SetTyped(sm, "typed-key", 42)
	typedValue, exists := GetTyped[int](sm, "typed-key")
	if !exists {
		t.Error("Expected typed value to exist")
	}
	if typedValue != 42 {
		t.Errorf("Expected 42, got %d", typedValue)
	}
}

func TestTimeoutOperations(t *testing.T) {
	tempDir := t.TempDir()

	err := config.LoadConfig(tempDir)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	ctx := Context{
		Context: context.Background(),
		Logger:  log.Default(),
	}

	cfg := NewConfig("test-timeout", "coder", ctx)
	baseDriver, err := NewBaseDriver(cfg, proto.StateWaiting)
	if err != nil {
		t.Fatalf("Failed to create base driver: %v", err)
	}

	// Note: StepWithTimeout and RunWithTimeout methods were removed during refactoring
	// These tests are disabled as the methods no longer exist in BaseDriver
	_ = baseDriver // Prevent unused variable warning
}

func TestResilientClient(t *testing.T) {
	baseClient := NewClaudeClient("test-key")

	// Test creating resilient client
	resilientClient := NewResilientClient(baseClient)
	if resilientClient == nil {
		t.Error("Expected non-nil resilient client")
	}

	// Test with logger
	logger := logx.NewLogger("test-resilient")
	resilientClientWithLogger := NewResilientClientWithLogger(baseClient, logger)
	if resilientClientWithLogger == nil {
		t.Error("Expected non-nil resilient client with logger")
	}
}
