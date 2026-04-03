package utils

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// secretPatterns matches common secret/credential patterns in text.
// These are applied case-insensitively.
var secretPatterns = []*regexp.Regexp{ //nolint:gochecknoglobals // compiled regexps are intentionally package-level
	// Key-value patterns: key=value, key: value, key = "value"
	regexp.MustCompile(`(?i)(api[_-]?key|token|password|secret|credential|auth|bearer|authorization)\s*[:=]\s*["']?[^\s"']{8,}["']?`),
	// AWS-style keys
	regexp.MustCompile(`(?i)AKIA[0-9A-Z]{16}`),
	// Generic long hex/base64 tokens (40+ chars, likely a key)
	regexp.MustCompile(`(?i)(sk-|pk_|rk_|whsec_|ghp_|gho_|github_pat_)[a-zA-Z0-9_-]{20,}`),
}

// homePathPattern normalizes absolute paths containing usernames.
var homePathPattern = regexp.MustCompile(`(/Users/|/home/)[^/\s]+/`)

// SanitizeString strips secrets, normalizes paths, and truncates a string.
// Used at both evidence capture time and telemetry send time.
func SanitizeString(s string, maxLen int) string {
	if s == "" {
		return s
	}

	// Strip secrets
	for _, pat := range secretPatterns {
		s = pat.ReplaceAllString(s, "[REDACTED]")
	}

	// Normalize home directory paths
	s = homePathPattern.ReplaceAllString(s, "<user>/")

	// Truncate on rune boundaries to avoid splitting multi-byte UTF-8 characters
	if maxLen > 0 && len(s) > maxLen {
		truncated := s[:maxLen]
		// Walk back to a valid rune boundary if we split a multi-byte character
		for truncated != "" && !utf8.ValidString(truncated) {
			truncated = truncated[:len(truncated)-1]
		}
		s = truncated + "...[truncated]"
	}

	return strings.TrimSpace(s)
}
