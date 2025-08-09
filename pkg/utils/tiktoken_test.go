package utils

import (
	"strings"
	"testing"
)

func TestNewTokenCounter(t *testing.T) {
	tests := []struct {
		model string
		valid bool
	}{
		{"gpt-4", true},
		{"gpt-3.5-turbo", true},
		{"claude-3-sonnet", true},
		{"unknown-model", true}, // Should default to gpt-4 encoding
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			counter, err := NewTokenCounter(tt.model)
			if tt.valid && err != nil {
				t.Errorf("NewTokenCounter(%s) failed: %v", tt.model, err)
			}
			if tt.valid && counter == nil {
				t.Errorf("NewTokenCounter(%s) returned nil counter", tt.model)
			}
		})
	}
}

func TestCountTokens(t *testing.T) {
	counter, err := NewTokenCounter("gpt-4")
	if err != nil {
		t.Fatalf("Failed to create token counter: %v", err)
	}

	tests := []struct {
		text      string
		minTokens int
		maxTokens int
	}{
		{"", 0, 0},
		{"Hello", 1, 2},
		{"Hello world", 2, 3},
		{"This is a longer sentence with more words.", 8, 12},
		{strings.Repeat("word ", 100), 90, 110}, // ~100 tokens
	}

	for _, tt := range tests {
		t.Run(tt.text[:minInt(len(tt.text), 20)], func(t *testing.T) {
			tokens := counter.CountTokens(tt.text)
			if tokens < tt.minTokens || tokens > tt.maxTokens {
				t.Errorf("CountTokens(%q) = %d, want between %d and %d",
					tt.text, tokens, tt.minTokens, tt.maxTokens)
			}
		})
	}
}

func TestCountTokensSimple(t *testing.T) {
	tokens := CountTokensSimple("Hello world")
	if tokens < 2 || tokens > 3 {
		t.Errorf("CountTokensSimple(\"Hello world\") = %d, want between 2 and 3", tokens)
	}
}

func TestValidateTokenLimit(t *testing.T) {
	counter, err := NewTokenCounter("gpt-4")
	if err != nil {
		t.Fatalf("Failed to create token counter: %v", err)
	}

	tests := []struct {
		text     string
		limit    int
		expected bool
	}{
		{"short", 10, true},
		{"short", 1, true},
		{"", 0, true},
		{"a very long sentence that definitely exceeds a small token limit", 5, false},
	}

	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			result := counter.ValidateTokenLimit(tt.text, tt.limit)
			if result != tt.expected {
				t.Errorf("ValidateTokenLimit(%q, %d) = %v, want %v",
					tt.text, tt.limit, result, tt.expected)
			}
		})
	}
}

func TestTruncateToTokenLimit(t *testing.T) {
	counter, err := NewTokenCounter("gpt-4")
	if err != nil {
		t.Fatalf("Failed to create token counter: %v", err)
	}

	longText := strings.Repeat("This is a sentence. ", 50)
	truncated := counter.TruncateToTokenLimit(longText, 10)

	if len(truncated) >= len(longText) {
		t.Error("TruncateToTokenLimit should have shortened the text")
	}

	tokens := counter.CountTokens(truncated)
	if tokens > 15 { // Some margin for approximation
		t.Errorf("Truncated text has %d tokens, expected around 10", tokens)
	}
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
