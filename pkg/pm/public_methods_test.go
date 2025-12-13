package pm

import (
	"context"
	"testing"

	"orchestrator/pkg/agent"
	"orchestrator/pkg/contextmgr"
	"orchestrator/pkg/logx"
	"orchestrator/pkg/proto"
	"orchestrator/pkg/utils"
)

// createTestDriver creates a minimal PM driver for testing public methods.
// Uses NewBaseStateMachine directly to avoid needing all dependencies.
func createTestDriver(initialState proto.State) *Driver {
	sm := agent.NewBaseStateMachine("pm-test", initialState, nil, validTransitions)
	return &Driver{
		BaseStateMachine: sm,
		contextManager:   contextmgr.NewContextManager(),
		logger:           logx.NewLogger("pm-test"),
		workDir:          "/tmp/test-pm",
	}
}

// =============================================================================
// StartInterview Tests
// =============================================================================

func TestStartInterview_FailsInWrongState(t *testing.T) {
	tests := []struct {
		name         string
		initialState proto.State
	}{
		{"fails in WORKING state", StateWorking},
		{"fails in PREVIEW state", StatePreview},
		{"fails in AWAIT_ARCHITECT state", StateAwaitArchitect},
		{"fails in DONE state", proto.StateDone},
		{"fails in ERROR state", proto.StateError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := createTestDriver(tt.initialState)
			err := driver.StartInterview("BASIC")
			if err == nil {
				t.Errorf("Expected error when starting interview in %s state", tt.initialState)
			}
		})
	}
}

func TestStartInterview_SucceedsInWaitingState(t *testing.T) {
	driver := createTestDriver(StateWaiting)

	err := driver.StartInterview("BASIC")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify expertise was stored
	expertise := utils.GetStateValueOr[string](driver.BaseStateMachine, StateKeyUserExpertise, "")
	if expertise != "BASIC" {
		t.Errorf("Expected expertise BASIC, got %s", expertise)
	}

	// Verify state transitioned (to either WORKING or AWAIT_USER depending on bootstrap)
	state := driver.GetCurrentState()
	if state != StateWorking && state != StateAwaitUser {
		t.Errorf("Expected state WORKING or AWAIT_USER, got %s", state)
	}
}

func TestStartInterview_Idempotency(t *testing.T) {
	tests := []struct {
		name         string
		initialState proto.State
	}{
		{"idempotent in AWAIT_USER", StateAwaitUser},
		{"idempotent in WORKING", StateWorking},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := createTestDriver(tt.initialState)
			// Pre-set expertise to match what we'll request
			driver.SetStateData(StateKeyUserExpertise, "EXPERT")

			// Should succeed (idempotent)
			err := driver.StartInterview("EXPERT")
			if err != nil {
				t.Errorf("Expected idempotent success, got error: %v", err)
			}

			// State should remain unchanged
			if driver.GetCurrentState() != tt.initialState {
				t.Errorf("State should not change for idempotent call")
			}
		})
	}
}

func TestStartInterview_DifferentExpertiseFails(t *testing.T) {
	driver := createTestDriver(StateWorking)
	driver.SetStateData(StateKeyUserExpertise, "BASIC")

	// Different expertise should fail (not idempotent)
	err := driver.StartInterview("EXPERT")
	if err == nil {
		t.Error("Expected error when starting interview with different expertise")
	}
}

// =============================================================================
// UploadSpec Tests
// =============================================================================

func TestUploadSpec_FailsInWrongState(t *testing.T) {
	tests := []struct {
		name         string
		initialState proto.State
	}{
		{"fails in WORKING state", StateWorking},
		{"fails in PREVIEW state", StatePreview},
		{"fails in AWAIT_ARCHITECT state", StateAwaitArchitect},
		{"fails in DONE state", proto.StateDone},
		{"fails in ERROR state", proto.StateError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := createTestDriver(tt.initialState)
			err := driver.UploadSpec("# Test Spec")
			if err == nil {
				t.Errorf("Expected error when uploading spec in %s state", tt.initialState)
			}
		})
	}
}

func TestUploadSpec_SucceedsInWaitingState(t *testing.T) {
	driver := createTestDriver(StateWaiting)
	testSpec := "# Test Spec\n\nThis is a test."

	err := driver.UploadSpec(testSpec)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify spec was stored
	storedSpec := utils.GetStateValueOr[string](driver.BaseStateMachine, StateKeyUserSpecMd, "")
	if storedSpec != testSpec {
		t.Errorf("Spec not stored correctly")
	}

	// Verify expertise was set to EXPERT (user provided own spec)
	expertise := utils.GetStateValueOr[string](driver.BaseStateMachine, StateKeyUserExpertise, "")
	if expertise != "EXPERT" {
		t.Errorf("Expected expertise EXPERT, got %s", expertise)
	}

	// Verify uploaded flag was set
	uploaded := utils.GetStateValueOr[bool](driver.BaseStateMachine, StateKeySpecUploaded, false)
	if !uploaded {
		t.Error("Expected spec_uploaded flag to be true")
	}
}

func TestUploadSpec_SucceedsInAwaitUserState(t *testing.T) {
	driver := createTestDriver(StateAwaitUser)

	err := driver.UploadSpec("# Test Spec")
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify state transitioned to WORKING
	state := driver.GetCurrentState()
	if state != StateWorking {
		t.Errorf("Expected state WORKING, got %s", state)
	}
}

func TestUploadSpec_Idempotency(t *testing.T) {
	tests := []struct {
		name         string
		initialState proto.State
	}{
		{"idempotent in WORKING", StateWorking},
		{"idempotent in PREVIEW", StatePreview},
	}

	testSpec := "# Same Spec"

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := createTestDriver(tt.initialState)
			// Pre-set spec to match what we'll upload
			driver.SetStateData(StateKeyUserSpecMd, testSpec)

			// Should succeed (idempotent)
			err := driver.UploadSpec(testSpec)
			if err != nil {
				t.Errorf("Expected idempotent success, got error: %v", err)
			}

			// State should remain unchanged
			if driver.GetCurrentState() != tt.initialState {
				t.Errorf("State should not change for idempotent call")
			}
		})
	}
}

// =============================================================================
// PreviewAction Tests
// =============================================================================

func TestPreviewAction_FailsInWrongState(t *testing.T) {
	// Note: AWAIT_USER is excluded because PreviewActionContinue is idempotent there
	// Note: AWAIT_ARCHITECT is excluded because PreviewActionSubmit is idempotent there
	tests := []struct {
		name         string
		initialState proto.State
	}{
		{"fails in WAITING state", StateWaiting},
		{"fails in WORKING state", StateWorking},
		{"fails in DONE state", proto.StateDone},
		{"fails in ERROR state", proto.StateError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := createTestDriver(tt.initialState)
			err := driver.PreviewAction(context.Background(), PreviewActionContinue)
			if err == nil {
				t.Errorf("Expected error when calling PreviewAction in %s state", tt.initialState)
			}
		})
	}
}

func TestPreviewAction_FailsInAwaitArchitect(t *testing.T) {
	// AWAIT_ARCHITECT fails for continue_interview (only submit is idempotent)
	driver := createTestDriver(StateAwaitArchitect)
	err := driver.PreviewAction(context.Background(), PreviewActionContinue)
	if err == nil {
		t.Error("Expected error when calling PreviewAction(continue) in AWAIT_ARCHITECT state")
	}
}

func TestPreviewAction_InvalidAction(t *testing.T) {
	driver := createTestDriver(StatePreview)
	driver.SetStateData(StateKeyUserSpecMd, "# Test Spec")

	err := driver.PreviewAction(context.Background(), "invalid_action")
	if err == nil {
		t.Error("Expected error for invalid action")
	}
}

func TestPreviewAction_ContinueInterview(t *testing.T) {
	driver := createTestDriver(StatePreview)
	driver.SetStateData(StateKeyUserSpecMd, "# Test Spec")

	err := driver.PreviewAction(context.Background(), PreviewActionContinue)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	// Verify state transitioned to AWAIT_USER
	if driver.GetCurrentState() != StateAwaitUser {
		t.Errorf("Expected state AWAIT_USER, got %s", driver.GetCurrentState())
	}
}

func TestPreviewAction_SubmitWithoutSpec(t *testing.T) {
	driver := createTestDriver(StatePreview)
	// Don't set any spec

	err := driver.PreviewAction(context.Background(), PreviewActionSubmit)
	if err == nil {
		t.Error("Expected error when submitting without spec")
	}
}

func TestPreviewAction_ContinueIdempotency(t *testing.T) {
	driver := createTestDriver(StateAwaitUser)
	driver.SetStateData(StateKeyUserSpecMd, "# Test Spec")

	// Already in AWAIT_USER, continue should be idempotent
	err := driver.PreviewAction(context.Background(), PreviewActionContinue)
	if err != nil {
		t.Errorf("Expected idempotent success, got error: %v", err)
	}

	// State should remain AWAIT_USER
	if driver.GetCurrentState() != StateAwaitUser {
		t.Errorf("State should remain AWAIT_USER for idempotent call")
	}
}

func TestPreviewAction_SubmitIdempotency(t *testing.T) {
	driver := createTestDriver(StateAwaitArchitect)
	driver.SetStateData(StateKeyUserSpecMd, "# Test Spec")

	// Already in AWAIT_ARCHITECT, submit should be idempotent
	err := driver.PreviewAction(context.Background(), PreviewActionSubmit)
	if err != nil {
		t.Errorf("Expected idempotent success, got error: %v", err)
	}

	// State should remain AWAIT_ARCHITECT
	if driver.GetCurrentState() != StateAwaitArchitect {
		t.Errorf("State should remain AWAIT_ARCHITECT for idempotent call")
	}
}

// =============================================================================
// collectBootstrapParamsJSON Tests
// =============================================================================

func TestCollectBootstrapParamsJSON_Empty(t *testing.T) {
	driver := createTestDriver(StateWaiting)

	result := driver.collectBootstrapParamsJSON()
	if result != nil {
		t.Error("Expected nil when no bootstrap params are set")
	}
}

func TestCollectBootstrapParamsJSON_WithParams(t *testing.T) {
	driver := createTestDriver(StateWaiting)

	// Set some bootstrap params
	driver.SetStateData(StateKeyHasRepository, true)
	driver.SetStateData(StateKeyUserExpertise, "EXPERT")
	driver.SetStateData(StateKeyDetectedPlatform, "go")
	driver.SetStateData(StateKeyInFlight, false)

	result := driver.collectBootstrapParamsJSON()
	if result == nil {
		t.Fatal("Expected non-nil result")
	}

	// Verify it's valid JSON containing expected keys
	json := *result
	if len(json) == 0 {
		t.Error("Expected non-empty JSON string")
	}

	// Check that it contains expected substrings
	expectedSubstrings := []string{
		`"has_repository":true`,
		`"user_expertise":"EXPERT"`,
		`"detected_platform":"go"`,
		`"in_flight":false`,
	}

	for _, substr := range expectedSubstrings {
		if !contains(json, substr) {
			t.Errorf("Expected JSON to contain %s, got: %s", substr, json)
		}
	}
}

// contains checks if string s contains substring substr.
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsHelper(s, substr))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// =============================================================================
// GetDraftSpec Tests
// =============================================================================

func TestGetDraftSpec_Empty(t *testing.T) {
	driver := createTestDriver(StateWaiting)

	spec := driver.GetDraftSpec()
	if spec != "" {
		t.Errorf("Expected empty spec, got: %s", spec)
	}
}

func TestGetDraftSpec_WithSpec(t *testing.T) {
	driver := createTestDriver(StatePreview)
	expectedSpec := "# My Feature Spec\n\nDescription here."
	driver.SetStateData(StateKeyUserSpecMd, expectedSpec)

	spec := driver.GetDraftSpec()
	if spec != expectedSpec {
		t.Errorf("Expected spec %q, got %q", expectedSpec, spec)
	}
}

// =============================================================================
// IsInFlight Tests
// =============================================================================

func TestIsInFlight_Default(t *testing.T) {
	driver := createTestDriver(StateWaiting)

	if driver.IsInFlight() {
		t.Error("Expected IsInFlight to be false by default")
	}
}

func TestIsInFlight_WhenSet(t *testing.T) {
	driver := createTestDriver(StateWorking)
	driver.SetStateData(StateKeyInFlight, true)

	if !driver.IsInFlight() {
		t.Error("Expected IsInFlight to be true when set")
	}
}

// =============================================================================
// HasRepository Tests
// =============================================================================

func TestHasRepository_Default(t *testing.T) {
	driver := createTestDriver(StateWaiting)

	if driver.HasRepository() {
		t.Error("Expected HasRepository to be false by default")
	}
}

func TestHasRepository_WhenSet(t *testing.T) {
	driver := createTestDriver(StateWaiting)
	driver.SetStateData(StateKeyHasRepository, true)

	if !driver.HasRepository() {
		t.Error("Expected HasRepository to be true when set")
	}
}
