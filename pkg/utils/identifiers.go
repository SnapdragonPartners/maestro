package utils

import "strings"

// SanitizeIdentifier makes an identifier safe for Docker container names and filesystem paths.
// Docker container names must match [a-zA-Z0-9][a-zA-Z0-9_.-]*.
// This function replaces problematic characters with safe alternatives.
func SanitizeIdentifier(id string) string {
	// Replace colons with dashes (most common issue with agent IDs like "claude_sonnet4:001")
	sanitized := strings.ReplaceAll(id, ":", "-")

	// Replace any other problematic characters for Docker/filesystem safety.
	sanitized = strings.ReplaceAll(sanitized, " ", "-")
	sanitized = strings.ReplaceAll(sanitized, "/", "-")
	sanitized = strings.ReplaceAll(sanitized, "\\", "-")

	return sanitized
}

// SanitizeContainerName specifically sanitizes identifiers for Docker container naming.
// Alias for SanitizeIdentifier for clarity when used for containers.
func SanitizeContainerName(name string) string {
	return SanitizeIdentifier(name)
}
