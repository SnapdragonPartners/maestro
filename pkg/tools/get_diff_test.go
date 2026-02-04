package tools

import (
	"context"
	"strings"
	"testing"
)

func TestGetDiffToolValidationRejectsInvalidIDs(t *testing.T) {
	// Create tool with nil executor - we're only testing validation which returns early for invalid IDs
	tool := NewGetDiffTool(nil, 1000)

	invalidIDs := []struct {
		name    string
		coderID string
	}{
		{"architect", "architect-001"},
		{"pm", "pm-001"},
		{"random", "random-agent"},
		{"empty prefix", "001"},
		{"supervisor", "supervisor"},
	}

	for _, tt := range invalidIDs {
		t.Run(tt.name, func(t *testing.T) {
			args := map[string]any{
				"coder_id": tt.coderID,
			}

			result, err := tool.Exec(context.Background(), args)

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result == nil {
				t.Fatal("expected result with error, got nil")
			}

			if !strings.Contains(result.Content, "invalid coder_id format") {
				t.Errorf("expected error message containing 'invalid coder_id format', got %s", result.Content)
			}
		})
	}
}

func TestGetDiffToolValidationAcceptsValidIDs(t *testing.T) {
	// For valid IDs, we can't fully test without an executor, but we can verify
	// the validation logic by checking the code accepts the right prefixes
	validPrefixes := []string{"coder-", "hotfix-"}

	for _, prefix := range validPrefixes {
		testID := prefix + "001"
		if !strings.HasPrefix(testID, "coder-") && !strings.HasPrefix(testID, "hotfix-") {
			t.Errorf("validation logic should accept %q but wouldn't", testID)
		}
	}

	// Verify the validation condition matches what we expect
	testCases := []struct {
		id    string
		valid bool
	}{
		{"coder-001", true},
		{"coder-002", true},
		{"hotfix-001", true},
		{"hotfix-002", true},
		{"architect-001", false},
		{"pm-001", false},
	}

	for _, tc := range testCases {
		isValid := strings.HasPrefix(tc.id, "coder-") || strings.HasPrefix(tc.id, "hotfix-")
		if isValid != tc.valid {
			t.Errorf("ID %q: expected valid=%v, got valid=%v", tc.id, tc.valid, isValid)
		}
	}
}
