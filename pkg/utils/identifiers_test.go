package utils

import "testing"

func TestSanitizeIdentifier(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "agent ID with colon",
			input:    "claude_sonnet4:001",
			expected: "claude_sonnet4-001",
		},
		{
			name:     "ID with spaces",
			input:    "test agent 123",
			expected: "test-agent-123",
		},
		{
			name:     "ID with slashes",
			input:    "path/to/agent",
			expected: "path-to-agent",
		},
		{
			name:     "ID with backslashes",
			input:    "path\\to\\agent",
			expected: "path-to-agent",
		},
		{
			name:     "complex ID",
			input:    "openai_o3:v1.2/beta test",
			expected: "openai_o3-v1.2-beta-test",
		},
		{
			name:     "already clean ID",
			input:    "clean-agent-123",
			expected: "clean-agent-123",
		},
		{
			name:     "empty string",
			input:    "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := SanitizeIdentifier(tt.input)
			if result != tt.expected {
				t.Errorf("SanitizeIdentifier(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		})
	}
}

func TestSanitizeContainerName(t *testing.T) {
	// Test that SanitizeContainerName is equivalent to SanitizeIdentifier
	input := "claude_sonnet4:001"
	expected := "claude_sonnet4-001"

	result1 := SanitizeIdentifier(input)
	result2 := SanitizeContainerName(input)

	if result1 != result2 {
		t.Errorf("SanitizeIdentifier and SanitizeContainerName should return same result")
	}

	if result1 != expected {
		t.Errorf("SanitizeContainerName(%q) = %q, want %q", input, result1, expected)
	}
}
