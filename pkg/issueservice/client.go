// Package issueservice provides shared helpers for communicating with the
// maestro-issues service (HMAC signing, base URL resolution).
package issueservice

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"os"

	"orchestrator/pkg/version"
)

const defaultServiceURL = "https://issues.maestroappfactory.ai"

// BaseURL returns the issue service URL, allowing env var override.
func BaseURL() string {
	if url := os.Getenv("MAESTRO_ISSUE_SERVICE_URL"); url != "" {
		return url
	}
	return defaultServiceURL
}

// ComputeHMAC computes hex(HMAC-SHA256(message, key)) using the build-time
// issue reporting key. This is installation-auth only — it validates that the
// sender has a legitimate maestro binary, not payload integrity.
func ComputeHMAC(installationID string) string {
	return ComputeHMACWithKey(installationID, version.IssueReportingKey)
}

// ComputeHMACWithKey computes hex(HMAC-SHA256(message, key)) with an explicit key.
// Exported for testing.
func ComputeHMACWithKey(message, key string) string {
	mac := hmac.New(sha256.New, []byte(key))
	mac.Write([]byte(message))
	return hex.EncodeToString(mac.Sum(nil))
}
