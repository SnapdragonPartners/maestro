package coder

import (
	"context"
	"strings"
	"testing"

	"orchestrator/pkg/tools"
)

func TestBuildVerificationFailureMessage_FailedCriteria(t *testing.T) {
	// Use map[string]any — the shape the tool actually emits
	evidence := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{
				"criterion": "API returns 200",
				"method":    "command",
				"result":    "pass",
				"evidence":  "curl returned 200",
			},
			map[string]any{
				"criterion": "Input validation present",
				"method":    "inspection",
				"result":    "fail",
				"evidence":  "No validation in handler.go",
			},
			map[string]any{
				"criterion": "Error messages are user-friendly",
				"method":    "inspection",
				"result":    "partial",
				"evidence":  "Some errors are raw stack traces",
			},
		},
		"gaps": []any{"Missing validation middleware"},
	}

	msg := buildVerificationFailureMessage(evidence)

	// Should include failed and partial, not pass
	if !strings.Contains(msg, "[FAIL]") {
		t.Error("Expected [FAIL] tag in message")
	}
	if !strings.Contains(msg, "[PARTIAL]") {
		t.Error("Expected [PARTIAL] tag in message")
	}
	if !strings.Contains(msg, "Input validation present") {
		t.Error("Expected failed criterion text")
	}
	if !strings.Contains(msg, "Missing validation middleware") {
		t.Error("Expected gap text")
	}
	// Should NOT include passing criteria
	if strings.Contains(msg, "API returns 200") {
		t.Error("Should not include passing criteria")
	}
}

func TestBuildVerificationFailureMessage_NilEvidence(t *testing.T) {
	msg := buildVerificationFailureMessage(nil)
	if msg == "" {
		t.Error("Expected non-empty fallback message")
	}
}

func TestBuildVerificationFailureMessage_Truncation(t *testing.T) {
	// Create evidence with very long entries to trigger truncation
	criteria := make([]any, 50)
	for i := range criteria {
		criteria[i] = map[string]any{
			"criterion": strings.Repeat("criterion text ", 10),
			"method":    "inspection",
			"result":    "fail",
			"evidence":  strings.Repeat("evidence text ", 20),
		}
	}

	evidence := map[string]any{
		"acceptance_criteria_checked": criteria,
	}

	msg := buildVerificationFailureMessage(evidence)

	// Should be within bounds (maxFailureMessageLen + truncation suffix)
	if len(msg) > maxFailureMessageLen+100 {
		t.Errorf("Message too long: %d chars (max %d)", len(msg), maxFailureMessageLen)
	}
}

func TestBuildVerificationFailureMessage_MapStringAny(t *testing.T) {
	// Test with map[string]any (the canonical shape from tool and JSON roundtrip)
	evidence := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{
				"criterion": "Test coverage > 80%",
				"method":    "command",
				"result":    "fail",
				"evidence":  "Coverage is 65%",
			},
		},
	}

	msg := buildVerificationFailureMessage(evidence)
	if !strings.Contains(msg, "Test coverage > 80%") {
		t.Error("Expected criterion text in message")
	}
}

func TestFormatVerificationEvidence_Pass(t *testing.T) {
	outcome := VerificationOutcome{
		Status: VerificationPass,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{
					"criterion": "API endpoint works",
					"result":    "pass",
				},
			},
			"confidence": "high",
		},
	}

	result := formatVerificationEvidence(outcome)
	if !strings.Contains(result, "No acceptance criteria failures found") {
		t.Error("Expected pass header")
	}
	if !strings.Contains(result, "API endpoint works") {
		t.Error("Expected criterion text")
	}
	if !strings.Contains(result, "high") {
		t.Error("Expected confidence")
	}
}

func TestFormatVerificationEvidence_Fail(t *testing.T) {
	outcome := VerificationOutcome{
		Status: VerificationFail,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{
					"criterion": "Tests pass",
					"result":    "fail",
				},
			},
			"gaps":       []any{"Missing test file"},
			"confidence": "medium",
		},
	}

	result := formatVerificationEvidence(outcome)
	if !strings.Contains(result, "gaps found") {
		t.Error("Expected fail header")
	}
	if !strings.Contains(result, "Missing test file") {
		t.Error("Expected gap text")
	}
}

func TestFormatVerificationEvidence_Unavailable(t *testing.T) {
	outcome := VerificationOutcome{
		Status: VerificationUnavailable,
		Reason: "LLM error: rate limited",
	}

	result := formatVerificationEvidence(outcome)
	if !strings.Contains(result, "unavailable") {
		t.Error("Expected unavailable header")
	}
	if !strings.Contains(result, "LLM error: rate limited") {
		t.Error("Expected reason text")
	}
}

func TestFormatVerificationEvidence_Icons(t *testing.T) {
	outcome := VerificationOutcome{
		Status: VerificationPass,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{"criterion": "A", "result": "pass"},
				map[string]any{"criterion": "B", "result": "fail"},
				map[string]any{"criterion": "C", "result": "partial"},
				map[string]any{"criterion": "D", "result": "unverified"},
			},
			"confidence": "low",
		},
	}

	result := formatVerificationEvidence(outcome)
	// Each criterion type should get its own icon
	lines := strings.Split(result, "\n")
	var foundPass, foundFail, foundPartial, foundUnverified bool
	for _, line := range lines {
		if strings.Contains(line, "A") && strings.Contains(line, "\u2705") {
			foundPass = true
		}
		if strings.Contains(line, "B") && strings.Contains(line, "\u274c") {
			foundFail = true
		}
		if strings.Contains(line, "C") && strings.Contains(line, "\u26a0") {
			foundPartial = true
		}
		if strings.Contains(line, "D") && strings.Contains(line, "\u2753") {
			foundUnverified = true
		}
	}
	if !foundPass {
		t.Error("Expected pass icon for criterion A")
	}
	if !foundFail {
		t.Error("Expected fail icon for criterion B")
	}
	if !foundPartial {
		t.Error("Expected partial icon for criterion C")
	}
	if !foundUnverified {
		t.Error("Expected unverified icon for criterion D")
	}
}

func TestVerificationOutcome_StatusValues(t *testing.T) {
	// Ensure the status constants are distinct
	if VerificationPass == VerificationFail {
		t.Error("pass and fail should be different")
	}
	if VerificationPass == VerificationUnavailable {
		t.Error("pass and unavailable should be different")
	}
	if VerificationFail == VerificationUnavailable {
		t.Error("fail and unavailable should be different")
	}
}

// TestRehydrateVerificationOutcome_DirectPath tests that in-memory typed structs
// pass through rehydration unchanged.
func TestRehydrateVerificationOutcome_DirectPath(t *testing.T) {
	original := VerificationOutcome{
		Status: VerificationPass,
		Evidence: map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{"criterion": "A", "result": "pass"},
			},
			"confidence": "high",
		},
	}

	outcome, ok := rehydrateVerificationOutcome(original)
	if !ok {
		t.Fatal("Expected rehydration to succeed for typed struct")
	}
	if outcome.Status != VerificationPass {
		t.Errorf("Expected status %s, got %s", VerificationPass, outcome.Status)
	}
	if outcome.Evidence["confidence"] != "high" {
		t.Errorf("Expected confidence 'high', got %v", outcome.Evidence["confidence"])
	}
}

// TestRehydrateVerificationOutcome_ResumedPath tests that map[string]any
// (as produced by state persistence JSON roundtrip) is correctly rehydrated.
func TestRehydrateVerificationOutcome_ResumedPath(t *testing.T) {
	// Simulate what state persistence produces after JSON roundtrip
	resumed := map[string]any{
		"Status": "fail",
		"Evidence": map[string]any{
			"acceptance_criteria_checked": []any{
				map[string]any{"criterion": "Tests pass", "result": "fail"},
			},
			"gaps":       []any{"Missing tests"},
			"confidence": "medium",
		},
		"Reason": "",
	}

	outcome, ok := rehydrateVerificationOutcome(resumed)
	if !ok {
		t.Fatal("Expected rehydration to succeed for map[string]any")
	}
	if outcome.Status != VerificationFail {
		t.Errorf("Expected status %s, got %s", VerificationFail, outcome.Status)
	}
	if outcome.Evidence == nil {
		t.Fatal("Expected non-nil evidence")
	}
	if outcome.Evidence["confidence"] != "medium" {
		t.Errorf("Expected confidence 'medium', got %v", outcome.Evidence["confidence"])
	}
}

// TestRehydrateVerificationOutcome_UnavailableResumed tests unavailable status rehydration.
func TestRehydrateVerificationOutcome_UnavailableResumed(t *testing.T) {
	resumed := map[string]any{
		"Status": "unavailable",
		"Reason": "LLM error: timeout",
	}

	outcome, ok := rehydrateVerificationOutcome(resumed)
	if !ok {
		t.Fatal("Expected rehydration to succeed")
	}
	if outcome.Status != VerificationUnavailable {
		t.Errorf("Expected status %s, got %s", VerificationUnavailable, outcome.Status)
	}
	if outcome.Reason != "LLM error: timeout" {
		t.Errorf("Expected reason text, got %q", outcome.Reason)
	}
	if outcome.Evidence != nil {
		t.Error("Expected nil evidence for unavailable")
	}
}

// TestRehydrateVerificationOutcome_InvalidInput tests that invalid inputs fail gracefully.
func TestRehydrateVerificationOutcome_InvalidInput(t *testing.T) {
	tests := []struct {
		name  string
		input any
	}{
		{"nil", nil},
		{"string", "not a struct"},
		{"int", 42},
		{"empty map", map[string]any{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ok := rehydrateVerificationOutcome(tt.input)
			if ok {
				t.Error("Expected rehydration to fail for invalid input")
			}
		})
	}
}

// TestProducerConsumerRoundTrip exercises the full path: tool emits evidence →
// consumers (buildVerificationFailureMessage, formatVerificationEvidence) process it.
// This catches type shape mismatches between producer and consumer.
func TestProducerConsumerRoundTrip(t *testing.T) {
	tool := tools.NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{
				"criterion": "API returns 200",
				"method":    "command",
				"result":    "pass",
				"evidence":  "curl OK",
			},
			map[string]any{
				"criterion": "Input validation",
				"method":    "inspection",
				"result":    "fail",
				"evidence":  "No validation found",
			},
		},
		"gaps":       []any{"Missing validation"},
		"confidence": "medium",
		"summary":    "One gap found",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Tool exec failed: %v", err)
	}

	evidence, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data from tool")
	}

	// Feed tool output directly into buildVerificationFailureMessage
	failMsg := buildVerificationFailureMessage(evidence)
	if !strings.Contains(failMsg, "[FAIL]") {
		t.Error("buildVerificationFailureMessage: expected [FAIL] tag from tool output")
	}
	if !strings.Contains(failMsg, "Input validation") {
		t.Error("buildVerificationFailureMessage: expected criterion text from tool output")
	}
	if !strings.Contains(failMsg, "Missing validation") {
		t.Error("buildVerificationFailureMessage: expected gap text from tool output")
	}
	// Passing criteria should not appear
	if strings.Contains(failMsg, "API returns 200") {
		t.Error("buildVerificationFailureMessage: should not include passing criteria")
	}

	// Feed tool output directly into formatVerificationEvidence
	outcome := VerificationOutcome{
		Status:   VerificationFail,
		Evidence: evidence,
	}
	formatted := formatVerificationEvidence(outcome)
	if !strings.Contains(formatted, "gaps found") {
		t.Error("formatVerificationEvidence: expected fail header from tool output")
	}
	if !strings.Contains(formatted, "Input validation") {
		t.Error("formatVerificationEvidence: expected criterion in formatted output")
	}
	if !strings.Contains(formatted, "Missing validation") {
		t.Error("formatVerificationEvidence: expected gap in formatted output")
	}
}

// TestProducerConsumerRoundTrip_AllPass verifies the pass path with actual tool output.
func TestProducerConsumerRoundTrip_AllPass(t *testing.T) {
	tool := tools.NewSubmitVerificationTool()

	args := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]any{
				"criterion": "Tests pass",
				"method":    "command",
				"result":    "pass",
				"evidence":  "All green",
			},
		},
		"confidence": "high",
		"summary":    "All criteria met",
	}

	result, err := tool.Exec(context.Background(), args)
	if err != nil {
		t.Fatalf("Tool exec failed: %v", err)
	}
	if result.ProcessEffect.Signal != tools.SignalVerificationPass {
		t.Errorf("Expected pass signal, got %s", result.ProcessEffect.Signal)
	}

	evidence, ok := result.ProcessEffect.Data.(map[string]any)
	if !ok {
		t.Fatal("Expected map[string]any data from tool")
	}

	outcome := VerificationOutcome{
		Status:   VerificationPass,
		Evidence: evidence,
	}
	formatted := formatVerificationEvidence(outcome)
	if !strings.Contains(formatted, "No acceptance criteria failures found") {
		t.Error("Expected pass header in formatted output")
	}
	if !strings.Contains(formatted, "Tests pass") {
		t.Error("Expected criterion text in formatted output")
	}
}
