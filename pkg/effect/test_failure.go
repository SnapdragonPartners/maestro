package effect

import (
	"context"

	"orchestrator/pkg/proto"
)

// TestFailureEffect represents a test failure that requires transitioning back to CODING
// with concise failure information for the LLM to understand and fix.
type TestFailureEffect struct {
	FailureType    string // Type of failure: "container_build_fix", "container_runtime_fix", "test_fix", etc.
	FailureMessage string // Concise failure message for the LLM (already truncated if needed)
	TargetState    proto.State
}

// Execute processes the test failure by setting appropriate context for the CODING state.
// This effect doesn't send messages - it just prepares the context transition data.
func (e *TestFailureEffect) Execute(_ context.Context, runtime Runtime) (any, error) {
	runtime.Info("🧪 Processing test failure: %s", e.FailureType)
	runtime.Debug("Test failure message: %s", e.FailureMessage)

	// Create result with failure context for CODING state
	result := &TestFailureResult{
		FailureType:    e.FailureType,
		FailureMessage: e.FailureMessage,
		TargetState:    e.TargetState,
	}

	return result, nil
}

// Type returns the effect type identifier.
func (e *TestFailureEffect) Type() string {
	return "test_failure"
}

// TestFailureResult represents the result of a test failure effect.
type TestFailureResult struct {
	FailureType    string      `json:"failure_type"`
	FailureMessage string      `json:"failure_message"`
	TargetState    proto.State `json:"target_state"`
}

// NewTestFailureEffect creates a test failure effect for TESTING→CODING transitions.
func NewTestFailureEffect(failureType, failureMessage string) *TestFailureEffect {
	return &TestFailureEffect{
		FailureType:    failureType,
		FailureMessage: failureMessage,
		TargetState:    proto.State("CODING"), // Target state for test failure fixes
	}
}

// NewContainerBuildFailureEffect creates a container build failure effect.
func NewContainerBuildFailureEffect(failureMessage string) *TestFailureEffect {
	return NewTestFailureEffect("container_build_fix", failureMessage)
}

// NewContainerRuntimeFailureEffect creates a container runtime failure effect.
func NewContainerRuntimeFailureEffect(failureMessage string) *TestFailureEffect {
	return NewTestFailureEffect("container_runtime_fix", failureMessage)
}

// NewGenericTestFailureEffect creates a generic test failure effect.
func NewGenericTestFailureEffect(failureMessage string) *TestFailureEffect {
	return NewTestFailureEffect("test_fix", failureMessage)
}

// NewContainerConfigSetupEffect creates a container configuration setup effect.
func NewContainerConfigSetupEffect(failureMessage string) *TestFailureEffect {
	return NewTestFailureEffect("container_config_setup", failureMessage)
}

// NewLoopbackLintFailureEffect creates a loopback linting failure effect.
func NewLoopbackLintFailureEffect(failureMessage string) *TestFailureEffect {
	return NewTestFailureEffect("loopback_lint_fix", failureMessage)
}
