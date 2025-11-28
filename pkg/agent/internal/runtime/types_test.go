package runtime

import (
	"testing"
)

// TestAgentTypeString tests the String method.
func TestAgentTypeString(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
		expected  string
	}{
		{
			name:      "architect type",
			agentType: AgentTypeArchitect,
			expected:  "architect",
		},
		{
			name:      "coder type",
			agentType: AgentTypeCoder,
			expected:  "coder",
		},
		{
			name:      "custom type",
			agentType: AgentType("custom"),
			expected:  "custom",
		},
		{
			name:      "empty type",
			agentType: AgentType(""),
			expected:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.agentType.String()
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestParseAgentType tests agent type parsing.
func TestParseAgentType(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected AgentType
	}{
		{
			name:     "architect string",
			input:    "architect",
			expected: AgentTypeArchitect,
		},
		{
			name:     "coder string",
			input:    "coder",
			expected: AgentTypeCoder,
		},
		{
			name:     "unknown type defaults to coder",
			input:    "unknown",
			expected: AgentTypeCoder,
		},
		{
			name:     "empty string defaults to coder",
			input:    "",
			expected: AgentTypeCoder,
		},
		{
			name:     "uppercase architect",
			input:    "ARCHITECT",
			expected: AgentTypeCoder, // Case-sensitive, so falls back to default
		},
		{
			name:     "mixed case coder",
			input:    "Coder",
			expected: AgentTypeCoder, // Case-sensitive, but matches default
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParseAgentType(tt.input)
			if result != tt.expected {
				t.Errorf("expected %q, got %q", tt.expected, result)
			}
		})
	}
}

// TestAgentTypeConstants tests that constants are defined correctly.
func TestAgentTypeConstants(t *testing.T) {
	if AgentTypeArchitect != "architect" {
		t.Errorf("expected AgentTypeArchitect to be %q, got %q", "architect", AgentTypeArchitect)
	}

	if AgentTypeCoder != "coder" {
		t.Errorf("expected AgentTypeCoder to be %q, got %q", "coder", AgentTypeCoder)
	}
}

// TestAgentTypeRoundTrip tests parsing and stringifying.
func TestAgentTypeRoundTrip(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
	}{
		{"architect", AgentTypeArchitect},
		{"coder", AgentTypeCoder},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.agentType.String()
			parsed := ParseAgentType(str)

			if parsed != tt.agentType {
				t.Errorf("round trip failed: expected %q, got %q", tt.agentType, parsed)
			}
		})
	}
}
