package coder

import (
	"strings"
	"testing"
)

func TestBuildVerificationFailureMessage_FailedCriteria(t *testing.T) {
	evidence := map[string]any{
		"acceptance_criteria_checked": []any{
			map[string]string{
				"criterion": "API returns 200",
				"method":    "command",
				"result":    "pass",
				"evidence":  "curl returned 200",
			},
			map[string]string{
				"criterion": "Input validation present",
				"method":    "inspection",
				"result":    "fail",
				"evidence":  "No validation in handler.go",
			},
			map[string]string{
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
		criteria[i] = map[string]string{
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
	// Test with map[string]any (as would come from JSON unmarshaling)
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
				map[string]string{
					"criterion": "API endpoint works",
					"result":    "pass",
				},
			},
			"confidence": "high",
		},
	}

	result := formatVerificationEvidence(outcome)
	if !strings.Contains(result, "All acceptance criteria verified") {
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
				map[string]string{
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
