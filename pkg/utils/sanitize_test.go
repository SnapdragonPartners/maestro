package utils

import (
	"strings"
	"testing"
)

func TestSanitizeStringEmpty(t *testing.T) {
	if got := SanitizeString("", 100); got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

func TestSanitizeStringStripsSecrets(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"api_key", "my api_key=sk-abc123456789012345678901234567890 rest"},
		{"token", "token: ghp_abcdefghijklmnopqrstuvwxyz1234"},
		{"password", "password=supersecretpassword123"},
		{"bearer", "bearer: sk-abcdefghijklmnopqrstuvwxyz1234567890"},
		{"aws key", "key is AKIAIOSFODNN7EXAMPLE rest"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeString(tt.input, 0)
			if !strings.Contains(result, "[REDACTED]") {
				t.Errorf("expected [REDACTED] in result, got %q", result)
			}
		})
	}
}

func TestSanitizeStringNormalizesHomePaths(t *testing.T) {
	input := "error at /Users/johndoe/projects/app/main.go"
	result := SanitizeString(input, 0)
	if strings.Contains(result, "johndoe") {
		t.Errorf("expected username stripped, got %q", result)
	}
	if !strings.Contains(result, "<user>/") {
		t.Errorf("expected <user>/ replacement, got %q", result)
	}
}

func TestSanitizeStringTruncates(t *testing.T) {
	input := strings.Repeat("a", 200)
	result := SanitizeString(input, 100)
	if len(result) > 120 { // 100 + "...[truncated]"
		t.Errorf("expected truncation, got length %d", len(result))
	}
	if !strings.HasSuffix(result, "...[truncated]") {
		t.Errorf("expected truncated suffix, got %q", result)
	}
}

func TestSanitizeStringUTF8Safe(t *testing.T) {
	// Create a string with multi-byte UTF-8 characters (emoji = 4 bytes each)
	input := strings.Repeat("🎉", 10) // 40 bytes, 10 runes
	// Truncate at 15 bytes — should not split an emoji (each is 4 bytes)
	result := SanitizeString(input, 15)
	if !strings.HasSuffix(result, "...[truncated]") {
		t.Errorf("expected truncated suffix, got %q", result)
	}
	// Extract the prefix before the suffix
	prefix := strings.TrimSuffix(result, "...[truncated]")
	// Should be 3 complete emoji (12 bytes) since 4 emoji = 16 bytes > 15
	if prefix != "🎉🎉🎉" {
		t.Errorf("expected 3 complete emoji, got %q (len=%d)", prefix, len(prefix))
	}
}

func TestSanitizeStringNoTruncationWhenUnderLimit(t *testing.T) {
	input := "short string"
	result := SanitizeString(input, 100)
	if result != input {
		t.Errorf("expected no change, got %q", result)
	}
}

func TestSanitizeStringZeroMaxLen(t *testing.T) {
	input := strings.Repeat("x", 1000)
	result := SanitizeString(input, 0)
	// maxLen=0 means no truncation
	if len(result) != 1000 {
		t.Errorf("expected no truncation with maxLen=0, got length %d", len(result))
	}
}
