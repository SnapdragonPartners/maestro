// Package chat provides agent chat functionality with secret scanning.
package chat

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// SecretScanner interface defines the contract for secret detection.
type SecretScanner interface {
	// Scan checks text for secrets and returns redacted text with a boolean indicating if redactions occurred.
	Scan(ctx context.Context, text string) (redactedText string, hadRedactions bool, err error)
}

// PatternScanner is a simple pattern-based secret scanner.
type PatternScanner struct {
	patterns []*regexp.Regexp
	timeout  time.Duration
}

// NewPatternScanner creates a new pattern-based scanner with default patterns.
func NewPatternScanner(timeoutMs int) *PatternScanner {
	return &PatternScanner{
		patterns: compileDefaultPatterns(),
		timeout:  time.Duration(timeoutMs) * time.Millisecond,
	}
}

// compileDefaultPatterns returns a list of compiled regex patterns for common secrets.
func compileDefaultPatterns() []*regexp.Regexp {
	patterns := []string{
		// OpenAI API keys
		`sk-[A-Za-z0-9]{48}`,
		`sk-proj-[A-Za-z0-9_-]{48,}`,

		// Anthropic API keys
		`sk-ant-[A-Za-z0-9_-]{95,}`,

		// AWS Access Keys
		`AKIA[0-9A-Z]{16}`,

		// Generic API key patterns
		`api[_-]?key[_-]?[:=]\s*['\"]?[A-Za-z0-9_-]{20,}['\"]?`,
		`apikey[_-]?[:=]\s*['\"]?[A-Za-z0-9_-]{20,}['\"]?`,

		// Generic secret patterns
		`secret[_-]?[:=]\s*['\"]?[A-Za-z0-9_-]{20,}['\"]?`,

		// Bearer tokens
		`Bearer\s+[A-Za-z0-9_-]{20,}`,

		// GitHub tokens
		`ghp_[A-Za-z0-9]{36}`,
		`gho_[A-Za-z0-9]{36}`,
		`ghu_[A-Za-z0-9]{36}`,
		`ghs_[A-Za-z0-9]{36}`,
		`ghr_[A-Za-z0-9]{36}`,

		// Private keys (PEM format)
		`-----BEGIN\s+(?:RSA|DSA|EC|OPENSSH|PGP)\s+PRIVATE\s+KEY-----`,
	}

	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		re, err := regexp.Compile(pattern)
		if err == nil {
			compiled = append(compiled, re)
		}
	}

	return compiled
}

// Scan checks the text for secrets and redacts them.
func (s *PatternScanner) Scan(ctx context.Context, text string) (string, bool, error) {
	// Create a context with timeout
	if s.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.timeout)
		defer cancel()
	}

	// Check for context cancellation
	select {
	case <-ctx.Done():
		return "", false, fmt.Errorf("context cancelled during scan initialization: %w", ctx.Err())
	default:
	}

	hadRedactions := false
	redactedText := text

	// Apply each pattern
	for _, pattern := range s.patterns {
		// Check for context cancellation between patterns
		select {
		case <-ctx.Done():
			return "", false, fmt.Errorf("context cancelled during pattern matching: %w", ctx.Err())
		default:
		}

		matches := pattern.FindAllStringIndex(redactedText, -1)
		if len(matches) > 0 {
			hadRedactions = true

			// Replace matches from end to start to preserve indices
			for i := len(matches) - 1; i >= 0; i-- {
				match := matches[i]
				start, end := match[0], match[1]
				redactedText = redactedText[:start] + "[redacted]" + redactedText[end:]
			}
		}
	}

	return redactedText, hadRedactions, nil
}

// RedactSecrets is a convenience function that applies redaction and appends the note if needed.
func RedactSecrets(ctx context.Context, scanner SecretScanner, text string) (string, error) {
	redacted, hadRedactions, err := scanner.Scan(ctx, text)
	if err != nil {
		// Fail-open: return original text on scanner error
		return text, fmt.Errorf("secret scanner error: %w", err)
	}

	if hadRedactions {
		// Append the note if not already present
		note := " (Note: content redacted by scanner)"
		if !strings.HasSuffix(redacted, note) {
			redacted += note
		}
	}

	return redacted, nil
}
