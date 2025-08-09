// Package utils provides tiktoken-based token counting utilities.
package utils

import (
	"fmt"

	"github.com/tiktoken-go/tokenizer"

	"orchestrator/pkg/config"
)

// TokenCounter provides accurate token counting for different models.
type TokenCounter struct {
	codec tokenizer.Codec
}

// NewTokenCounter creates a new token counter for the specified model.
// Supported models are defined as constants in config package. All use GPT-4 encoding.
func NewTokenCounter(model string) (*TokenCounter, error) {
	// Map model names to tiktoken models, defaulting to GPT-4
	var tikModel tokenizer.Model
	switch model {
	case config.ModelOpenAIO3, config.ModelOpenAIO3Mini:
		// OpenAI O3 models use GPT-4 tokenizer
		tikModel = tokenizer.GPT4
	case config.ModelClaudeSonnet3, config.ModelClaudeSonnet4:
		// Claude uses a similar tokenization, approximate with GPT-4 encoding
		tikModel = tokenizer.GPT4
	default:
		// Default to GPT-4 encoding for unknown models
		tikModel = tokenizer.GPT4
	}

	codec, err := tokenizer.ForModel(tikModel)
	if err != nil {
		return nil, fmt.Errorf("failed to create tokenizer codec for model %s: %w", model, err)
	}

	return &TokenCounter{codec: codec}, nil
}

// CountTokens returns the number of tokens in the given text.
func (tc *TokenCounter) CountTokens(text string) int {
	if tc.codec == nil {
		// Fallback to character-based estimation (4 chars ≈ 1 token)
		return len(text) / 4
	}

	count, err := tc.codec.Count(text)
	if err != nil {
		// Fallback to character-based estimation on error
		return len(text) / 4
	}

	return count
}

// CountTokensSimple provides a simple token counting function without requiring a TokenCounter instance.
// Uses GPT-4 encoding by default.
func CountTokensSimple(text string) int {
	counter, err := NewTokenCounter("gpt-4")
	if err != nil {
		// Fallback to character-based estimation
		return len(text) / 4
	}
	return counter.CountTokens(text)
}

// ValidateTokenLimit checks if text exceeds the specified token limit.
// Returns true if within limit, false if exceeds limit.
func (tc *TokenCounter) ValidateTokenLimit(text string, limit int) bool {
	return tc.CountTokens(text) <= limit
}

// TruncateToTokenLimit truncates text to fit within the specified token limit.
// This is a rough approximation - it truncates by characters, not perfect token boundaries.
func (tc *TokenCounter) TruncateToTokenLimit(text string, limit int) string {
	currentTokens := tc.CountTokens(text)
	if currentTokens <= limit {
		return text
	}

	// Rough approximation: truncate proportionally
	ratio := float64(limit) / float64(currentTokens)
	charLimit := int(float64(len(text)) * ratio * 0.9) // 0.9 safety margin

	if charLimit >= len(text) {
		return text
	}

	return text[:charLimit] + "..."
}
