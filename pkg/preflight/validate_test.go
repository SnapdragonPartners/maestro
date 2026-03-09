package preflight

import (
	"context"
	"errors"
	"testing"
)

func TestClassifyError(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		expect KeyStatus
	}{
		{"nil error", nil, KeyStatusValid},
		{"deadline exceeded", context.DeadlineExceeded, KeyStatusUnreachable},
		{"context canceled", context.Canceled, KeyStatusUnreachable},
		{"401 error", errors.New("API returned 401 Unauthorized"), KeyStatusUnauthorized},
		{"unauthorized lowercase", errors.New("request unauthorized"), KeyStatusUnauthorized},
		{"invalid_api_key", errors.New("error: invalid_api_key"), KeyStatusUnauthorized},
		{"403 forbidden", errors.New("403 Forbidden"), KeyStatusForbidden},
		{"permission denied", errors.New("permission denied for resource"), KeyStatusForbidden},
		{"connection refused", errors.New("connection refused"), KeyStatusUnreachable},
		{"no such host", errors.New("dial tcp: no such host"), KeyStatusUnreachable},
		{"timeout", errors.New("request timeout"), KeyStatusUnreachable},
		{"wrapped deadline", errors.New("context deadline exceeded"), KeyStatusUnreachable},
		{"unknown error", errors.New("some random error"), KeyStatusError},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyError(tt.err)
			if got != tt.expect {
				t.Errorf("classifyError(%v) = %q, want %q", tt.err, got, tt.expect)
			}
		})
	}
}

func TestValidateKeysMissingKeys(t *testing.T) {
	// With no secrets configured, all keys should be "missing"
	results := ValidateKeys(context.Background())
	if len(results) != 4 {
		t.Fatalf("Expected 4 results (one per provider), got %d", len(results))
	}

	for _, r := range results {
		// Keys might be present in the test environment (CI), so just verify
		// that missing keys get the right status
		if r.Status == KeyStatusMissing && r.Message != "Not configured" {
			t.Errorf("Missing key %s should have message 'Not configured', got %q", r.EnvVar, r.Message)
		}
	}
}
