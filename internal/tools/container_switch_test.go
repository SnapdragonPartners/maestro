package tools

import (
	"testing"
	"time"

	"orchestrator/internal/state"
)

// TestTruncateImageID tests image ID truncation for logging.
func TestTruncateImageID(t *testing.T) {
	tests := []struct {
		name     string
		imageID  string
		expected string
	}{
		{
			name:     "short ID",
			imageID:  "abc123",
			expected: "abc123",
		},
		{
			name:     "exact 12 chars",
			imageID:  "abc123456789",
			expected: "abc123456789",
		},
		{
			name:     "long ID (sha256)",
			imageID:  "sha256:1234567890abcdef1234567890abcdef",
			expected: "sha256:12345",
		},
		{
			name:     "empty string",
			imageID:  "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := truncateImageID(tt.imageID)

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestFormatActiveContainer tests active container formatting.
func TestFormatActiveContainer(t *testing.T) {
	tests := []struct {
		name      string
		container *state.ActiveContainer
		expected  string
	}{
		{
			name:      "nil container",
			container: nil,
			expected:  "none",
		},
		{
			name: "safe role",
			container: &state.ActiveContainer{
				Role:    state.RoleSafe,
				ImageID: "sha256:abc123",
				CID:     "cid-123",
				Name:    "safe-container",
				Started: time.Now(),
			},
			expected: "sha256:abc12:safe",
		},
		{
			name: "target role",
			container: &state.ActiveContainer{
				Role:    state.RoleTarget,
				ImageID: "sha256:def456",
				CID:     "cid-456",
				Name:    "target-container",
				Started: time.Now(),
			},
			expected: "sha256:def45:target",
		},
		{
			name: "short image ID",
			container: &state.ActiveContainer{
				Role:    state.RoleTarget,
				ImageID: "short",
				CID:     "cid",
				Name:    "container",
				Started: time.Now(),
			},
			expected: "short:target",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatActiveContainer(tt.container)

			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestResultStruct tests Result structure creation.
func TestResultStruct(t *testing.T) {
	now := time.Now()
	result := &Result{
		Timestamp:     now,
		Status:        "switched",
		ActiveImageID: "sha256:abc123",
		Role:          "target",
	}

	if result.Status != "switched" {
		t.Errorf("expected status %q, got %q", "switched", result.Status)
	}

	if result.ActiveImageID != "sha256:abc123" {
		t.Errorf("expected image ID %q, got %q", "sha256:abc123", result.ActiveImageID)
	}

	if result.Role != "target" {
		t.Errorf("expected role %q, got %q", "target", result.Role)
	}

	if !result.Timestamp.Equal(now) {
		t.Error("expected timestamp to match")
	}
}

// TestResultStatuses tests common result status values.
func TestResultStatuses(t *testing.T) {
	statuses := []string{"switched", "noop", "failed"}

	for _, status := range statuses {
		result := &Result{
			Status:        status,
			Timestamp:     time.Now(),
			ActiveImageID: "test",
			Role:          "test",
		}

		if result.Status != status {
			t.Errorf("expected status %q, got %q", status, result.Status)
		}

		// Verify all fields are accessible
		if result.Timestamp.IsZero() {
			t.Error("expected non-zero timestamp")
		}
		if result.ActiveImageID == "" {
			t.Error("expected non-empty ActiveImageID")
		}
		if result.Role == "" {
			t.Error("expected non-empty Role")
		}
	}
}
